// Package chain 은 체인별 설정(RPC URL, Factory/EntryPoint 주소)을 제공한다.
// (TS의 lib/config 대응) 순수 도메인 — env 조회만 하고 외부 IO 없음.
//
// 동적 키(RPC_URL_<chainId> 등)는 정적 struct로 표현할 수 없어 lookup 함수로 조회한다.
// 우선순위는 TS config와 동일: <키>_<chainId> > <키> 기본값.
package chain

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 는 한 체인의 번들러 설정 스냅샷.
type Config struct {
	ChainID        int64
	RPCURL         string
	EntryPoint     string
	Create2Factory string // 없을 수 있음
	EOFSProxy      string // 없을 수 있음
}

// Registry 는 체인 ID → 설정 조회. env lookup은 주입 가능(테스트용).
type Registry struct {
	getenv func(string) string
}

// NewRegistry 는 os.Getenv 기반 Registry를 만든다.
func NewRegistry() *Registry { return NewRegistryWithLookup(os.Getenv) }

// NewRegistryWithLookup 는 lookup 함수를 주입한 Registry를 만든다.
func NewRegistryWithLookup(getenv func(string) string) *Registry {
	return &Registry{getenv: getenv}
}

func (r *Registry) lookup(key string) string {
	return strings.TrimSpace(r.getenv(key))
}

// perChainOrDefault 는 <key>_<chainId> 우선, 없으면 <key> 기본값을 반환한다.
func (r *Registry) perChainOrDefault(key string, chainID int64) string {
	if v := r.lookup(fmt.Sprintf("%s_%d", key, chainID)); v != "" {
		return v
	}
	return r.lookup(key)
}

// RPCURL 은 체인 ID의 RPC URL을 반환한다. RPC_URL_<chainId> 우선, 없으면 RPC_URL.
func (r *Registry) RPCURL(chainID int64) (string, error) {
	url := r.perChainOrDefault("RPC_URL", chainID)
	if url == "" {
		return "", fmt.Errorf("no RPC URL for chainId %d: set RPC_URL or RPC_URL_%d", chainID, chainID)
	}
	return url, nil
}

// EntryPoint 는 EntryPoint 주소(체인 무관, 동일 주소)를 반환한다.
func (r *Registry) EntryPoint() (string, error) {
	v := r.lookup("ENTRYPOINT_ADDRESS")
	if v == "" {
		return "", fmt.Errorf("missing ENTRYPOINT_ADDRESS")
	}
	return v, nil
}

// Create2Factory 는 Factory 주소를 반환한다(없으면 ""). CREATE2_FACTORY_ADDRESS_<chainId> 우선.
func (r *Registry) Create2Factory(chainID int64) string {
	return r.perChainOrDefault("CREATE2_FACTORY_ADDRESS", chainID)
}

// EOFSProxy 는 EoaFundedSplit 라우터 주소를 반환한다. EOFS_PROXY_ADDRESS_<chainId> 우선.
func (r *Registry) EOFSProxy(chainID int64) (string, error) {
	v := r.perChainOrDefault("EOFS_PROXY_ADDRESS", chainID)
	if v == "" {
		return "", fmt.Errorf("EoaFundedSplit proxy not configured: set EOFS_PROXY_ADDRESS_%d or EOFS_PROXY_ADDRESS", chainID)
	}
	return v, nil
}

// DefaultChainID 는 DEFAULT_CHAIN_ID(있으면)를 반환한다.
func (r *Registry) DefaultChainID() (int64, bool) {
	v := r.lookup("DEFAULT_CHAIN_ID")
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ResolveChainID 는 후보 chainId(없으면 0)와 DEFAULT_CHAIN_ID로 유효 체인을 결정한다.
// (TS rpc.ts resolveChainId 대응)
func (r *Registry) ResolveChainID(candidate int64) (int64, error) {
	if candidate != 0 {
		return candidate, nil
	}
	if d, ok := r.DefaultChainID(); ok {
		return d, nil
	}
	return 0, fmt.Errorf("chainId required: use path /api/bundler/<chainId>, pass chainId in params, or set DEFAULT_CHAIN_ID")
}

// Get 은 체인 설정 스냅샷을 반환한다. RPC URL·EntryPoint 누락 시 error.
func (r *Registry) Get(chainID int64) (Config, error) {
	rpcURL, err := r.RPCURL(chainID)
	if err != nil {
		return Config{}, err
	}
	ep, err := r.EntryPoint()
	if err != nil {
		return Config{}, err
	}
	return Config{
		ChainID:        chainID,
		RPCURL:         rpcURL,
		EntryPoint:     ep,
		Create2Factory: r.Create2Factory(chainID),
		EOFSProxy:      r.perChainOrDefault("EOFS_PROXY_ADDRESS", chainID),
	}, nil
}

// AssertConfig 는 ENTRYPOINT_ADDRESS + 최소 1개 RPC URL을 검증한다. (TS assertConfig 대응)
func (r *Registry) AssertConfig() error {
	if _, err := r.EntryPoint(); err != nil {
		return err
	}
	if r.lookup("RPC_URL") != "" {
		return nil
	}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "RPC_URL_") {
			if i := strings.IndexByte(kv, '='); i >= 0 && strings.TrimSpace(kv[i+1:]) != "" {
				return nil
			}
		}
	}
	return fmt.Errorf("missing RPC: set RPC_URL or RPC_URL_<chainId>")
}
