// Package deploy 는 CREATE2 지갑 배포 도메인이다. (TS의 lib/create2-factory + api/create2/deploy)
// POST /api/create2/deploy — 디플로이어 nonce는 redis 분산락으로 직렬화.
//
// 멀티번들러 주의: 배포 주소는 deployer에 의존하므로 라운드로빈하지 않고 PrimaryBundler(고정)를 쓴다.
package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/httpx"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

const factoryABIJSON = `[
  {"type":"function","name":"deploy","stateMutability":"payable","inputs":[
    {"name":"bytecode","type":"bytes"},{"name":"salt","type":"bytes32"}],"outputs":[{"name":"addr","type":"address"}]},
  {"type":"function","name":"computeAddress","stateMutability":"view","inputs":[
    {"name":"deployer","type":"address"},{"name":"salt","type":"bytes32"},{"name":"bytecodeHash","type":"bytes32"}],
    "outputs":[{"type":"address"}]},
  {"type":"event","name":"Deployed","anonymous":false,"inputs":[
    {"name":"addr","type":"address","indexed":true},
    {"name":"deployer","type":"address","indexed":true},
    {"name":"salt","type":"bytes32","indexed":true},
    {"name":"actualSalt","type":"bytes32","indexed":false}]}
]`

var (
	factoryABIOnce sync.Once
	factoryABI     abi.ABI
)

func loadFactoryABI() abi.ABI {
	factoryABIOnce.Do(func() {
		parsed, err := abi.JSON(strings.NewReader(factoryABIJSON))
		if err != nil {
			panic("deploy: factory ABI: " + err.Error())
		}
		factoryABI = parsed
	})
	return factoryABI
}

// Handler 는 배포 HTTP 핸들러.
type Handler struct {
	deps *core.Deps
}

// NewHandler 핸들러 생성.
func NewHandler(deps *core.Deps) *Handler {
	return &Handler{deps: deps}
}

type deployBody struct {
	ChainID          int64  `json:"chainId"`
	ProxyBytecodeHex string `json:"proxyBytecodeHex"`
	Salt             string `json:"salt"`
	FactoryAddress   string `json:"factoryAddress"`
}

type deployResponse struct {
	DeployedAddress  string `json:"deployedAddress"`
	TxHash           string `json:"txHash"`
	PredictedAddress string `json:"predictedAddress"`
	Match            bool   `json:"match"`
}

// Deploy POST /api/create2/deploy.
func (h *Handler) Deploy(w http.ResponseWriter, r *http.Request) {
	var body deployBody
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		httpx.WriteValidationError(w, "invalid JSON body")
		return
	}

	chainID, err := h.deps.Reg.ResolveChainID(body.ChainID)
	if err != nil {
		httpx.WriteValidationError(w, "chainId required")
		return
	}
	bytecode, err := evm.ParseHexBytes(strings.TrimSpace(body.ProxyBytecodeHex))
	if err != nil || len(bytecode) == 0 {
		httpx.WriteValidationError(w, "proxyBytecodeHex required")
		return
	}
	salt := body.Salt
	if strings.TrimSpace(salt) == "" {
		salt = "account-proxy-1"
	}
	factoryAddr := strings.TrimSpace(body.FactoryAddress)
	if factoryAddr == "" {
		factoryAddr = h.deps.Reg.Create2Factory(chainID)
	}
	if !common.IsHexAddress(factoryAddr) {
		httpx.WriteValidationError(w, "factoryAddress required (or set CREATE2_FACTORY_ADDRESS)")
		return
	}

	ctx := r.Context()
	deployer, err := h.deps.Signers.PrimaryBundler(ctx, chainID)
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}

	saltBytes := evm.SaltToBytes32(salt)
	bytecodeHash := evm.Keccak256(bytecode)
	factory := common.HexToAddress(factoryAddr)

	// 배포 전 주소 예측 (factory.computeAddress).
	predicted, err := h.computeAddress(ctx, chainID, factory, deployer.Address(), saltBytes, bytecodeHash)
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}

	deployed, txHash, err := h.deploy(ctx, chainID, deployer, factory, bytecode, saltBytes)
	if err != nil {
		httpx.WriteTxError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, deployResponse{
		DeployedAddress:  deployed.Hex(),
		TxHash:           txHash,
		PredictedAddress: predicted.Hex(),
		Match:            strings.EqualFold(predicted.Hex(), deployed.Hex()),
	})
}

func (h *Handler) computeAddress(ctx context.Context, chainID int64, factory, deployer common.Address, salt, bytecodeHash [32]byte) (common.Address, error) {
	client, err := h.deps.Clients.Client(ctx, chainID)
	if err != nil {
		return common.Address{}, err
	}
	bc := bind.NewBoundContract(factory, loadFactoryABI(), client, client, client)
	var out []any
	if err := bc.Call(&bind.CallOpts{Context: ctx}, &out, "computeAddress", deployer, salt, bytecodeHash); err != nil {
		return common.Address{}, err
	}
	addr, ok := out[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("computeAddress: unexpected type %T", out[0])
	}
	return addr, nil
}

// deploy 는 factory.deploy를 보내고 Deployed 이벤트에서 배포 주소를 추출한다.
func (h *Handler) deploy(ctx context.Context, chainID int64, deployer *signer.Account, factory common.Address, bytecode []byte, salt [32]byte) (common.Address, string, error) {
	tx, receipt, err := h.deps.SendManagedTx(ctx, chainID, deployer, factory, loadFactoryABI(), nil, "deploy", bytecode, salt)
	if err != nil {
		txHash := ""
		if tx != nil {
			txHash = tx.Hash().Hex()
		}
		return common.Address{}, txHash, err
	}

	deployedEventID := loadFactoryABI().Events["Deployed"].ID
	var deployed common.Address
	for _, lg := range receipt.Logs {
		if len(lg.Topics) >= 2 && lg.Topics[0] == deployedEventID {
			deployed = common.BytesToAddress(lg.Topics[1].Bytes())
			break
		}
	}
	return deployed, tx.Hash().Hex(), nil
}
