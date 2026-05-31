package userop

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

const (
	maxBatchSize  = 50
	txWaitTimeout = 60 * time.Second
)

// Batcher 는 체인별 핫월렛 풀 위에서 UserOp를 묶어 handleOps로 제출한다.
//
// 멀티번들러 모델: 체인마다 디스패처 1개가 채널에서 op를 그리디하게 모아 배치를 만들고,
// redis 라운드로빈 커서(Locker.NextIndex)로 풀에서 핫월렛을 골라 그 월렛으로 handleOps를
// 보낸다. 동시 flush는 풀 크기만큼 허용(semaphore)하며, 서로 다른 월렛은 nonce 락이 독립이라
// 동시 제출된다 → 체인당 동시 in-flight tx N개. 라운드로빈 커서는 redis에 있어 멀티프로세스에서도
// 전역적으로 부하가 분산된다(redis 미설정 시 프로세스 내 카운터로 폴백).
type Batcher struct {
	deps *core.Deps

	// submit 은 락 안에서 실제 체인 제출(nonce 조회 + handleOps + 채굴 대기)을 수행한다.
	// 기본값은 onchainSubmit. 테스트에서 체인 I/O를 페이크로 주입하는 seam.
	submit submitFunc

	mu     sync.Mutex
	chains map[int64]*chainQueue
}

// submitFunc 는 락 보유 상태에서 ops를 wallet으로 제출하고 txHash를 반환한다.
type submitFunc func(ctx context.Context, chainID int64, wallet *signer.Account, ops []PackedUserOp) (string, error)

type chainQueue struct {
	ch   chan *queuedOp
	pool []*signer.Account
	sem  chan struct{} // 동시 flush 상한 = 풀 크기
}

type queuedOp struct {
	op  PackedUserOp
	res chan opResult
}

type opResult struct {
	txHash string
	err    error
}

// NewBatcher 생성.
func NewBatcher(deps *core.Deps) *Batcher {
	b := &Batcher{deps: deps, chains: make(map[int64]*chainQueue)}
	b.submit = b.onchainSubmit
	return b
}

