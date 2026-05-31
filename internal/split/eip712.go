package split

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// payerSplitMessage 는 EIP-712 PayerSplit 서명 입력.
type payerSplitMessage struct {
	Payer       common.Address
	ERC20Token  common.Address
	Recipient0  common.Address
	Recipient1  common.Address
	NativeAmt0  *big.Int
	NativeAmt1  *big.Int
	ERC20Amt0   *big.Int
	ERC20Amt1   *big.Int
	SignerEpoch *big.Int
	Nonce       *big.Int
}

// typedDataHash 는 EoaFundedSplitMultisig PayerSplit의 EIP-712 최종 해시(32바이트)를 만든다.
// (TS ethers.TypedDataEncoder.hash(domain, PAYER_SPLIT_TYPES, message) 대응)
func typedDataHash(chainID int64, router common.Address, m payerSplitMessage) ([]byte, error) {
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"PayerSplit": {
				{Name: "payer", Type: "address"},
				{Name: "erc20Token", Type: "address"},
				{Name: "recipient0", Type: "address"},
				{Name: "recipient1", Type: "address"},
				{Name: "nativeAmount0", Type: "uint256"},
				{Name: "nativeAmount1", Type: "uint256"},
				{Name: "erc20Amount0", Type: "uint256"},
				{Name: "erc20Amount1", Type: "uint256"},
				{Name: "signerEpoch", Type: "uint256"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "PayerSplit",
		Domain: apitypes.TypedDataDomain{
			Name:              "EoaFundedSplitMultisig",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: router.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"payer":         m.Payer.Hex(),
			"erc20Token":    m.ERC20Token.Hex(),
			"recipient0":    m.Recipient0.Hex(),
			"recipient1":    m.Recipient1.Hex(),
			"nativeAmount0": m.NativeAmt0.String(),
			"nativeAmount1": m.NativeAmt1.String(),
			"erc20Amount0":  m.ERC20Amt0.String(),
			"erc20Amount1":  m.ERC20Amt1.String(),
			"signerEpoch":   m.SignerEpoch.String(),
			"nonce":         m.Nonce.String(),
		},
	}

	hash, _, err := apitypes.TypedDataAndHash(td)
	return hash, err
}
