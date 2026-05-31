package userop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/kms"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/redis"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

// --- 테스트용 인메모리 keystore (EncKey에 평문 hex) ---

type memKeystore map[string]string

func (m memKeystore) Lookup(name string) (kms.KeyEntry, bool, error) {
	v, ok := m[name]
	if !ok {
		return kms.KeyEntry{}, false, nil
	}
	return kms.KeyEntry{KeyID: name, CiphertextBase64: v}, true, nil
}
func (m memKeystore) ListByPrefix(prefix string) ([]string, error) {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (m memKeystore) Close() error { return nil }

type passthroughDecryptor struct{}

func (passthroughDecryptor) Decrypt(_ context.Context, _, ct string) (string, error) { return ct, nil }

// fakeChain 은 체인 제출(submit)을 흉내내며 동시성·nonce·분산을 기록한다.
type fakeChain struct {
	mu            sync.Mutex
	nextNonce     map[string]uint64 // wallet -> 다음 nonce
	usedNonce     map[string]map[uint64]int
	opsPerWallet  map[string]int
	active        map[string]bool // 현재 제출 중인 월렛 (같은 월렛 중복 = 락 실패)
	overlapSame   bool            // 같은 월렛에서 동시 제출 감지
	cur, maxCross int             // 서로 다른 월렛 동시 제출 최대치
}

func newFakeChain() *fakeChain {
	return &fakeChain{
		nextNonce:    map[string]uint64{},
		usedNonce:    map[string]map[uint64]int{},
		opsPerWallet: map[string]int{},
		active:       map[string]bool{},
	}
}

func (f *fakeChain) submit(_ context.Context, _ int64, wallet *signer.Account, ops []PackedUserOp) (string, error) {
	addr := wallet.Address().Hex()

	f.mu.Lock()
	if f.active[addr] {
		f.overlapSame = true // 락이 깨졌다 — 같은 월렛 동시 제출
	}
	f.active[addr] = true
	f.cur++
	if f.cur > f.maxCross {
		f.maxCross = f.cur
	}
	nonce := f.nextNonce[addr]
	f.nextNonce[addr] = nonce + 1
	if f.usedNonce[addr] == nil {
		f.usedNonce[addr] = map[uint64]int{}
	}
	f.usedNonce[addr][nonce]++
	f.opsPerWallet[addr] += len(ops)
	f.mu.Unlock()

	time.Sleep(3 * time.Millisecond) // 채굴 흉내 — race 창 확대

	f.mu.Lock()
	f.active[addr] = false
	f.cur--
	f.mu.Unlock()

	return fmt.Sprintf("0x%s_%d", addr[2:8], nonce), nil
}

func newTestBatcher(t *testing.T, poolSize int) (*Batcher, *fakeChain) {
	t.Helper()
	ks := memKeystore{}
	// bundler_key_1_0 .. _N (서로 다른 32B 키)
	for i := 0; i < poolSize; i++ {
		ks[fmt.Sprintf("bundler_key_1_%d", i)] = fmt.Sprintf("0x%064x", i+1)
	}
	provider := kms.NewProvider(passthroughDecryptor{}, ks)
	locker := redis.NewLocker(nil, time.Second) // 인메모리 락 + 커서
	signers := signer.NewManager(provider, locker, signer.EnvKeys{})
	deps := &core.Deps{
		Signers: signers,
		Locker:  locker,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	b := NewBatcher(deps)
	fc := newFakeChain()
	b.submit = fc.submit // 체인 I/O만 페이크로 교체
	return b, fc
}

// 순차 제출 시 라운드로빈 커서가 풀을 고르게 순회하는지(배치=1건씩).
func TestBatcher_RoundRobinDistribution(t *testing.T) {
	const poolSize, rounds = 3, 4
	b, fc := newTestBatcher(t, poolSize)
	ctx := context.Background()

	for i := 0; i < poolSize*rounds; i++ {
		if _, err := b.Enqueue(ctx, 1, PackedUserOp{Nonce: big.NewInt(int64(i))}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	if len(fc.opsPerWallet) != poolSize {
		t.Fatalf("used %d wallets, want all %d (round-robin)", len(fc.opsPerWallet), poolSize)
	}
	for addr, n := range fc.opsPerWallet {
		if n != rounds {
			t.Fatalf("wallet %s handled %d ops, want %d (even round-robin)", addr, n, rounds)
		}
	}
}

// 다수 동시 제출 시: 월렛별 직렬화(같은 월렛 동시 제출 없음) + 교차 월렛 병렬성 + nonce 무충돌.
func TestBatcher_ConcurrentSerializationAndParallelism(t *testing.T) {
	const poolSize, total = 3, 90
	b, fc := newTestBatcher(t, poolSize)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, err := b.Enqueue(ctx, 1, PackedUserOp{Nonce: big.NewInt(int64(n))}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("enqueue error: %v", err)
	}

	// 1) 같은 월렛에서 동시 제출이 없어야 한다(WithNonceLock 직렬화 증명).
	if fc.overlapSame {
		t.Fatal("same-wallet concurrent submit detected — per-wallet nonce lock failed")
	}
	// 2) 서로 다른 월렛은 병렬 제출되어야 한다(멀티번들러 동시성 증명).
	if fc.maxCross < 2 {
		t.Fatalf("max cross-wallet concurrency = %d, want >= 2 (no multi-bundler parallelism)", fc.maxCross)
	}
	// 3) 월렛별 nonce 중복 없음 + 전체 op 수 일치.
	seenOps := 0
	for addr, nonces := range fc.usedNonce {
		for nonce, count := range nonces {
			if count != 1 {
				t.Fatalf("wallet %s nonce %d used %d times (collision)", addr, nonce, count)
			}
		}
	}
	for _, n := range fc.opsPerWallet {
		seenOps += n
	}
	if seenOps != total {
		t.Fatalf("handled %d ops, want %d", seenOps, total)
	}
}
