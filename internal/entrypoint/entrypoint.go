// Package entrypoint 는 EntryPoint 가스풀 deposit/withdraw 도메인이다.
// (TS의 lib/entrypoint + api/entrypoint/transfer) POST /api/entrypoint/transfer.
//
// TODO(골격): deploy 패턴 따라 구현.
package entrypoint

import "net/http"

// Handler 는 가스풀 HTTP 핸들러.
type Handler struct{}

// NewHandler 핸들러 생성.
func NewHandler() *Handler { return &Handler{} }

// Transfer POST /api/entrypoint/transfer (DEPOSIT / WITHDRAW).
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	panic("not implemented")
}
