// Package chain 은 체인별 설정(RPC URL, Factory/EntryPoint 주소)을 제공한다.
// (TS의 lib/config 대응) 순수 도메인 — 외부 IO 없음.
package chain

// Config 는 한 체인의 번들러 설정.
type Config struct {
	ChainID         int64
	RPCURL          string
	Create2Factory  string
	EntryPoint      string
}

// Registry 는 체인 ID → Config 조회.
type Registry struct {
	// TODO(골격): map[int64]Config (env RPC_URL_<chainId> 등에서 로드)
}

// Get 은 체인 설정을 반환한다.
//
// TODO(골격): 구현.
func (r *Registry) Get(chainID int64) (Config, bool) {
	panic("not implemented")
}
