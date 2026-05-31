package evm

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Fees 는 EIP-1559(MaxFee/MaxPriority) 또는 legacy(GasPrice) 가스 설정.
type Fees struct {
	MaxFee      *big.Int
	MaxPriority *big.Int
	GasPrice    *big.Int
}

// Gwei 는 n gwei를 wei로 변환한다.
func Gwei(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e9)) }

func bump180(x *big.Int) *big.Int {
	return new(big.Int).Div(new(big.Int).Mul(x, big.NewInt(180)), big.NewInt(100))
}

// FeeFromProvider 는 RPC feeData + baseFee로 가스 상한을 정한다(180% 범프, baseFee 급등 완화).
// (TS getFeeOverridesFromProvider 대응)
func FeeFromProvider(ctx context.Context, client *ethclient.Client) (Fees, error) {
	minPriority := Gwei(2)

	tipCap, tipErr := client.SuggestGasTipCap(ctx)
	head, headErr := client.HeaderByNumber(ctx, nil)

	if tipErr == nil && headErr == nil && head.BaseFee != nil && head.BaseFee.Sign() > 0 && tipCap != nil && tipCap.Sign() > 0 {
		maxPriority := bump180(tipCap)
		if maxPriority.Cmp(minPriority) < 0 {
			maxPriority = minPriority
		}
		baseFee := head.BaseFee
		maxFee := bump180(new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tipCap))
		minMax := new(big.Int).Add(
			new(big.Int).Mul(new(big.Int).Div(new(big.Int).Mul(baseFee, big.NewInt(125)), big.NewInt(100)), big.NewInt(2)),
			maxPriority,
		)
		if maxFee.Cmp(minMax) < 0 {
			maxFee = new(big.Int).Div(new(big.Int).Mul(minMax, big.NewInt(110)), big.NewInt(100))
		}
		return Fees{MaxFee: maxFee, MaxPriority: maxPriority}, nil
	}

	if gp, err := client.SuggestGasPrice(ctx); err == nil && gp != nil && gp.Sign() > 0 {
		return Fees{GasPrice: bump180(gp)}, nil
	}

	return Fees{MaxFee: Gwei(150), MaxPriority: Gwei(5)}, nil
}

// BumpGasLimit 는 estimateGas +30%·소량 마진, min/max 캡. (TS bumpGasLimit 대응)
func BumpGasLimit(estimated, min, maxCap uint64) uint64 {
	bumped := estimated*130/100 + 8000
	if bumped < min {
		return min
	}
	if bumped > maxCap {
		return maxCap
	}
	return bumped
}

// EstimateGas 는 추정 실패 시 fallback을 반환한다.
func EstimateGas(ctx context.Context, client *ethclient.Client, msg ethereum.CallMsg, min, maxCap, fallback uint64) uint64 {
	est, err := client.EstimateGas(ctx, msg)
	if err != nil {
		return fallback
	}
	return BumpGasLimit(est, min, maxCap)
}

// SignAndSend 는 priv 키로 서명한 트랜잭션을 explicit nonce로 브로드캐스트하고 해시를 반환한다(채굴 대기 없음).
// nonce 관리는 호출부가 락 안에서 담당한다.
func SignAndSend(ctx context.Context, client *ethclient.Client, chainID int64, priv *ecdsa.PrivateKey, nonce uint64, to common.Address, value *big.Int, data []byte, gasLimit uint64, fees Fees) (string, error) {
	cid := big.NewInt(chainID)
	var tx *types.Transaction
	if fees.MaxFee != nil {
		tx = types.NewTx(&types.DynamicFeeTx{
			ChainID: cid, Nonce: nonce, GasTipCap: fees.MaxPriority, GasFeeCap: fees.MaxFee,
			Gas: gasLimit, To: &to, Value: value, Data: data,
		})
	} else {
		tx = types.NewTx(&types.LegacyTx{
			Nonce: nonce, GasPrice: fees.GasPrice, Gas: gasLimit, To: &to, Value: value, Data: data,
		})
	}
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(cid), priv)
	if err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return signed.Hash().Hex(), err
	}
	return signed.Hash().Hex(), nil
}

// WaitReceipt 는 txHash의 영수증을 timeout까지 폴링한다. status==0이면 revert로 error.
func WaitReceipt(ctx context.Context, client *ethclient.Client, txHash common.Hash, timeout time.Duration) (*types.Receipt, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		r, err := client.TransactionReceipt(ctx, txHash)
		if err == nil && r != nil {
			if r.Status == 0 {
				return r, errors.New("transaction reverted (status 0)")
			}
			return r, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("tx.wait timeout: " + txHash.Hex())
		case <-tick.C:
		}
	}
}
