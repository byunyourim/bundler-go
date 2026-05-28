// Package env 환경변수 파싱 전담 (TS의 lib/config 대응). process.env 접근은 이 패키지에서만.
package env

import "time"

// Config 번들러 전역 설정.
type Config struct {
	Listen         string `env:"LISTEN" envDefault:":3000"` // HTTP 서버 주소
	RedisURL       string `env:"REDIS_URL,required"`
	DefaultChainID int64  `env:"DEFAULT_CHAIN_ID"`

	// 체인별 RPC/Factory/EntryPoint는 RPC_URL_<chainId> 등으로 동적 로드 (TS config.getRpcUrl 대응).
	NonceLockTTL time.Duration `env:"NONCE_LOCK_TTL_MS" envDefault:"30s"`

	// 키 관리(KMS/keystore) 설정. 운영 키는 env 평문 금지 — KMS 경유.
	KMSEndpoint string `env:"KMS_ENDPOINT"`

	LogLevel  string `env:"LOG_LEVEL" envDefault:"debug"`
	LogPretty bool   `env:"LOG_PRETTY" envDefault:"true"`
}

// Load 환경변수 파싱 — 필수 누락 시 error.
//
// TODO(골격): caarlos0/env로 구현. 새 env 추가 시 .env.example, README 동기화.
func Load() (*Config, error) {
	panic("not implemented")
}
