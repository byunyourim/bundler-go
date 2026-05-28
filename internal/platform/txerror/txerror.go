// Package txerror 는 트랜잭션/RPC 실패를 정밀 분류한다.
//
// TS 번들러 lib/tx-error.ts(normalizeTxError)의 Go 이식 — **출력 계약 동일**.
// 추출 내부만 ethers → go-ethereum으로 적응한다:
//   - rpc.DataError.ErrorData() → abi.UnpackRevert 로 revert reason 디코드
//   - rpc.Error.ErrorCode()     → 노드 JSON-RPC 에러 코드
//   - 그 외는 에러 문자열 휴리스틱(nonce/insufficient funds/timeout/network)
//
// 코드 vocabulary·category·status·retryable 매핑은 TS와 1:1로 유지한다.
// (어댑터 platform/bundler.ClassifyResponse가 이 code/retryable을 그대로 소비)
package txerror

import (
	"errors"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// Category 는 실패 분류.
type Category string

const (
	CategoryRevert            Category = "REVERT"
	CategoryInsufficientFunds Category = "INSUFFICIENT_FUNDS"
	CategoryNonce             Category = "NONCE"
	CategoryNetwork           Category = "NETWORK"
	CategoryTimeout           Category = "TIMEOUT"
	CategoryServer            Category = "SERVER"
	CategoryUnknown           Category = "UNKNOWN"
)

// Normalized 는 정규화된 실패 정보. JSON 키는 TS tx-error.ts 응답과 동일.
type Normalized struct {
	Status     int      `json:"-"`             // HTTP status (응답 body엔 미포함)
	RPCCode    int      `json:"-"`             // 노드 JSON-RPC error code (JSON-RPC 응답용)
	Category   Category `json:"category"`
	Code       string   `json:"code"`          // AC007 / BUNDLER_NONCE_CONFLICT / ...
	NativeCode string   `json:"ethersCode,omitempty"` // 계약 유지: go-ethereum 식별자를 ethersCode 자리에
	Reason     string   `json:"reason,omitempty"`
	TxHash     string   `json:"txHash,omitempty"`
	Retryable  bool     `json:"retryable"`
	Message    string   `json:"error"`
}

// revertMessages 는 온체인 revert 사유 코드 → 설명 (TS/어댑터와 동일 집합).
var revertMessages = map[string]string{
	"AC001": "caller is not EntryPoint",
	"AC004": "target call failed",
	"AC005": "native transfer failed",
	"AC006": "beneficiary not set",
	"AC007": "insufficient ERC20 balance",
	"AC008": "ERC20 transfer failed",
	"AC009": "forwarding disabled",
	"AC010": "signature verification failed",
	"EP001": "insufficient deposit",
	"EP003": "insufficient gas balance",
	"EP011": "account not deployed",
	"PM006": "insufficient token balance",
	"PM007": "insufficient token allowance",
}

var revertCodeRe = regexp.MustCompile(`\b(AC0\d{2}|EP0\d{2}|PM0\d{2})\b`)

// Normalize 는 go-ethereum 에러를 정밀 분류한다.
func Normalize(err error) *Normalized {
	if err == nil {
		return nil
	}
	msg := err.Error()
	reason, rpcCode := extractRPC(err)
	revertCode := extractRevertCode(reason, msg)
	base := firstNonEmpty(reason, msg)
	low := strings.ToLower(msg + " " + reason)

	// 1) 컨트랙트 revert
	if revertCode != "" || strings.Contains(low, "execution reverted") {
		code := revertCode
		if code == "" {
			code = "CALL_EXCEPTION"
		}
		message := base
		if rm, ok := revertMessages[revertCode]; ok {
			message = rm
		}
		return &Normalized{
			Status: 422, Category: CategoryRevert, Code: code, NativeCode: "CALL_EXCEPTION",
			Reason: firstNonEmpty(reason, revertCode), RPCCode: rpcCode, Retryable: false, Message: message,
		}
	}

	// 2) 가스/잔액 부족
	if strings.Contains(low, "insufficient funds") {
		return &Normalized{
			Status: 422, Category: CategoryInsufficientFunds, Code: "INSUFFICIENT_FUNDS",
			RPCCode: rpcCode, Retryable: false, Message: base,
		}
	}

	// 3) nonce 경합 (재시도 가능)
	if strings.Contains(low, "nonce") || strings.Contains(low, "replacement transaction underpriced") {
		return &Normalized{
			Status: 409, Category: CategoryNonce, Code: "BUNDLER_NONCE_CONFLICT",
			RPCCode: rpcCode, Retryable: true, Message: base,
		}
	}

	// 4) 타임아웃 / 네트워크 (재시도 가능)
	if strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded") {
		return &Normalized{Status: 503, Category: CategoryTimeout, Code: "RPC_TIMEOUT", RPCCode: rpcCode, Retryable: true, Message: base}
	}
	if strings.Contains(low, "connection refused") || strings.Contains(low, "connection reset") ||
		strings.Contains(low, "no such host") || strings.Contains(low, "eof") {
		return &Normalized{Status: 503, Category: CategoryNetwork, Code: "RPC_NETWORK_ERROR", RPCCode: rpcCode, Retryable: true, Message: base}
	}

	// 5) 노드 서버 에러 범위
	if rpcCode <= -32000 && rpcCode >= -32099 {
		return &Normalized{Status: 503, Category: CategoryServer, Code: "RPC_SERVER_ERROR", RPCCode: rpcCode, Retryable: true, Message: base}
	}

	// 6) 미상 → 재시도 가능한 전송 실패
	return &Normalized{Status: 500, Category: CategoryUnknown, Code: "BUNDLER_SEND_FAILED", RPCCode: rpcCode, Retryable: true, Message: base}
}

// extractRPC 는 go-ethereum 에러에서 revert reason과 JSON-RPC 코드를 뽑는다.
func extractRPC(err error) (reason string, rpcCode int) {
	var de rpc.DataError
	if errors.As(err, &de) {
		if data, ok := de.ErrorData().(string); ok {
			if b, e := hexutil.Decode(data); e == nil {
				if r, e2 := abi.UnpackRevert(b); e2 == nil {
					reason = r
				}
			}
		}
	}
	var re rpc.Error
	if errors.As(err, &re) {
		rpcCode = re.ErrorCode()
	}
	return reason, rpcCode
}

// extractRevertCode 는 reason/메시지에서 AC*/EP*/PM* 코드를 추출한다.
func extractRevertCode(reason, msg string) string {
	if reason != "" {
		if _, ok := revertMessages[reason]; ok {
			return reason // reason이 코드 그 자체
		}
	}
	for _, s := range []string{reason, msg} {
		if m := revertCodeRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
