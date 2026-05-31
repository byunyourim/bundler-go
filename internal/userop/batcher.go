package userop

import (
	"context"
	"fmt"
	"math/big"
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
// 멀티번들러 모델: 체인마다 풀 크기 N개의 워커 고루틴을 띄운다(월렛당 1개).
// 워커는 공유 채널에서 그리디하게 op를 모아 자기 월렛으로 handleOps를 보낸다.
// 서로 다른 월렛은 nonce 락이 독립이라 동시 제출 → 체인당 동시 in-flight tx N개.
// 부하 분산(라운드로빈)은 "먼저 비는 워커가 다음 배치를 집는" 방식으로 자연 발생한다.
type Batcher struct {
	deps *core.Deps

	mu    sync.Mutex
	chans map[int64]chan *queuedOp
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
	return &Batcher{deps: deps, chans: make(map[int64]chan *queuedOp)}
}

// Enqueue 는 op를 체인 큐에 넣고 제출 결과(txHash)를 기다린다.
func (b *Batcher) Enqueue(ctx context.Context, chainID int64, op PackedUserOp) (string, error) {
	ch, err := b.chanFor(ctx, chainID)
	if err != nil {
		return "", err
	}
	q := &queuedOp{op: op, res: make(chan opResult, 1)}
	select {
	case ch <- q:
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

// chanFor 는 체인 큐 채널을 반환한다(없으면 풀 크기만큼 워커 시작).
func (b *Batcher) chanFor(ctx context.Context, chainID int64) (chan *queuedOp, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.chans[chainID]; ok {
		return ch, nil
	}
	pool, err := b.deps.Signers.BundlerPool(ctx, chainID)
	if err != nil {
		return nil, err
	}
	ch := make(chan *queuedOp, maxBatchSize*2)
	for _, wallet := range pool {
		go b.worker(chainID, wallet, ch)
	}
	b.chans[chainID] = ch
	b.deps.Log.Info("bundler pool started", "chainId", chainID, "wallets", len(pool))
	return ch, nil
}

// worker 는 한 핫월렛으로 채널에서 op를 그리디하게 모아 배치 제출한다.
func (b *Batcher) worker(chainID int64, wallet *signer.Account, ch chan *queuedOp) {
	for first := range ch {
		batch := []*queuedOp{first}
		// 현재 채널에 쌓인 op를 블로킹 없이 추가 수집(최대 maxBatchSize).
	drain:
		for len(batch) < maxBatchSize {
			select {
			case q := <-ch:
				batch = append(batch, q)
			default:
				break drain
			}
		}
		b.flush(chainID, wallet, batch)
	}
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

// sendUnderLock 은 월렛 nonce 락 안에서 온체인 pending nonce를 읽어 handleOps를 보내고 채굴을 기다린다.
func (b *Batcher) sendUnderLock(chainID int64, wallet *signer.Account, ops []PackedUserOp) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txWaitTimeout+15*time.Second)
	defer cancel()

	lockKey := core.NonceLockKey(chainID, wallet.Address().Hex())
	var txHash string
	err := b.deps.Locker.WithNonceLock(ctx, lockKey, func() error {
		client, err := b.deps.Clients.Client(ctx, chainID)
		if err != nil {
			return err
		}
		epAddr, err := b.deps.Reg.EntryPoint()
		if err != nil {
			return err
		}

		// 멀티프로세스 안전: nonce는 캐시하지 않고 매번 온체인 pending을 읽는다.
		nonce, err := client.PendingNonceAt(ctx, wallet.Address())
		if err != nil {
			return err
		}

		opts, err := wallet.Transactor(big.NewInt(chainID))
		if err != nil {
			return err
		}
		opts.Context = ctx
		opts.Nonce = new(big.Int).SetUint64(nonce)

		bc := bind.NewBoundContract(common.HexToAddress(epAddr), EntryPointABI(), client, client, client)
		// beneficiary=ZeroAddress → EntryPoint가 msg.sender(번들러)를 수령인으로 사용.
		tx, err := bc.Transact(opts, "handleOps", ops, common.Address{})
		if err != nil {
			return err
		}
		txHash = tx.Hash().Hex()

		waitCtx, waitCancel := context.WithTimeout(ctx, txWaitTimeout)
		defer waitCancel()
		if _, err := bind.WaitMined(waitCtx, client, tx); err != nil {
			return fmt.Errorf("tx.wait (txHash %s): %w", txHash, err)
		}
		return nil
	})
	if err != nil {
		return txHash, err
	}
	return txHash, nil
}
