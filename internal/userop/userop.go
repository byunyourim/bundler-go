// Package userop 은 ERC-4337 JSON-RPC 번들러다. (TS의 lib/rpc + batch-queue + api/bundler)
// eth_sendUserOperation / eth_supportedEntryPoints / eth_chainId 등 처리.
// userOp을 체인별 핫월렛 풀 배치 큐에 넣어 EntryPoint.handleOps로 묶어 전송한다.
//
// 실패 시 txerror.Normalize로 분류해 JSON-RPC error.data에 {code,category,retryable} 포함.
package userop

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/httpx"
)

// Handler 는 JSON-RPC HTTP 핸들러.
type Handler struct {
	deps    *core.Deps
	batcher *Batcher
}

// NewHandler 핸들러 생성. batcher는 transfer 도메인(sendViaAccount)과 공유한다.
func NewHandler(deps *core.Deps, batcher *Batcher) *Handler {
	return &Handler{deps: deps, batcher: batcher}
}

// RPC POST /api/bundler 및 /api/bundler/{chainId} (JSON-RPC).
func (h *Handler) RPC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, errResp(nil, -32600, "Invalid Request"))
		return
	}

	var pathChainID int64
	if s := r.PathValue("chainId"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			pathChainID = n
		}
	}

	resp := h.handleRPC(r.Context(), body, pathChainID)
	// JSON-RPC는 전송 단계 성공 — 본문 error로 실패를 표현하므로 HTTP 200.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
