// Package kms 는 암호화된 keystore + KMS 복호화를 담당한다.
// (TS의 lib/kms, kms-nhn, db-keystore, key-provider 대응)
//
// 보안: 개인키 평문을 env/코드/로그에 두지 않는다. keystore에는 암호문(EncKey)만 두고
// KMS로 복호화한다. 서명 자체는 internal/signer가 복호화된 키로 수행한다.
package kms

import (
	"context"
	"fmt"
	"strings"
)

// KeyEntry 는 keystore 한 행(KeyID + 암호문 Base64).
type KeyEntry struct {
	KeyID            string
	CiphertextBase64 string
}

// Decryptor 는 KMS 복호화 클라이언트. (TS KmsClient.decrypt 대응)
type Decryptor interface {
	Decrypt(ctx context.Context, keyID, ciphertextBase64 string) (string, error)
}

// Keystore 는 암호화된 키 저장소(SQLite keys.db 등). (TS db-keystore 대응)
type Keystore interface {
	// Lookup 은 Name 행의 KeyID·EncKey를 반환한다. 없으면 found=false.
	Lookup(name string) (entry KeyEntry, found bool, err error)
	// ListByPrefix 는 Name이 prefix로 시작하는 행 이름을 정렬해 반환한다(풀 탐색용).
	ListByPrefix(prefix string) ([]string, error)
	Close() error
}

// Provider 는 keystore + Decryptor를 묶어 이름으로 평문 키(hex)를 로드한다.
// dec/ks 둘 다 있어야 Enabled. (없으면 env fallback 모드)
type Provider struct {
	dec Decryptor
	ks  Keystore
}

// NewProvider 생성. dec/ks는 nil 가능.
func NewProvider(dec Decryptor, ks Keystore) *Provider {
	return &Provider{dec: dec, ks: ks}
}

// Enabled 는 keystore+KMS 경로가 사용 가능한지 반환한다.
func (p *Provider) Enabled() bool {
	return p != nil && p.dec != nil && p.ks != nil
}

// LoadHexKey 는 keystore에서 name 행을 찾아 KMS로 복호화한 평문(hex)을 반환한다.
// 행이 없으면 found=false.
func (p *Provider) LoadHexKey(ctx context.Context, name string) (key string, found bool, err error) {
	if !p.Enabled() {
		return "", false, nil
	}
	entry, ok, err := p.ks.Lookup(name)
	if err != nil {
		return "", false, fmt.Errorf("keystore lookup %q: %w", name, err)
	}
	if !ok {
		return "", false, nil
	}
	plain, err := p.dec.Decrypt(ctx, entry.KeyID, entry.CiphertextBase64)
	if err != nil {
		return "", false, fmt.Errorf("kms decrypt %q: %w", name, err)
	}
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", false, fmt.Errorf("kms decrypt %q: empty plaintext", name)
	}
	return plain, true, nil
}

// ListNames 는 prefix로 시작하는 keystore 행 이름을 반환한다(풀 탐색).
func (p *Provider) ListNames(prefix string) ([]string, error) {
	if !p.Enabled() {
		return nil, nil
	}
	return p.ks.ListByPrefix(prefix)
}

// Close 는 keystore 핸들을 닫는다.
func (p *Provider) Close() error {
	if p != nil && p.ks != nil {
		return p.ks.Close()
	}
	return nil
}
