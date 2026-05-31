// Package signer 는 번들러 핫월렛 풀 + 서명자(server_key1/2)를 관리한다.
//
// 멀티번들러 핵심: 체인별 핫월렛 풀(bundler_key_<chainId>_<idx>)에서 라운드로빈으로
// 제출용 월렛을 고른다. nonce 직렬화는 redis.Locker(nonce:{chain}:{addr})가 담당하므로
// 풀 크기 N = 체인당 동시 in-flight tx N개.
//
// 평문 개인키는 메모리에만 보관한다(부팅 시 KMS 복호화). 로그·env 평문 금지.
package signer

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Account 는 체인 무관 키 머티리얼(개인키 + 주소).
type Account struct {
	priv *ecdsa.PrivateKey
	addr common.Address
}

// newAccount 는 hex 개인키(0x 접두어 선택)로 Account를 만든다.
func newAccount(hexKey string) (*Account, error) {
	k := strings.TrimSpace(hexKey)
	k = strings.TrimPrefix(k, "0x")
	k = strings.TrimPrefix(k, "0X")
	priv, err := crypto.HexToECDSA(k)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return &Account{priv: priv, addr: crypto.PubkeyToAddress(priv.PublicKey)}, nil
}

// Address 는 EOA 주소를 반환한다.
func (a *Account) Address() common.Address { return a.addr }

// PrivateKey 는 ECDSA 개인키를 반환한다(서명용).
func (a *Account) PrivateKey() *ecdsa.PrivateKey { return a.priv }

// Transactor 는 지정 체인용 bind.TransactOpts를 만든다(서명자 포함).
func (a *Account) Transactor(chainID *big.Int) (*bind.TransactOpts, error) {
	return bind.NewKeyedTransactorWithChainID(a.priv, chainID)
}
