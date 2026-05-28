// Package userop 은 ERC-4337 JSON-RPC 번들러다. (TS의 lib/rpc + batch-queue + api/bundler)
// eth_sendUserOperation / eth_supportedEntryPoints / eth_chainId 등 처리.
// userOp을 batch queue에 넣어 EntryPoint.handleOps로 묶어 전송.
//
// TODO(골격): handleRpc + enqueueOp 구현.
// 실패 시 txerror.Normalize로 분류해 JSON-RPC error.data에 {code,category,retryable} 포함.
package userop

import "net/http"

// Handler 는 JSON-RPC HTTP 핸들러.
type Handler struct{}

// NewHandler 핸들러 생성.
func NewHandler() *Handler { return &Handler{} }

// RPC POST /api/bundler 및 /api/bundler/{chainId} (JSON-RPC).
func (h *Handler) RPC(w http.ResponseWriter, r *http.Request) {
	panic("not implemented")
}
