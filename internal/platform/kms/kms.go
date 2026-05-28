// Package kms 는 서명 키 관리를 담당한다. (TS의 lib/kms, kms-nhn, db-keystore, key-provider 대응)
//
// 보안: 개인키 평문을 env/코드/로그에 두지 않는다. KMS 경유 서명 또는
// 암호화된 keystore에서 로드한다.
package kms

import "context"

// Provider 는 키 식별자(server_key1 등)로 서명자를 제공한다.
type Provider struct {
	// TODO(골격): KMS 클라이언트 / keystore 핸들
}

// New Provider 생성.
func New() *Provider {
	return &Provider{}
}

// Signer 는 키 식별자에 대응하는 서명자를 반환한다.
//
// TODO(골격): KMS 기반 서명자 또는 keystore 복호화.
func (p *Provider) Signer(ctx context.Context, keyID string) (any, error) {
	panic("not implemented")
}
