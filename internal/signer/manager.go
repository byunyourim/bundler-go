package signer

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/byunyourim/stablecoin-bundler/internal/platform/kms"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/redis"
)

// EnvKeys 는 로컬/테스트용 env fallback 키(64 hex). 운영에서는 비움.
type EnvKeys struct {
	OwnerKey   string // OWNER_KEY
	BundlerKey string // BUNDLER_PRIVATE_KEY (OWNER_KEY 대체)
	ServerKey1 string // SERVER_KEY1
	ServerKey2 string // SERVER_KEY2
}

// Manager 는 번들러 핫월렛 풀 + server_key1/2를 lazy 로드·캐시한다.
//
// 키 우선순위(TS bundler-key.ts와 동일): env > keystore+KMS.
type Manager struct {
	kms    *kms.Provider
	locker *redis.Locker
	env    EnvKeys

	mu      sync.Mutex
	pools   map[int64][]*Account // 체인별 핫월렛 풀
	server1 *Account
	server2 *Account
}

// NewManager 생성. kms는 nil 가능(env 전용 모드).
func NewManager(provider *kms.Provider, locker *redis.Locker, env EnvKeys) *Manager {
	return &Manager{
		kms:    provider,
		locker: locker,
		env:    env,
		pools:  make(map[int64][]*Account),
	}
}

// BundlerPool 은 체인의 핫월렛 풀을 반환한다(lazy 로드 + 캐시).
func (m *Manager) BundlerPool(ctx context.Context, chainID int64) ([]*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[chainID]; ok {
		return p, nil
	}
	pool, err := m.buildPool(ctx, chainID)
	if err != nil {
		return nil, err
	}
	m.pools[chainID] = pool
	return pool, nil
}

func (m *Manager) buildPool(ctx context.Context, chainID int64) ([]*Account, error) {
	// 1) env OWNER_KEY / BUNDLER_PRIVATE_KEY → 단일 풀 (로컬/테스트)
	if envKey := firstNonEmpty(m.env.OwnerKey, m.env.BundlerKey); envKey != "" {
		acc, err := newAccount(envKey)
		if err != nil {
			return nil, fmt.Errorf("OWNER_KEY/BUNDLER_PRIVATE_KEY: %w", err)
		}
		return []*Account{acc}, nil
	}

	// 2) keystore bundler_key_<chainId>_<idx> → 풀
	if m.kms.Enabled() {
		prefix := fmt.Sprintf("bundler_key_%d_", chainID)
		names, err := m.kms.ListNames(prefix)
		if err != nil {
			return nil, err
		}
		if len(names) > 0 {
			sortByTrailingIndex(names)
			pool := make([]*Account, 0, len(names))
			for _, name := range names {
				acc, err := m.loadAccount(ctx, name)
				if err != nil {
					return nil, err
				}
				pool = append(pool, acc)
			}
			return pool, nil
		}

		// 3) keystore owner_key (TS 단일 키 하위호환) → 단일 풀
		if acc, found, err := m.tryLoad(ctx, "owner_key"); err != nil {
			return nil, err
		} else if found {
			return []*Account{acc}, nil
		}
	}

	return nil, fmt.Errorf(
		"bundler key not configured for chain %d: set OWNER_KEY/BUNDLER_PRIVATE_KEY, or keystore bundler_key_%d_<idx> (or owner_key) with KMS",
		chainID, chainID)
}

// SelectBundler 는 풀에서 라운드로빈으로 핫월렛 1개를 고른다(throughput용).
func (m *Manager) SelectBundler(ctx context.Context, chainID int64) (*Account, error) {
	pool, err := m.BundlerPool(ctx, chainID)
	if err != nil {
		return nil, err
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	idx, err := m.locker.NextIndex(ctx, strconv.FormatInt(chainID, 10), len(pool))
	if err != nil {
		return nil, err
	}
	return pool[idx], nil
}

// PrimaryBundler 는 결정론적/관리성 작업(create2 deploy, entrypoint deposit/withdraw)용
// 고정 월렛(pool[0])을 반환한다. 라운드로빈하지 않는다.
func (m *Manager) PrimaryBundler(ctx context.Context, chainID int64) (*Account, error) {
	pool, err := m.BundlerPool(ctx, chainID)
	if err != nil {
		return nil, err
	}
	return pool[0], nil
}

// ServerKey1 은 server_key1 EOA(2-of-3 첫 서명자 / wallet 전송 / payer)를 반환한다.
func (m *Manager) ServerKey1(ctx context.Context) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server1 != nil {
		return m.server1, nil
	}
	acc, err := m.loadServerKey(ctx, "server_key1", m.env.ServerKey1, "SERVER_KEY1")
	if err != nil {
		return nil, err
	}
	m.server1 = acc
	return acc, nil
}

// ServerKey2 은 server_key2 EOA(2-of-3 둘째 서명자)를 반환한다.
func (m *Manager) ServerKey2(ctx context.Context) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server2 != nil {
		return m.server2, nil
	}
	acc, err := m.loadServerKey(ctx, "server_key2", m.env.ServerKey2, "SERVER_KEY2")
	if err != nil {
		return nil, err
	}
	m.server2 = acc
	return acc, nil
}

func (m *Manager) loadServerKey(ctx context.Context, keystoreName, envKey, envVar string) (*Account, error) {
	if envKey != "" {
		acc, err := newAccount(envKey)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", envVar, err)
		}
		return acc, nil
	}
	if acc, found, err := m.tryLoad(ctx, keystoreName); err != nil {
		return nil, err
	} else if found {
		return acc, nil
	}
	return nil, fmt.Errorf("%s not configured: set %s env, or keystore row %s (KeyID+EncKey) with KMS",
		keystoreName, envVar, keystoreName)
}

// loadAccount 는 keystore name을 KMS로 복호화해 Account를 만든다(필수).
func (m *Manager) loadAccount(ctx context.Context, name string) (*Account, error) {
	acc, found, err := m.tryLoad(ctx, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("keystore key %q not found", name)
	}
	return acc, nil
}

func (m *Manager) tryLoad(ctx context.Context, name string) (*Account, bool, error) {
	hexKey, found, err := m.kms.LoadHexKey(ctx, name)
	if err != nil || !found {
		return nil, found, err
	}
	acc, err := newAccount(hexKey)
	if err != nil {
		return nil, false, fmt.Errorf("keystore key %q: %w", name, err)
	}
	return acc, true, nil
}

// sortByTrailingIndex 는 name 끝의 _<int> 인덱스 기준 숫자 정렬한다(_2 < _10).
func sortByTrailingIndex(names []string) {
	sort.Slice(names, func(i, j int) bool {
		return trailingIndex(names[i]) < trailingIndex(names[j])
	})
}

func trailingIndex(name string) int {
	i := strings.LastIndexByte(name, '_')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(name[i+1:])
	if err != nil {
		return 0
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
