// Package httpx 는 HTTP 응답 헬퍼다.
// 트랜잭션/RPC 실패는 txerror.Normalize로 정밀 분류해 구조화 에러 응답을 낸다
// (어댑터가 code/category/retryable로 재시도·DLQ 판단).
package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/byunyourim/stablecoin-bundler/internal/platform/txerror"
)

// WriteJSON 은 v를 JSON으로 status와 함께 쓴다.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteValidationError 는 400 입력 검증 실패 응답을 쓴다.
func WriteValidationError(w http.ResponseWriter, msg string) {
	WriteJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

// WriteTxError 는 트랜잭션/RPC 실패를 정밀 분류해 구조화 응답을 쓴다.
// 응답 body: { error, code, category, ethersCode, reason, txHash, retryable }
func WriteTxError(w http.ResponseWriter, err error) {
	n := txerror.Normalize(err)
	if n == nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "unknown error"})
		return
	}
	WriteJSON(w, n.Status, n)
}
