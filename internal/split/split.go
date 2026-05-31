// Package split 은 EOA-funded-split 정산 도메인이다.
// (TS의 lib/eoa-funded-split-exec + api/eoa-funded-split/execute-with-signatures)
// POST /api/eoa-funded-split/execute-with-signatures — EIP-712 서명 + payer nonce 직렬화.
package split

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/httpx"
)

// Handler 는 정산 split HTTP 핸들러.
type Handler struct {
	deps *core.Deps
	svc  *Service
}

// NewHandler 핸들러 생성.
func NewHandler(deps *core.Deps) *Handler {
	return &Handler{deps: deps, svc: NewService(deps)}
}

type execBody struct {
	ChainID        int64  `json:"chainId"`
	ERC20Token     string `json:"erc20Token"`
	Recipient0     string `json:"recipient0"`
	Recipient1     string `json:"recipient1"`
	NativeAmount0  any    `json:"nativeAmount0"`
	NativeAmount1  any    `json:"nativeAmount1"`
	ERC20Amount0   any    `json:"erc20Amount0"`
	ERC20Amount1   any    `json:"erc20Amount1"`
	PayerKey       string `json:"payerKey"`
	PermitDeadline any    `json:"permitDeadline"`
	PermitAmount   any    `json:"permitAmount"`
	PermitV        int    `json:"permitV"`
	PermitR        string `json:"permitR"`
	PermitS        string `json:"permitS"`
}

// Execute POST /api/eoa-funded-split/execute-with-signatures.
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	var b execBody
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &b); err != nil {
		httpx.WriteValidationError(w, "invalid JSON body")
		return
	}

	chainID, err := h.deps.Reg.ResolveChainID(b.ChainID)
	if err != nil {
		httpx.WriteValidationError(w, "chainId required")
		return
	}
	if !common.IsHexAddress(strings.TrimSpace(b.ERC20Token)) {
		httpx.WriteValidationError(w, "valid erc20Token required (use 0x0 for native-only)")
		return
	}
	if !common.IsHexAddress(b.Recipient0) || !common.IsHexAddress(b.Recipient1) {
		httpx.WriteValidationError(w, "valid recipient0/recipient1 required")
		return
	}

	na0, e1 := reqUint(b.NativeAmount0, "nativeAmount0")
	na1, e2 := reqUint(b.NativeAmount1, "nativeAmount1")
	ea0, e3 := reqUint(b.ERC20Amount0, "erc20Amount0")
	ea1, e4 := reqUint(b.ERC20Amount1, "erc20Amount1")
	if msg := firstErr(e1, e2, e3, e4); msg != "" {
		httpx.WriteValidationError(w, msg)
		return
	}

	p := Params{
		ChainID:       chainID,
		ERC20Token:    common.HexToAddress(strings.TrimSpace(b.ERC20Token)),
		Recipient0:    common.HexToAddress(b.Recipient0),
		Recipient1:    common.HexToAddress(b.Recipient1),
		NativeAmount0: na0,
		NativeAmount1: na1,
		ERC20Amount0:  ea0,
		ERC20Amount1:  ea1,
		PayerKey:      b.PayerKey,
		PermitV:       uint8(b.PermitV),
	}
	if b.PermitDeadline != nil {
		if v, err := evm.ToBigInt(b.PermitDeadline); err == nil {
			p.PermitDeadline = v
		}
	}
	if b.PermitAmount != nil {
		if v, err := evm.ToBigInt(b.PermitAmount); err == nil {
			p.PermitAmount = v
		}
	}
	if r32, ok := parseBytes32(b.PermitR); ok {
		p.PermitR = r32
	}
	if s32, ok := parseBytes32(b.PermitS); ok {
		p.PermitS = s32
	}

	res, err := h.svc.Execute(r.Context(), p)
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func reqUint(v any, name string) (*big.Int, string) {
	if v == nil {
		return nil, name + " required"
	}
	n, err := evm.ToBigInt(v)
	if err != nil {
		return nil, "invalid " + name + ": " + err.Error()
	}
	if n.Sign() < 0 {
		return nil, name + " must be >= 0"
	}
	return n, ""
}

// parseBytes32 는 hex 문자열을 왼쪽 0패딩 [32]byte로 만든다(ethers.zeroPadValue 대응).
func parseBytes32(s string) ([32]byte, bool) {
	var out [32]byte
	s = strings.TrimSpace(s)
	if s == "" {
		return out, false
	}
	b, err := hexutil.Decode(s)
	if err != nil || len(b) > 32 {
		return out, false
	}
	copy(out[32-len(b):], b)
	return out, true
}

func firstErr(errs ...string) string {
	for _, e := range errs {
		if e != "" {
			return e
		}
	}
	return ""
}
