// Package ethclient 는 체인별 go-ethereum 클라이언트를 캐시·제공한다.
// (TS의 lib/create2-factory·entrypoint의 JsonRpcProvider 캐시 대응)
//
// 실패는 호출부에서 txerror.Normalize로 분류한다(여기서 분류하지 않음).
package ethclient

import (
	"context"
	"sync"

	gethclient "github.com/ethereum/go-ethereum/ethclient"

	"github.com/byunyourim/stablecoin-bundler/internal/chain"
)

// Provider 는 체인별 *ethclient.Client 캐시.
type Provider struct {
	reg *chain.Registry

	mu      sync.Mutex
	clients map[int64]*gethclient.Client
}

// New Provider 생성.
func New(reg *chain.Registry) *Provider {
	return &Provider{reg: reg, clients: make(map[int64]*gethclient.Client)}
}

// Client 는 체인 ID의 ethclient를 반환한다(캐시). RPC URL은 chain.Registry에서 조회.
func (p *Provider) Client(ctx context.Context, chainID int64) (*gethclient.Client, error) {
	p.mu.Lock()
	c, ok := p.clients[chainID]
	p.mu.Unlock()
	if ok {
		return c, nil
	}

	url, err := p.reg.RPCURL(chainID)
	if err != nil {
		return nil, err
	}
	c, err = gethclient.DialContext(ctx, url)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// 경합 시 먼저 들어온 클라이언트 재사용.
	if existing, ok := p.clients[chainID]; ok {
		c.Close()
		return existing, nil
	}
	p.clients[chainID] = c
	return c, nil
}

// Close 는 모든 캐시된 클라이언트를 닫는다.
func (p *Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		c.Close()
	}
	p.clients = make(map[int64]*gethclient.Client)
}
