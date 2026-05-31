package userop

import (
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// PackedUserOp 은 EntryPoint.handleOps 의 UserOperation 튜플(go-ethereum abi 패킹용).
// 필드 순서·이름은 ABI 컴포넌트와 1:1.
type PackedUserOp struct {
	Sender               common.Address
	Nonce                *big.Int
	InitCode             []byte
	CallData             []byte
	CallGasLimit         *big.Int
	VerificationGasLimit *big.Int
	PreVerificationGas   *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	PaymasterAndData     []byte
	Signature            []byte
}

// entryPointABIJSON 은 번들러가 쓰는 EntryPoint 함수만 담은 JSON ABI.
// (TS entrypoint.ts ENTRYPOINT_ABI 대응 — handleOps + 가스풀 deposit/withdraw)
const entryPointABIJSON = `[
  {"type":"function","name":"handleOps","stateMutability":"nonpayable","inputs":[
    {"name":"ops","type":"tuple[]","components":[
      {"name":"sender","type":"address"},
      {"name":"nonce","type":"uint256"},
      {"name":"initCode","type":"bytes"},
      {"name":"callData","type":"bytes"},
      {"name":"callGasLimit","type":"uint256"},
      {"name":"verificationGasLimit","type":"uint256"},
      {"name":"preVerificationGas","type":"uint256"},
      {"name":"maxFeePerGas","type":"uint256"},
      {"name":"maxPriorityFeePerGas","type":"uint256"},
      {"name":"paymasterAndData","type":"bytes"},
      {"name":"signature","type":"bytes"}
    ]},
    {"name":"beneficiary","type":"address"}
  ],"outputs":[]},
  {"type":"function","name":"depositGlobalGasPool","stateMutability":"payable","inputs":[],"outputs":[]},
  {"type":"function","name":"withdrawGlobalGasPool","stateMutability":"nonpayable","inputs":[
    {"name":"to","type":"address"},
    {"name":"amount","type":"uint256"}
  ],"outputs":[]}
]`

var (
	entryPointABIOnce sync.Once
	entryPointABI     abi.ABI
)

// EntryPointABI 는 파싱된 EntryPoint ABI를 반환한다(entrypoint 도메인과 공유).
func EntryPointABI() abi.ABI {
	entryPointABIOnce.Do(func() {
		parsed, err := abi.JSON(strings.NewReader(entryPointABIJSON))
		if err != nil {
			panic("userop: invalid EntryPoint ABI: " + err.Error())
		}
		entryPointABI = parsed
	})
	return entryPointABI
}
