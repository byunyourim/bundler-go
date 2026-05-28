// Package split 은 EOA-funded-split 정산 도메인이다.
// (TS의 lib/eoa-funded-split-exec + api/eoa-funded-split/execute-with-signatures)
// POST /api/eoa-funded-split/execute-with-signatures — EIP-712 서명 + payer nonce 직렬화.
//
// TODO(골격): deploy 패턴 따라 구현.
package split

import "net/http"

// Handler 는 정산 split HTTP 핸들러.
type Handler struct{}

// NewHandler 핸들러 생성.
func NewHandler() *Handler { return &Handler{} }

// Execute POST /api/eoa-funded-split/execute-with-signatures.
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	panic("not implemented")
}
