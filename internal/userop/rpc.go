package userop

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/txerror"
)

// rpcRequest 는 JSON-RPC 요청.
type rpcRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// rpcResponse 는 JSON-RPC 응답.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

var nullID = json.RawMessage("null")

func errResp(id json.RawMessage, code int, message string) rpcResponse {
	if id == nil {
		id = nullID
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func okResp(id json.RawMessage, result any) rpcResponse {
	if id == nil {
		id = nullID
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// handleRPC 는 JSON-RPC 요청을 처리한다. (TS rpc.ts handleRpc 대응)
// pathChainID는 path /api/bundler/<chainId>에서 온 값(없으면 0).
func (h *Handler) handleRPC(ctx context.Context, body []byte, pathChainID int64) rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Method == "" {
		return errResp(req.ID, -32600, "Invalid Request")
	}

	// chainId 결정: params[2] > path > DEFAULT_CHAIN_ID.
	var paramsChainID int64
	if req.Method == "eth_sendUserOperation" || req.Method == "eth_estimateUserOperationGas" {
		paramsChainID = parseChainIDFromParams(req.Params)
	}
	effective := paramsChainID
	if effective == 0 {
		effective = pathChainID
	}
	chainID, err := h.deps.Reg.ResolveChainID(effective)
	if err != nil {
		return errResp(req.ID, -32602, err.Error())
	}

	switch req.Method {
	case "eth_chainId":
		return okResp(req.ID, evm.HexQuantity(chainID))

	case "eth_supportedEntryPoints":
		ep, err := h.deps.Reg.EntryPoint()
		if err != nil {
			return errResp(req.ID, -32602, err.Error())
		}
		return okResp(req.ID, []string{ep})

	case "eth_estimateUserOperationGas":
		// TS와 동일 stub.
		return okResp(req.ID, map[string]string{
			"preVerificationGas":   "0x0",
			"verificationGasLimit": "0x0",
			"callGasLimit":         "0x0",
		})

	case "eth_sendUserOperation":
		return h.handleSendUserOp(ctx, req, chainID)

	default:
		return errResp(req.ID, -32601, "Method not found")
	}
}

func (h *Handler) handleSendUserOp(ctx context.Context, req rpcRequest, chainID int64) rpcResponse {
	if len(req.Params) == 0 {
		return errResp(req.ID, -32602, "Missing userOp")
	}
	var uo UserOperation
	if err := json.Unmarshal(req.Params[0], &uo); err != nil {
		return errResp(req.ID, -32602, "invalid userOp: "+err.Error())
	}
	packed, err := uo.toPacked()
	if err != nil {
		return errResp(req.ID, -32602, err.Error())
	}

	txHash, err := h.batcher.Enqueue(ctx, chainID, packed)
	if err != nil {
		return h.txErrorResp(req.ID, err)
	}
	return okResp(req.ID, txHash)
}

// txErrorResp 는 전송 실패를 txerror로 분류해 JSON-RPC error.data에 담는다(TS rpc.ts catch 대응).
func (h *Handler) txErrorResp(id json.RawMessage, err error) rpcResponse {
	n := txerror.Normalize(err)
	if n == nil {
		return errResp(id, -32000, err.Error())
	}
	code := n.RPCCode
	if code == 0 {
		code = -32000
	}
	if id == nil {
		id = nullID
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: n.Message,
			Data: map[string]any{
				"code":       n.Code,
				"category":   n.Category,
				"ethersCode": n.NativeCode,
				"txHash":     n.TxHash,
				"retryable":  n.Retryable,
			},
		},
	}
}

// parseChainIDFromParams 는 params[2](number 또는 hex/dec 문자열)에서 chainId를 뽑는다.
func parseChainIDFromParams(params []json.RawMessage) int64 {
	if len(params) < 3 {
		return 0
	}
	raw := params[2]
	// number?
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		if n, err := num.Int64(); err == nil {
			return n
		}
	}
	// string?
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			if n, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
				return n
			}
		} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
