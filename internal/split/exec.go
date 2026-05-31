package split

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

const routerABIJSON = `[
  {"type":"function","name":"executeWithSignatures","stateMutability":"payable","inputs":[
    {"name":"payer","type":"address"},{"name":"erc20Token","type":"address"},
    {"name":"recipient0","type":"address"},{"name":"recipient1","type":"address"},
    {"name":"nativeAmount0","type":"uint256"},{"name":"nativeAmount1","type":"uint256"},
    {"name":"erc20Amount0","type":"uint256"},{"name":"erc20Amount1","type":"uint256"},
    {"name":"nonce","type":"uint256"},{"name":"sig0","type":"bytes"},{"name":"sig1","type":"bytes"},
    {"name":"permitDeadline","type":"uint256"},{"name":"permitAmount","type":"uint256"},
    {"name":"permitV","type":"uint8"},{"name":"permitR","type":"bytes32"},{"name":"permitS","type":"bytes32"}],
    "outputs":[]},
  {"type":"function","name":"payerNonce","stateMutability":"view","inputs":[{"name":"","type":"address"}],"outputs":[{"type":"uint256"}]},
  {"type":"function","name":"signerEpoch","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]},
  {"type":"function","name":"hashTypedDataPayerSplit","stateMutability":"view","inputs":[
    {"name":"payer","type":"address"},{"name":"erc20Token","type":"address"},
    {"name":"recipient0","type":"address"},{"name":"recipient1","type":"address"},
    {"name":"nativeAmount0","type":"uint256"},{"name":"nativeAmount1","type":"uint256"},
    {"name":"erc20Amount0","type":"uint256"},{"name":"erc20Amount1","type":"uint256"},
    {"name":"nonce","type":"uint256"}],"outputs":[{"type":"bytes32"}]}
]`

const erc20AllowanceABIJSON = `[
  {"type":"function","name":"allowance","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"type":"uint256"}]},
  {"type":"function","name":"approve","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"type":"bool"}]}
]`

var (
	abiOnce     sync.Once
	routerABI   abi.ABI
	erc20ABI    abi.ABI
	zeroBytes32 [32]byte
)

func initABIs() {
	abiOnce.Do(func() {
		var err error
		if routerABI, err = abi.JSON(strings.NewReader(routerABIJSON)); err != nil {
			panic("split: router ABI: " + err.Error())
		}
		if erc20ABI, err = abi.JSON(strings.NewReader(erc20AllowanceABIJSON)); err != nil {
			panic("split: erc20 ABI: " + err.Error())
		}
	})
}

// Params 는 executeWithSignatures 입력.
type Params struct {
	ChainID        int64
	ERC20Token     common.Address
	Recipient0     common.Address
	Recipient1     common.Address
	NativeAmount0  *big.Int
	NativeAmount1  *big.Int
	ERC20Amount0   *big.Int
	ERC20Amount1   *big.Int
	PayerKey       string // "server_key1"(기본) | "server_key2"
	PermitDeadline *big.Int
	PermitAmount   *big.Int
	PermitV        uint8
	PermitR        [32]byte
	PermitS        [32]byte
}

// Result 는 응답.
type Result struct {
	TxHash        string `json:"txHash"`
	From          string `json:"from"`
	Payer         string `json:"payer"`
	RouterAddress string `json:"routerAddress"`
	NonceUsed     string `json:"nonceUsed"`
	ApproveTxHash string `json:"approveTxHash,omitempty"`
}

// Service 는 EOA-funded-split 실행 로직.
type Service struct {
	deps *core.Deps
}

// NewService 생성.
func NewService(deps *core.Deps) *Service {
	return &Service{deps: deps}
}

func isNonceErr(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "efs024") || strings.Contains(m, "nonce")
}

