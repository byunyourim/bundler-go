// Package transfer 는 native/erc20 전송 도메인이다. (TS의 lib/transfer + api/transfer)
// POST /api/transfer — source: wallet(server_key1/2 직접) | account(2-of-3 서명 UserOp).
package transfer

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/httpx"
	"github.com/byunyourim/stablecoin-bundler/internal/userop"
)

// Handler 는 전송 HTTP 핸들러.
type Handler struct {
	deps *core.Deps
	svc  *Service
}

// NewHandler 핸들러 생성. batcher는 userop과 공유.
func NewHandler(deps *core.Deps, batcher *userop.Batcher) *Handler {
	return &Handler{deps: deps, svc: NewService(deps, batcher)}
}

type transferBody struct {
	ChainID      int64  `json:"chainId"`
	Type         string `json:"type"`
	Source       string `json:"source"`
	From         string `json:"from"`
	To           string `json:"to"`
	ValueWei     any    `json:"valueWei"`
	TokenAddress string `json:"tokenAddress"`
	Amount       any    `json:"amount"`
}

// Transfer POST /api/transfer.
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	var body transferBody
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		httpx.WriteValidationError(w, "invalid JSON body")
		return
	}

	chainID, err := h.deps.Reg.ResolveChainID(body.ChainID)
	if err != nil {
		httpx.WriteValidationError(w, "chainId required")
		return
	}
	typ := strings.ToLower(strings.TrimSpace(body.Type))
	source := strings.ToLower(strings.TrimSpace(body.Source))
	if typ != "native" && typ != "erc20" {
		httpx.WriteValidationError(w, "type required: 'native' or 'erc20'")
		return
	}
	if source != "wallet" && source != "account" {
		httpx.WriteValidationError(w, "source required: 'wallet' or 'account'")
		return
	}
	to := strings.TrimSpace(body.To)
	if !common.IsHexAddress(to) {
		httpx.WriteValidationError(w, "valid 'to' required")
		return
	}
	from := strings.TrimSpace(body.From)
	if !common.IsHexAddress(from) {
		hint := "Account address"
		if source == "wallet" {
			hint = "must equal server_key1 or server_key2 EOA"
		}
		httpx.WriteValidationError(w, "source="+source+" requires valid 'from' ("+hint+")")
		return
	}
	toAddr := common.HexToAddress(to)
	fromAddr := common.HexToAddress(from)
	ctx := r.Context()

	if typ == "native" {
		value, perr := parseBig(body.ValueWei, false)
		if perr != "" {
			httpx.WriteValidationError(w, perr)
			return
		}
		var res Result
		if source == "account" {
			res, err = h.svc.SendViaAccountNative(ctx, chainID, fromAddr, toAddr, value)
		} else {
			res, err = h.svc.SendNative(ctx, chainID, fromAddr, toAddr, value)
		}
		h.respond(w, res, err)
		return
	}

	// erc20
	token := strings.TrimSpace(body.TokenAddress)
	if !common.IsHexAddress(token) {
		httpx.WriteValidationError(w, "valid 'tokenAddress' required")
		return
	}
	amount, perr := parseBig(body.Amount, true)
	if perr != "" {
		httpx.WriteValidationError(w, perr)
		return
	}
	tokenAddr := common.HexToAddress(token)
	var res Result
	if source == "account" {
		res, err = h.svc.SendViaAccountErc20(ctx, chainID, fromAddr, tokenAddr, toAddr, amount)
	} else {
		res, err = h.svc.SendErc20(ctx, chainID, fromAddr, tokenAddr, toAddr, amount)
	}
	h.respond(w, res, err)
}

func (h *Handler) respond(w http.ResponseWriter, res Result, err error) {
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// parseBig 는 valueWei/amount(string hex·decimal | number)를 *big.Int로 파싱한다.
// positive=true면 0 불가(amount), false면 0 허용(valueWei).
func parseBig(v any, positive bool) (*big.Int, string) {
	if v == nil {
		return nil, "value required"
	}
	n, err := evm.ToBigInt(v)
	if err != nil {
		return nil, "invalid amount: " + err.Error()
	}
	if n.Sign() < 0 {
		return nil, "amount must be >= 0"
	}
	if positive && n.Sign() == 0 {
		return nil, "amount must be positive"
	}
	return n, ""
}
