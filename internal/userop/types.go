package userop

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/byunyourim/stablecoin-bundler/internal/evm"
)

// UserOperation 은 JSON-RPC params[0] UserOp (TS types.ts 대응).
// 숫자 필드는 hex/decimal 문자열·number 모두 허용.
type UserOperation struct {
	Sender               string      `json:"sender"`
	Nonce                evm.Uint256 `json:"nonce"`
	InitCode             string      `json:"initCode"`
	CallData             string      `json:"callData"`
	CallGasLimit         evm.Uint256 `json:"callGasLimit"`
	VerificationGasLimit evm.Uint256 `json:"verificationGasLimit"`
	PreVerificationGas   evm.Uint256 `json:"preVerificationGas"`
	MaxFeePerGas         evm.Uint256 `json:"maxFeePerGas"`
	MaxPriorityFeePerGas evm.Uint256 `json:"maxPriorityFeePerGas"`
	PaymasterAndData     string      `json:"paymasterAndData"`
	Signature            string      `json:"signature"`
}

// toPacked 는 ABI 패킹용 PackedUserOp로 변환한다.
// EntryPoint만 사용하므로 paymasterAndData는 항상 "0x"로 강제(TS normalizeUserOp 대응).
func (u *UserOperation) toPacked() (PackedUserOp, error) {
	if !common.IsHexAddress(u.Sender) {
		return PackedUserOp{}, fmt.Errorf("invalid sender address")
	}
	initCode, err := evm.ParseHexBytes(u.InitCode)
	if err != nil {
		return PackedUserOp{}, fmt.Errorf("initCode: %w", err)
	}
	callData, err := evm.ParseHexBytes(u.CallData)
	if err != nil {
		return PackedUserOp{}, fmt.Errorf("callData: %w", err)
	}
	sig, err := evm.ParseHexBytes(u.Signature)
	if err != nil {
		return PackedUserOp{}, fmt.Errorf("signature: %w", err)
	}
	return PackedUserOp{
		Sender:               common.HexToAddress(u.Sender),
		Nonce:                u.Nonce.Big(),
		InitCode:             initCode,
		CallData:             callData,
		CallGasLimit:         u.CallGasLimit.Big(),
		VerificationGasLimit: u.VerificationGasLimit.Big(),
		PreVerificationGas:   u.PreVerificationGas.Big(),
		MaxFeePerGas:         u.MaxFeePerGas.Big(),
		MaxPriorityFeePerGas: u.MaxPriorityFeePerGas.Big(),
		PaymasterAndData:     []byte{}, // EntryPoint 전용 — Paymaster 미사용
		Signature:            sig,
	}, nil
}
