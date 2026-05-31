package transfer

import (
	"context"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/byunyourim/stablecoin-bundler/internal/evm"
)

// --- UserOp maxFee 결정 (Account ERC20/native). EOA 가스는 evm.FeeFromProvider 공용. ---

// userOpMinMaxFeeWei 는 Sepolia/Fuji/KCP RPC 저fee 보정 하한(prefund 부족 방지).
var userOpMinMaxFeeWei = map[int64]*big.Int{
	11155111: evm.Gwei(500),
	43113:    evm.Gwei(500),
	56357:    evm.Gwei(500),
}
var userOpMinPriorityFeeWei = map[int64]*big.Int{
	11155111: evm.Gwei(50),
	43113:    evm.Gwei(50),
	56357:    evm.Gwei(50),
}

func parseUserOpGweiEnv(name string) *big.Int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return nil
	}
	wei := new(big.Int)
	big.NewFloat(f * 1e9).Int(wei)
	if wei.Sign() <= 0 {
		wei = big.NewInt(1)
	}
	return wei
}

func applySepoliaFujiKcpFloor(chainID int64, maxF, maxP *big.Int) (*big.Int, *big.Int) {
	minMax, ok := userOpMinMaxFeeWei[chainID]
	minPri := userOpMinPriorityFeeWei[chainID]
	if !ok {
		return maxF, maxP
	}
	if maxF.Cmp(minMax) < 0 {
		maxF = new(big.Int).Set(minMax)
	}
	if maxP.Cmp(minPri) < 0 {
		maxP = new(big.Int).Set(minPri)
	}
	if maxP.Cmp(maxF) > 0 {
		half := new(big.Int).Div(maxF, big.NewInt(2))
		if half.Sign() > 0 {
			maxP = half
		} else {
			maxP = new(big.Int).Set(minPri)
		}
	}
	return maxF, maxP
}

// userOpMaxFeeFields 는 UserOp maxFee/maxPriority(wei)를 결정한다.
// 우선순위: env(USEROP_*_<chainId> → USEROP_*) → RPC(+ Sepolia/Fuji/KCP 하한) → 보수 fallback.
// (TS getUserOpMaxFeeFieldsHex 대응)
func userOpMaxFeeFields(ctx context.Context, client *ethclient.Client, chainID int64) (*big.Int, *big.Int, error) {
	cid := strconv.FormatInt(chainID, 10)
	envMax := firstBig(parseUserOpGweiEnv("USEROP_MAX_FEE_PER_GAS_GWEI_"+cid), parseUserOpGweiEnv("USEROP_MAX_FEE_PER_GAS_GWEI"))
	envPri := firstBig(parseUserOpGweiEnv("USEROP_MAX_PRIORITY_FEE_PER_GAS_GWEI_"+cid), parseUserOpGweiEnv("USEROP_MAX_PRIORITY_FEE_PER_GAS_GWEI"))
	if envMax != nil && envPri != nil {
		if envPri.Cmp(envMax) > 0 {
			return nil, nil, errPriorityGtMax
		}
		return envMax, envPri, nil
	}

	o, err := evm.FeeFromProvider(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	if o.MaxFee != nil && o.MaxPriority != nil && o.MaxFee.Sign() > 0 && o.MaxPriority.Sign() > 0 {
		maxF, maxP := o.MaxFee, o.MaxPriority
		if maxP.Cmp(maxF) > 0 {
			maxP = new(big.Int).Div(maxF, big.NewInt(2))
		}
		maxF, maxP = applySepoliaFujiKcpFloor(chainID, maxF, maxP)
		return maxF, maxP, nil
	}
	if o.GasPrice != nil && o.GasPrice.Sign() > 0 {
		maxF := new(big.Int).Mul(o.GasPrice, big.NewInt(2))
		maxP := new(big.Int).Set(o.GasPrice)
		maxF, maxP = applySepoliaFujiKcpFloor(chainID, maxF, maxP)
		return maxF, maxP, nil
	}

	if _, isTriple := userOpMinMaxFeeWei[chainID]; isTriple {
		return evm.Gwei(2000), evm.Gwei(100), nil
	}
	return evm.Gwei(150), evm.Gwei(5), nil
}

func firstBig(vals ...*big.Int) *big.Int {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
