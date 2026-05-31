package signer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byunyourim/stablecoin-bundler/internal/platform/kms"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/redis"
)

// memKeystore 는 테스트용 인메모리 keystore. EncKey에 평문 hex를 그대로 둔다.
type memKeystore map[string]string // name -> hex key

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

// passthroughDecryptor 는 ciphertext를 그대로 평문으로 반환(테스트용).
type passthroughDecryptor struct{}

func (passthroughDecryptor) Decrypt(_ context.Context, _, ciphertext string) (string, error) {
	return ciphertext, nil
}

// 32바이트 hex 테스트 키 3개.
const (
	k0 = "0x1111111111111111111111111111111111111111111111111111111111111111"
	k1 = "0x2222222222222222222222222222222222222222222222222222222222222222"
	k2 = "0x3333333333333333333333333333333333333333333333333333333333333333"
)

func newTestManager(t *testing.T, ks memKeystore, env EnvKeys) *Manager {
	t.Helper()
	provider := kms.NewProvider(passthroughDecryptor{}, ks)
	locker := redis.NewLocker(nil, time.Second) // 인메모리 락
	return NewManager(provider, locker, env)
}

func TestBundlerPool_KeystoreNumericOrder(t *testing.T) {
	// _10 이 _2 보다 뒤로 정렬되어야 한다.
	ks := memKeystore{
		"bundler_key_56357_0":  k0,
		"bundler_key_56357_1":  k1,
		"bundler_key_56357_10": k2,
		"bundler_key_56357_2":  k0,
	}
	m := newTestManager(t, ks, EnvKeys{})
	pool, err := m.BundlerPool(context.Background(), 56357)
	if err != nil {
		t.Fatalf("BundlerPool: %v", err)
	}
	if len(pool) != 4 {
		t.Fatalf("pool size = %d, want 4", len(pool))
	}
}

func TestSelectBundler_RoundRobin(t *testing.T) {
	ks := memKeystore{
		"bundler_key_1_0": k0,
		"bundler_key_1_1": k1,
		"bundler_key_1_2": k2,
	}
	m := newTestManager(t, ks, EnvKeys{})
	ctx := context.Background()

	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		acc, err := m.SelectBundler(ctx, 1)
		if err != nil {
			t.Fatalf("SelectBundler: %v", err)
		}
		seen[acc.Address().Hex()]++
	}
	if len(seen) != 3 {
		t.Fatalf("round-robin touched %d distinct wallets, want 3", len(seen))
	}
	for addr, n := range seen {
		if n != 2 {
			t.Fatalf("wallet %s used %d times, want 2 (even round-robin)", addr, n)
		}
	}
}

func TestEnvKeyTakesPriority_SinglePool(t *testing.T) {
	ks := memKeystore{"bundler_key_1_0": k1, "bundler_key_1_1": k2}
	m := newTestManager(t, ks, EnvKeys{OwnerKey: k0})
	pool, err := m.BundlerPool(context.Background(), 1)
	if err != nil {
		t.Fatalf("BundlerPool: %v", err)
	}
	if len(pool) != 1 {
		t.Fatalf("env OWNER_KEY should give single-wallet pool, got %d", len(pool))
	}
}

func TestServerKeys(t *testing.T) {
	ks := memKeystore{"server_key1": k0, "server_key2": k1}
	m := newTestManager(t, ks, EnvKeys{})
	ctx := context.Background()
	s1, err := m.ServerKey1(ctx)
	if err != nil {
		t.Fatalf("ServerKey1: %v", err)
	}
	s2, err := m.ServerKey2(ctx)
	if err != nil {
		t.Fatalf("ServerKey2: %v", err)
	}
	if s1.Address() == s2.Address() {
		t.Fatal("server_key1 and server_key2 must differ")
	}
}

func TestBundlerPool_OwnerKeyFallback(t *testing.T) {
	// 풀 키가 없고 owner_key만 있으면 단일 풀(TS 하위호환).
	ks := memKeystore{"owner_key": k0}
	m := newTestManager(t, ks, EnvKeys{})
	pool, err := m.BundlerPool(context.Background(), 999)
	if err != nil {
		t.Fatalf("BundlerPool: %v", err)
	}
	if len(pool) != 1 {
		t.Fatalf("owner_key fallback should give single pool, got %d", len(pool))
	}
}
