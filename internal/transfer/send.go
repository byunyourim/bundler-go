package transfer

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/evm"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
	"github.com/byunyourim/stablecoin-bundler/internal/userop"
)

// Service 는 전송 로직. userop.Batcher를 공유해 sendViaAccount UserOp를 제출한다.
type Service struct {
	deps    *core.Deps
	batcher *userop.Batcher
}

// NewService 생성.
func NewService(deps *core.Deps, batcher *userop.Batcher) *Service {
	return &Service{deps: deps, batcher: batcher}
}

// Result 는 전송 응답.
type Result struct {
	TxHash string `json:"txHash"`
	From   string `json:"from"`
}

// resolveSourceWallet 은 from이 server_key1/server_key2 EOA 중 하나와 일치하는지 확인하고 그 계정을 반환한다.
func (s *Service) resolveSourceWallet(ctx context.Context, from common.Address, method string) (*signer.Account, error) {
	s1, err := s.deps.Signers.ServerKey1(ctx)
	if err != nil {
		return nil, err
	}
	s2, err := s.deps.Signers.ServerKey2(ctx)
	if err != nil {
		return nil, err
	}
	if from == s1.Address() {
		return s1, nil
	}
	if from == s2.Address() {
		return s2, nil
	}
	return nil, fmt.Errorf("%s: from must match server_key1 or server_key2 EOA", method)
}

// signAndSend 는 account 키로 서명한 트랜잭션을 브로드캐스트하고 해시를 반환한다(채굴 대기 없음).
func (s *Service) signAndSend(ctx context.Context, client *ethclient.Client, chainID int64, acc *signer.Account, to common.Address, value *big.Int, data []byte, gasLimit uint64, fees evm.Fees) (string, error) {
	nonce, err := client.PendingNonceAt(ctx, acc.Address())
	if err != nil {
		return "", err
	}
	if value == nil {
		value = big.NewInt(0)
	}
	return evm.SignAndSend(ctx, client, chainID, acc.PrivateKey(), nonce, to, value, data, gasLimit, fees)
}

// SendNative 는 source=wallet 네이티브 전송(server_key1/2 직접 서명).
func (s *Service) SendNative(ctx context.Context, chainID int64, from, to common.Address, value *big.Int) (Result, error) {
	acc, err := s.resolveSourceWallet(ctx, from, "sendNative")
	if err != nil {
		return Result{}, err
	}
	client, err := s.deps.Clients.Client(ctx, chainID)
	if err != nil {
		return Result{}, err
	}

	var hash string
	err = s.deps.Locker.WithNonceLock(ctx, core.NonceLockKey(chainID, acc.Address().Hex()), func() error {
		fees, err := evm.FeeFromProvider(ctx, client)
		if err != nil {
			return err
		}
		gasLimit := evm.EstimateGas(ctx, client, ethereum.CallMsg{From: acc.Address(), To: &to, Value: value}, 21000, 2_000_000, 200_000)
		h, err := s.signAndSend(ctx, client, chainID, acc, to, value, nil, gasLimit, fees)
		hash = h
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return Result{TxHash: hash, From: acc.Address().Hex()}, nil
}

// SendErc20 는 source=wallet ERC20 전송(server_key1/2 직접 서명).
func (s *Service) SendErc20(ctx context.Context, chainID int64, from, token, to common.Address, amount *big.Int) (Result, error) {
	acc, err := s.resolveSourceWallet(ctx, from, "sendErc20")
	if err != nil {
		return Result{}, err
	}
	client, err := s.deps.Clients.Client(ctx, chainID)
	if err != nil {
		return Result{}, err
	}
	initABIs()
	data, err := erc20ABI.Pack("transfer", to, amount)
	if err != nil {
		return Result{}, err
	}

	var hash string
	err = s.deps.Locker.WithNonceLock(ctx, core.NonceLockKey(chainID, acc.Address().Hex()), func() error {
		fees, err := evm.FeeFromProvider(ctx, client)
		if err != nil {
			return err
		}
		gasLimit := evm.EstimateGas(ctx, client, ethereum.CallMsg{From: acc.Address(), To: &token, Data: data}, 55_000, 2_000_000, 250_000)
		h, err := s.signAndSend(ctx, client, chainID, acc, token, big.NewInt(0), data, gasLimit, fees)
		hash = h
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return Result{TxHash: hash, From: acc.Address().Hex()}, nil
}

// SendViaAccountNative 는 Account에서 네이티브 출금(2-of-3 서명 → handleOps).
func (s *Service) SendViaAccountNative(ctx context.Context, chainID int64, from, to common.Address, value *big.Int) (Result, error) {
	return s.sendViaAccount(ctx, chainID, from, to, value, []byte{})
}

// SendViaAccountErc20 는 Account에서 ERC20 출금(2-of-3 서명 → handleOps).
func (s *Service) SendViaAccountErc20(ctx context.Context, chainID int64, from, token, to common.Address, amount *big.Int) (Result, error) {
	initABIs()
	data, err := erc20ABI.Pack("transfer", to, amount)
	if err != nil {
		return Result{}, err
	}
	return s.sendViaAccount(ctx, chainID, from, token, big.NewInt(0), data)
}

// sendViaAccount 는 Account.executeWithSignatures를 UserOp로 조립해 배치 큐에 제출한다.
//
// account nonce 직렬화: 멀티프로세스 안전을 위해 account 락 안에서 온체인 nonce를 읽고
// 제출(채굴 대기)까지 락을 유지한다 — 같은 Account의 연속 출금이 같은 nonce를 쓰지 않도록.
func (s *Service) sendViaAccount(ctx context.Context, chainID int64, from, to common.Address, value *big.Int, data []byte) (Result, error) {
	client, err := s.deps.Clients.Client(ctx, chainID)
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
	initABIs()

	lockKey := fmt.Sprintf("nonce:%d:account:%s", chainID, from.Hex())
	var hash string
	err = s.deps.Locker.WithNonceLock(ctx, lockKey, func() error {
		// 온체인 account nonce 조회.
		bc := bind.NewBoundContract(from, accountABI, client, client, client)
		var out []any
		if err := bc.Call(&bind.CallOpts{Context: ctx}, &out, "nonce"); err != nil {
			return err
		}
		nonce, ok := out[0].(*big.Int)
		if !ok {
			return fmt.Errorf("account nonce: unexpected type %T", out[0])
		}

		maxFee, maxPri, err := userOpMaxFeeFields(ctx, client, chainID)
		if err != nil {
			return err
		}

		digest, err := getDigest(from, nonce, to, value, data)
		if err != nil {
			return err
		}
		sig1, err := sign65(digest, s1.PrivateKey())
		if err != nil {
			return err
		}
		sig2, err := sign65(digest, s2.PrivateKey())
		if err != nil {
			return err
		}
		callData, err := accountABI.Pack("executeWithSignatures", to, value, data, [][]byte{sig1, sig2})
		if err != nil {
			return err
		}

		packed := userop.PackedUserOp{
			Sender:               from,
			Nonce:                nonce,
			InitCode:             []byte{},
			CallData:             callData,
			CallGasLimit:         big.NewInt(1_200_000),
			VerificationGasLimit: big.NewInt(400_000),
			PreVerificationGas:   big.NewInt(100_000),
			MaxFeePerGas:         maxFee,
			MaxPriorityFeePerGas: maxPri,
			PaymasterAndData:     []byte{},
			Signature:            []byte{},
		}
		h, err := s.batcher.Enqueue(ctx, chainID, packed)
		hash = h
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return Result{TxHash: hash, From: from.Hex()}, nil
}
