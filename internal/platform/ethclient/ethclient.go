// Package ethclient 는 체인별 go-ethereum provider/signer를 제공한다.
// (TS의 lib/create2-factory·entrypoint의 JsonRpcProvider/Wallet 캐시 대응)
//
// 실패는 호출부에서 txerror.Normalize로 분류한다(여기서 분류하지 않음).
package ethclient

import "context"

// Provider 는 체인별 클라이언트 캐시.
type Provider struct {
	// TODO(골격): map[int64]*ethclient.Client, 체인별 signer(KMS)
}

// New Provider 생성.
func New() *Provider {
	return &Provider{}
}

// Client 는 체인 ID의 ethclient를 반환한다.
//
// TODO(골격): ethclient.Dial(rpcURL) 캐시.
func (p *Provider) Client(ctx context.Context, chainID int64) (any, error) {
	panic("not implemented")
}
