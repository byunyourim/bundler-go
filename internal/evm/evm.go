// Package evm 은 EVM 공통 유틸이다. (TS의 create2-factory 주소계산, ABI/숫자 헬퍼 대응)
// CREATE2 주소 예측, salt 변환, JSON 숫자 파싱 등. go-ethereum crypto/abi 사용.
package evm

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Sign65 는 digest를 priv로 서명해 65바이트(r||s||v, v∈{27,28})를 반환한다.
// (TS signatureTo65Bytes / typedDataSigTo65BytesHex 대응 — crypto.Sign의 v 0/1 → +27)
func Sign65(digest []byte, priv *ecdsa.PrivateKey) ([]byte, error) {
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

// PredictCreate2 는 CREATE2 결정론적 주소를 계산한다.
// keccak256(0xff ++ deployer ++ salt ++ initCodeHash)[12:].
func PredictCreate2(deployer common.Address, salt [32]byte, initCodeHash [32]byte) common.Address {
	return crypto.CreateAddress2(deployer, salt, initCodeHash[:])
}

// ParseHexBytes 는 "0x..." 또는 "..." hex 문자열을 바이트로 변환한다. 빈 문자열/"0x"는 빈 슬라이스.
func ParseHexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0x" || s == "0X" {
		return []byte{}, nil
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		s = "0x" + s
	}
	b, err := hexutil.Decode(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return b, nil
}

// SaltToBytes32 는 salt를 bytes32로 만든다. 0x + 64hex(66자)면 그대로, 아니면 keccak256(utf8) (ethers.id 대응).
func SaltToBytes32(salt string) [32]byte {
	s := strings.TrimSpace(salt)
	var out [32]byte
	if (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) && len(s) == 66 {
		if b, err := hexutil.Decode(s); err == nil {
			copy(out[:], b)
			return out
		}
	}
	copy(out[:], crypto.Keccak256([]byte(s)))
	return out
}

// Keccak256 는 데이터의 keccak256 해시를 bytes32로 반환한다.
func Keccak256(data []byte) [32]byte {
	var out [32]byte
	copy(out[:], crypto.Keccak256(data))
	return out
}

// ToBigInt 는 JSON에서 온 값(string hex/decimal, float64 number)을 *big.Int로 변환한다.
// (TS BigInt(op.nonce) 대응 — hex/decimal/number 모두 허용)
func ToBigInt(v any) (*big.Int, error) {
	switch t := v.(type) {
	case nil:
		return nil, fmt.Errorf("nil value")
	case string:
		return parseBigString(t)
	case json.Number:
		return parseBigString(t.String())
	case float64:
		// JSON number. 큰 값은 정밀도 손실 위험 — 정수만 허용.
		if t != float64(int64(t)) {
			return nil, fmt.Errorf("non-integer number %v; send large values as string", t)
		}
		return big.NewInt(int64(t)), nil
	case *big.Int:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported numeric type %T", v)
	}
}

func parseBigString(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty number")
	}
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
		base = 16
	}
	n, ok := new(big.Int).SetString(s, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", s)
	}
	return n, nil
}

// Uint256 는 JSON에서 string(hex/decimal)·number 모두 받는 *big.Int 래퍼.
type Uint256 struct {
	V *big.Int
}

// UnmarshalJSON 은 hex/decimal 문자열 또는 number를 파싱한다.
func (u *Uint256) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == nil {
		u.V = nil
		return nil
	}
	n, err := ToBigInt(raw)
	if err != nil {
		return err
	}
	u.V = n
	return nil
}

// Big 은 nil이면 0을 반환한다.
func (u Uint256) Big() *big.Int {
	if u.V == nil {
		return big.NewInt(0)
	}
	return u.V
}

// HexQuantity 는 정수를 "0x.." JSON-RPC quantity 문자열로 만든다(ethers.toQuantity 대응).
func HexQuantity(n int64) string {
	return hexutil.EncodeUint64(uint64(n))
}
