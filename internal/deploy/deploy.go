// Package deploy 는 CREATE2 지갑 배포 도메인이다. (TS의 lib/create2-factory + api/create2/deploy)
// POST /api/create2/deploy — 디플로이어 nonce는 redis 분산락으로 직렬화.
package deploy

import "net/http"

// Handler 는 배포 HTTP 핸들러.
type Handler struct {
	// TODO(골격): ethclient.Provider, redis.Locker, kms.Provider
}

// NewHandler 핸들러 생성.
func NewHandler() *Handler {
	return &Handler{}
}

// Deploy POST /api/create2/deploy.
//
// 실패 시 httpx.WriteTxError(w, err)로 정밀 분류 응답.
//
// TODO(골격): body 파싱 → 검증 → withNonceLock → factory.deploy → 200 응답.
func (h *Handler) Deploy(w http.ResponseWriter, r *http.Request) {
	panic("not implemented")
}
