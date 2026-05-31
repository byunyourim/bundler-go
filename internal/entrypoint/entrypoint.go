// Package entrypoint 는 EntryPoint 가스풀 deposit/withdraw 도메인이다.
// (TS의 lib/entrypoint + api/entrypoint/transfer) POST /api/entrypoint/transfer.
//
// 서명은 PrimaryBundler(= TS bundler owner key). nonce는 redis 분산락으로 직렬화.
package entrypoint

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/httpx"
	"github.com/byunyourim/stablecoin-bundler/internal/userop"
)

var kst = time.FixedZone("KST", 9*60*60)

// Handler 는 가스풀 HTTP 핸들러.
type Handler struct {
	deps *core.Deps
}

// NewHandler 핸들러 생성.
func NewHandler(deps *core.Deps) *Handler {
	return &Handler{deps: deps}
}

type body struct {
	ChainID           int64  `json:"chainId"`
	Action            string `json:"action"`
	EntrypointAddress string `json:"entrypointAddress"`
	Amount            string `json:"amount"`
	ToAddress         string `json:"toAddress"`
}

type response struct {
	Status           string `json:"status"`
	TxHash           string `json:"txHash"`
	GasFee           string `json:"gasFee"`
	CompleteDatetime string `json:"completeDatetime"`
	Message          string `json:"message"`
}

// Transfer POST /api/entrypoint/transfer (DEPOSIT / WITHDRAW).
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	var b body
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
	action := strings.ToUpper(strings.TrimSpace(b.Action))
	if action != "DEPOSIT" && action != "WITHDRAW" {
		httpx.WriteValidationError(w, "action required: 'DEPOSIT' or 'WITHDRAW'")
		return
	}
	epAddr := strings.TrimSpace(b.EntrypointAddress)
	if !common.IsHexAddress(epAddr) {
		httpx.WriteValidationError(w, "valid entrypointAddress required")
		return
	}
	amount, err := evm.ToBigInt(strings.TrimSpace(b.Amount))
	if err != nil || amount.Sign() < 0 {
		httpx.WriteValidationError(w, "valid amount (wei integer string) required")
		return
	}

	ctx := r.Context()
	owner, err := h.deps.Signers.PrimaryBundler(ctx, chainID)
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}
	ep := common.HexToAddress(epAddr)
	abiEP := userop.EntryPointABI()

	var (
		tx      interface{ Hash() common.Hash }
		gasFee  string
		message string
	)

	if action == "DEPOSIT" {
		t, receipt, err := h.deps.SendManagedTx(ctx, chainID, owner, ep, abiEP, amount, "depositGlobalGasPool")
		if err != nil {
			httpx.WriteTxError(w, err)
			return
		}
		tx, gasFee = t, gasFeeOf(receipt.GasUsed, receipt.EffectiveGasPrice)
		message = "deposit completed"
	} else {
		withdrawTo := strings.TrimSpace(b.ToAddress)
		var to common.Address
		if common.IsHexAddress(withdrawTo) {
			to = common.HexToAddress(withdrawTo)
		} else {
			to = owner.Address() // 미지정 시 owner 주소로 회수
		}
		t, receipt, err := h.deps.SendManagedTx(ctx, chainID, owner, ep, abiEP, nil, "withdrawGlobalGasPool", to, amount)
		if err != nil {
			httpx.WriteTxError(w, err)
			return
		}
		tx, gasFee = t, gasFeeOf(receipt.GasUsed, receipt.EffectiveGasPrice)
		message = "withdraw completed"
	}

	httpx.WriteJSON(w, http.StatusOK, response{
		Status:           "SUCCESS",
		TxHash:           tx.Hash().Hex(),
		GasFee:           gasFee,
		CompleteDatetime: time.Now().In(kst).Format("20060102150405"),
		Message:          message,
	})
}

func gasFeeOf(gasUsed uint64, effectivePrice *big.Int) string {
	if effectivePrice == nil {
		return "0"
	}
	return new(big.Int).Mul(new(big.Int).SetUint64(gasUsed), effectivePrice).String()
}
