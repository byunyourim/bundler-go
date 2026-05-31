package transfer

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var errPriorityGtMax = errors.New("USEROP_MAX_PRIORITY_FEE_PER_GAS_GWEI must be <= USEROP_MAX_FEE_PER_GAS_GWEI")

const accountABIJSON = `[
  {"type":"function","name":"nonce","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]},
  {"type":"function","name":"executeWithSignatures","stateMutability":"nonpayable","inputs":[
    {"name":"to","type":"address"},
    {"name":"value","type":"uint256"},
    {"name":"data","type":"bytes"},
    {"name":"signatures","type":"bytes[]"}
  ],"outputs":[]}
]`

const erc20ABIJSON = `[
  {"type":"function","name":"transfer","stateMutability":"nonpayable","inputs":[
    {"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"type":"bool"}]}
]`

var (
	abiOnce    sync.Once
	accountABI abi.ABI
	erc20ABI   abi.ABI
	digestArgs abi.Arguments
)

func initABIs() {
	abiOnce.Do(func() {
		var err error
		if accountABI, err = abi.JSON(strings.NewReader(accountABIJSON)); err != nil {
			panic("transfer: account ABI: " + err.Error())
		}
		if erc20ABI, err = abi.JSON(strings.NewReader(erc20ABIJSON)); err != nil {
			panic("transfer: erc20 ABI: " + err.Error())
		}
		addrT, _ := abi.NewType("address", "", nil)
		uint256T, _ := abi.NewType("uint256", "", nil)
		bytes32T, _ := abi.NewType("bytes32", "", nil)
		digestArgs = abi.Arguments{
			{Type: addrT}, {Type: uint256T}, {Type: addrT}, {Type: uint256T}, {Type: bytes32T},
		}
	})
}

// getDigest 는 온체인 Account._getDigest를 오프체인 재현한다.
// keccak256(abi.encode(account,nonce,to,value,keccak256(data))) → EIP-191 hashMessage.
// (TS transfer.ts getDigest 대응)
func getDigest(account common.Address, nonce *big.Int, to common.Address, value *big.Int, data []byte) ([]byte, error) {
	initABIs()
	var dataHash [32]byte
	copy(dataHash[:], crypto.Keccak256(data))
	enc, err := digestArgs.Pack(account, nonce, to, value, dataHash)
	if err != nil {
		return nil, err
	}
	inner := crypto.Keccak256(enc)
	// EIP-191: keccak256("\x19Ethereum Signed Message:\n32" + inner)
	return accounts.TextHash(inner), nil
}

// sign65 는 digest를 priv로 서명해 65바이트(r||s||v, v∈{27,28})를 반환한다.
// (TS signatureTo65Bytes 대응 — crypto.Sign의 v 0/1 → +27)
func sign65(digest []byte, priv *ecdsa.PrivateKey) ([]byte, error) {
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}
