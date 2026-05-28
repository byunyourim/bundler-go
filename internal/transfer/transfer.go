// Package transfer 는 native/erc20 전송 도메인이다. (TS의 lib/transfer + api/transfer)
// POST /api/transfer — source: wallet(EOA 키) | account(서명 UserOp).
//
// TODO(골격): deploy 패턴(Handler + httpx.WriteTxError) 따라 구현.
package transfer

import "net/http"

// Handler 는 전송 HTTP 핸들러.
type Handler struct{}

// NewHandler 핸들러 생성.
func NewHandler() *Handler { return &Handler{} }

// Transfer POST /api/transfer.
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	panic("not implemented")
}