// Execute 는 EIP-712 2인 서명 후 relayer(payer) EOA로 executeWithSignatures를 보낸다.
// (TS executeEoaFundedSplitWithServerSignatures 대응)
func (s *Service) Execute(ctx context.Context, p Params) (Result, error) {
	initABIs()
	routerStr, err := s.deps.Reg.EOFSProxy(p.ChainID)
	if err != nil {
		return Result{}, err
	}
	if !common.IsHexAddress(routerStr) {
		return Result{}, fmt.Errorf("invalid EOFS proxy address")
	}
	router := common.HexToAddress(routerStr)

	if p.ERC20Token == (common.Address{}) {
		if p.ERC20Amount0.Sign() != 0 || p.ERC20Amount1.Sign() != 0 {
			return Result{}, fmt.Errorf("erc20Token is zero address: erc20 amounts must be 0")
		}
	}
	total := new(big.Int).Add(new(big.Int).Add(p.NativeAmount0, p.NativeAmount1), new(big.Int).Add(p.ERC20Amount0, p.ERC20Amount1))
	if total.Sign() <= 0 {
		return Result{}, fmt.Errorf("sum of amounts must be positive")
	}

	relayer, err := s.relayer(ctx, p.PayerKey)
	if err != nil {
		return Result{}, err
	}
	s1, err := s.deps.Signers.ServerKey1(ctx)
	if err != nil {
		return Result{}, err
	}
	s2, err := s.deps.Signers.ServerKey2(ctx)
	if err != nil {
		return Result{}, err
	}
	payer := relayer.Address()

	if err := validatePermit(&p); err != nil {
		return Result{}, err
	}

	client, err := s.deps.Clients.Client(ctx, p.ChainID)
	if err != nil {
		return Result{}, err
	}

	var result Result
	lockKey := core.NonceLockKey(p.ChainID, payer.Hex())
	err = s.deps.Locker.WithNonceLock(ctx, lockKey, func() error {
		routerC := bind.NewBoundContract(router, routerABI, client, client, client)

		epoch, err := callBig(ctx, routerC, "signerEpoch")
		if err != nil {
			return err
		}
		payerNonce, err := callBig(ctx, routerC, "payerNonce", payer)
		if err != nil {
			return err
		}
		eoaNonce, err := client.PendingNonceAt(ctx, payer)
		if err != nil {
			return err
		}

		// permit 미사용 + ERC20 출금 시 allowance 보장.
		erc20Needed := new(big.Int).Add(p.ERC20Amount0, p.ERC20Amount1)
		if p.ERC20Token != (common.Address{}) && erc20Needed.Sign() > 0 && p.PermitDeadline.Sign() == 0 {
			approveHash, nextNonce, err := s.ensureAllowance(ctx, client, p.ChainID, relayer, p.ERC20Token, router, payer, erc20Needed, eoaNonce)
			if err != nil {
				return err
			}
			result.ApproveTxHash = approveHash
			eoaNonce = nextNonce
		}

		send := func(nonce *big.Int) (string, error) {
			return s.buildAndSend(ctx, client, p, router, payer, relayer, s1, s2, epoch, nonce, eoaNonce)
		}

		txHash, err := send(payerNonce)
		nonceUsed := payerNonce
		if err != nil {
			if !isNonceErr(err) {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			fresh, ferr := callBig(ctx, routerC, "payerNonce", payer)
			if ferr != nil {
				return ferr
			}
			eoaNonce++ // 이전 시도가 EOA nonce를 소비했을 수 있어 다음 슬롯 사용
			txHash, err = send(fresh)
			if err != nil {
				return err
			}
			nonceUsed = fresh
		}

		result.TxHash = txHash
		result.From = payer.Hex()
		result.Payer = payer.Hex()
		result.RouterAddress = router.Hex()
		result.NonceUsed = nonceUsed.String()
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) relayer(ctx context.Context, payerKey string) (*signer.Account, error) {
	if strings.EqualFold(strings.TrimSpace(payerKey), "server_key2") {
		return s.deps.Signers.ServerKey2(ctx)
	}
	return s.deps.Signers.ServerKey1(ctx)
}

// buildAndSend 는 EIP-712 해시 검증·2인 서명 후 executeWithSignatures를 eoaNonce로 보낸다.
func (s *Service) buildAndSend(ctx context.Context, ethClient *ethclient.Client, p Params, router, payer common.Address, relayer, s1, s2 *signer.Account, epoch, nonce *big.Int, eoaNonce uint64) (string, error) {
	msg := payerSplitMessage{
		Payer: payer, ERC20Token: p.ERC20Token, Recipient0: p.Recipient0, Recipient1: p.Recipient1,
		NativeAmt0: p.NativeAmount0, NativeAmt1: p.NativeAmount1, ERC20Amt0: p.ERC20Amount0, ERC20Amt1: p.ERC20Amount1,
		SignerEpoch: epoch, Nonce: nonce,
	}
	localHash, err := typedDataHash(p.ChainID, router, msg)
	if err != nil {
		return "", err
	}

	// 온체인 hashTypedDataPayerSplit와 대조(도메인/epoch/주소 불일치 조기 검출).
	routerC := bind.NewBoundContract(router, routerABI, ethClient, ethClient, ethClient)
	var out []any
	if err := routerC.Call(&bind.CallOpts{Context: ctx}, &out, "hashTypedDataPayerSplit",
		payer, p.ERC20Token, p.Recipient0, p.Recipient1,
		p.NativeAmount0, p.NativeAmount1, p.ERC20Amount0, p.ERC20Amount1, nonce); err != nil {
		return "", err
	}
	onChain, ok := out[0].([32]byte)
	if !ok {
		return "", fmt.Errorf("hashTypedDataPayerSplit: unexpected type %T", out[0])
	}
	if !bytes.Equal(localHash, onChain[:]) {
		return "", fmt.Errorf("EIP-712 digest mismatch (check chainId / router / signerEpoch)")
	}

	sig0, err := evm.Sign65(localHash, s1.PrivateKey())
	if err != nil {
		return "", err
	}
	sig1, err := evm.Sign65(localHash, s2.PrivateKey())
	if err != nil {
		return "", err
	}

	calldata, err := routerABI.Pack("executeWithSignatures",
		payer, p.ERC20Token, p.Recipient0, p.Recipient1,
		p.NativeAmount0, p.NativeAmount1, p.ERC20Amount0, p.ERC20Amount1,
		nonce, sig0, sig1,
		p.PermitDeadline, p.PermitAmount, p.PermitV, p.PermitR, p.PermitS)
	if err != nil {
		return "", err
	}

	nativeTotal := new(big.Int).Add(p.NativeAmount0, p.NativeAmount1)
	fees, err := evm.FeeFromProvider(ctx, ethClient)
	if err != nil {
		return "", err
	}
	gasLimit := evm.EstimateGas(ctx, ethClient, ethereum.CallMsg{
		From: payer, To: &router, Value: nativeTotal, Data: calldata,
	}, 180_000, 3_000_000, 300_000)

	return evm.SignAndSend(ctx, ethClient, p.ChainID, relayer.PrivateKey(), eoaNonce, router, nativeTotal, calldata, gasLimit, fees)
}

// ensureAllowance 는 allowance가 부족하면 approve를 전송(채굴 대기)하고 다음 EOA nonce를 반환한다.
func (s *Service) ensureAllowance(ctx context.Context, ethClient *ethclient.Client, chainID int64, relayer *signer.Account, token, router, owner common.Address, needed *big.Int, eoaNonce uint64) (string, uint64, error) {
	tokenC := bind.NewBoundContract(token, erc20ABI, ethClient, ethClient, ethClient)

	var out []any
	if err := tokenC.Call(&bind.CallOpts{Context: ctx}, &out, "allowance", owner, router); err != nil {
		return "", eoaNonce, err
	}
	cur, _ := out[0].(*big.Int)
	if cur != nil && cur.Cmp(needed) >= 0 {
		return "", eoaNonce, nil // 충분
	}

	approve := func(amount *big.Int, nonce uint64) (string, error) {
		data, err := erc20ABI.Pack("approve", router, amount)
		if err != nil {
			return "", err
		}
		fees, err := evm.FeeFromProvider(ctx, ethClient)
		if err != nil {
			return "", err
		}
		gasLimit := evm.EstimateGas(ctx, ethClient, ethereum.CallMsg{From: owner, To: &token, Data: data}, 45_000, 200_000, 120_000)
		hash, err := evm.SignAndSend(ctx, ethClient, chainID, relayer.PrivateKey(), nonce, token, big.NewInt(0), data, gasLimit, fees)
		if err != nil {
			return hash, err
		}
		_, err = evm.WaitReceipt(ctx, ethClient, common.HexToHash(hash), 60*time.Second)
		return hash, err
	}

	// approve(needed) 시도, 실패 시 approve(0) 리셋 후 재시도(USDT류 대응).
	hash, err := approve(needed, eoaNonce)
	if err == nil {
		return hash, eoaNonce + 1, nil
	}
	if _, e := approve(big.NewInt(0), eoaNonce); e != nil {
		return "", eoaNonce, fmt.Errorf("ERC20 approve(0) reset failed: %w", e)
	}
	hash, err = approve(needed, eoaNonce+1)
	if err != nil {
		return "", eoaNonce, err
	}
	return hash, eoaNonce + 2, nil
}

// callBig 는 view 메서드를 호출해 *big.Int를 반환한다.
func callBig(ctx context.Context, c *bind.BoundContract, method string, args ...any) (*big.Int, error) {
	var out []any
	if err := c.Call(&bind.CallOpts{Context: ctx}, &out, method, args...); err != nil {
		return nil, err
	}
	n, ok := out[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected type %T", method, out[0])
	}
	return n, nil
}

func validatePermit(p *Params) error {
	if p.PermitDeadline == nil {
		p.PermitDeadline = big.NewInt(0)
	}
	if p.PermitAmount == nil {
		p.PermitAmount = big.NewInt(0)
	}
	if p.PermitDeadline.Sign() == 0 {
		return nil
	}
	if p.ERC20Token == (common.Address{}) {
		return fmt.Errorf("permit requires non-zero erc20Token")
	}
	erc20Total := new(big.Int).Add(p.ERC20Amount0, p.ERC20Amount1)
	if erc20Total.Sign() <= 0 {
		return fmt.Errorf("permit requires positive erc20 amounts")
	}
	if p.PermitAmount.Cmp(erc20Total) < 0 {
		return fmt.Errorf("permitAmount must be >= erc20Amount0 + erc20Amount1")
	}
	if p.PermitR == zeroBytes32 || p.PermitS == zeroBytes32 {
		return fmt.Errorf("permitDeadline set: permitR and permitS (bytes32 hex) required")
	}
	return nil
}