// Enqueue 는 op를 체인 큐에 넣고 제출 결과(txHash)를 기다린다.
func (b *Batcher) Enqueue(ctx context.Context, chainID int64, op PackedUserOp) (string, error) {
	cq, err := b.queueFor(ctx, chainID)
	if err != nil {
		return "", err
	}
	q := &queuedOp{op: op, res: make(chan opResult, 1)}
	select {
	case cq.ch <- q:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case r := <-q.res:
		return r.txHash, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// queueFor 는 체인 큐를 반환한다(없으면 풀 로드 + 디스패처 시작).
func (b *Batcher) queueFor(ctx context.Context, chainID int64) (*chainQueue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cq, ok := b.chains[chainID]; ok {
		return cq, nil
	}
	pool, err := b.deps.Signers.BundlerPool(ctx, chainID)
	if err != nil {
		return nil, err
	}
	cq := &chainQueue{
		ch:   make(chan *queuedOp, maxBatchSize*2),
		pool: pool,
		sem:  make(chan struct{}, len(pool)),
	}
	b.chains[chainID] = cq
	go b.dispatch(chainID, cq)
	b.deps.Log.Info("bundler pool started", "chainId", chainID, "wallets", len(pool))
	return cq, nil
}

// dispatch 는 채널에서 배치를 모아 라운드로빈으로 월렛을 골라 flush를 띄운다.
func (b *Batcher) dispatch(chainID int64, cq *chainQueue) {
	cursorKey := strconv.FormatInt(chainID, 10)
	for first := range cq.ch {
		batch := []*queuedOp{first}
	drain:
		for len(batch) < maxBatchSize {
			select {
			case q := <-cq.ch:
				batch = append(batch, q)
			default:
				break drain
			}
		}

		wallet := cq.pool[b.nextIndex(cursorKey, len(cq.pool))]
		cq.sem <- struct{}{} // 동시 flush 상한
		go func(w *signer.Account, ops []*queuedOp) {
			defer func() { <-cq.sem }()
			b.flush(chainID, w, ops)
		}(wallet, batch)
	}
}

// nextIndex 는 redis 라운드로빈 커서로 풀 인덱스를 고른다(실패 시 0).
func (b *Batcher) nextIndex(cursorKey string, n int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	idx, err := b.deps.Locker.NextIndex(ctx, cursorKey, n)
	if err != nil {
		b.deps.Log.Warn("round-robin cursor failed, using wallet 0", "err", err)
		return 0
	}
	return idx
}

// flush 는 배치를 1개 handleOps로 보내고, 실패 시 1건 이상이면 개별 재시도한다.(TS drain 대응)
func (b *Batcher) flush(chainID int64, wallet *signer.Account, batch []*queuedOp) {
	ops := make([]PackedUserOp, len(batch))
	for i, q := range batch {
		ops[i] = q.op
	}

	txHash, err := b.sendUnderLock(chainID, wallet, ops)
	if err == nil {
		for _, q := range batch {
			q.res <- opResult{txHash: txHash}
		}
		b.deps.Log.Info("batch sent", "chainId", chainID, "wallet", wallet.Address().Hex(), "batchSize", len(ops), "txHash", txHash)
		return
	}

	if len(batch) > 1 {
		b.deps.Log.Warn("batch failed, retrying individually", "chainId", chainID, "batchSize", len(batch), "err", err)
		for _, q := range batch {
			h, e := b.sendUnderLock(chainID, wallet, []PackedUserOp{q.op})
			q.res <- opResult{txHash: h, err: e}
		}
		return
	}

	b.deps.Log.Error("batch send failed", "chainId", chainID, "err", err)
	batch[0].res <- opResult{err: err}
}

// sendUnderLock 은 월렛 nonce 락을 잡고 submit(기본 onchainSubmit)을 실행한다.
// 락이 월렛별 nonce 줄을 전역 단일 직렬화한다(멀티프로세스 안전).
func (b *Batcher) sendUnderLock(chainID int64, wallet *signer.Account, ops []PackedUserOp) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txWaitTimeout+15*time.Second)
	defer cancel()

	lockKey := core.NonceLockKey(chainID, wallet.Address().Hex())
	var txHash string
	err := b.deps.Locker.WithNonceLock(ctx, lockKey, func() error {
		h, err := b.submit(ctx, chainID, wallet, ops)
		txHash = h
		return err
	})
	return txHash, err
}

// onchainSubmit 은 온체인 pending nonce를 읽어 handleOps를 보내고 채굴을 기다린다.
func (b *Batcher) onchainSubmit(ctx context.Context, chainID int64, wallet *signer.Account, ops []PackedUserOp) (string, error) {
	client, err := b.deps.Clients.Client(ctx, chainID)
	if err != nil {
		return "", err
	}
	epAddr, err := b.deps.Reg.EntryPoint()
	if err != nil {
		return "", err
	}

	// 멀티프로세스 안전: nonce는 캐시하지 않고 매번 온체인 pending을 읽는다.
	nonce, err := client.PendingNonceAt(ctx, wallet.Address())
	if err != nil {
		return "", err
	}

	opts, err := wallet.Transactor(big.NewInt(chainID))
	if err != nil {
		return "", err
	}
	opts.Context = ctx
	opts.Nonce = new(big.Int).SetUint64(nonce)

	bc := bind.NewBoundContract(common.HexToAddress(epAddr), EntryPointABI(), client, client, client)
	// beneficiary=ZeroAddress → EntryPoint가 msg.sender(번들러)를 수령인으로 사용.
	tx, err := bc.Transact(opts, "handleOps", ops, common.Address{})
	if err != nil {
		return "", err
	}
	txHash := tx.Hash().Hex()

	waitCtx, waitCancel := context.WithTimeout(ctx, txWaitTimeout)
	defer waitCancel()
	if _, err := bind.WaitMined(waitCtx, client, tx); err != nil {
		return txHash, fmt.Errorf("tx.wait (txHash %s): %w", txHash, err)
	}
	return txHash, nil
}
