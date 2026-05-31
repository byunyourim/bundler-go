// Package env 환경변수 파싱 전담 (TS의 lib/config 대응).
//
// 체인 어드레싱(RPC_URL_<chainId>, ENTRYPOINT_ADDRESS, *_FACTORY_*, EOFS_*)은
// 동적 키라 internal/chain.Registry가 직접 조회한다. 이 패키지는 전역 정적 설정만 담는다.
package env

import (
	"time"

	"github.com/caarlos0/env/v11"
)

// Config 번들러 전역 설정.
type Config struct {
	Listen string `env:"LISTEN" envDefault:":3000"` // HTTP 서버 주소

	// Redis — 미설정 시 인메모리 락만 사용(단일 인스턴스 전용). 멀티프로세스 배포 시 필수.
	RedisURL      string `env:"REDIS_URL"`
	RedisHost     string `env:"REDIS_HOST"`
	RedisPort     int    `env:"REDIS_PORT" envDefault:"6379"`
	RedisPassword string `env:"REDIS_PASSWORD"`

	DefaultChainID int64 `env:"DEFAULT_CHAIN_ID"`

	// nonce 분산락 TTL(ms). TS withNonceLock 의 ttlMs 대응.
	NonceLockTTLMs int `env:"NONCE_LOCK_TTL_MS" envDefault:"30000"`

	// 키 관리(KMS/keystore). 운영 키는 env 평문 금지 — keystore + KMS 경유.
	NHNAppKey  string `env:"NHN_APPKEY"`
	NHNBaseURL string `env:"NHN_KMS_BASE_URL"`
	KeyDBPath  string `env:"KEY_DB_PATH"`
	KeyName    string `env:"KEY_NAME"` // 단일키 fallback 행 이름(기본 owner_key)

	// 로컬/테스트 fallback 키(64 hex). 운영에서는 비워두고 keystore+KMS 사용.
	OwnerKey   string `env:"OWNER_KEY"`
	BundlerKey string `env:"BUNDLER_PRIVATE_KEY"`
	ServerKey1 string `env:"SERVER_KEY1"`
	ServerKey2 string `env:"SERVER_KEY2"`

	LogLevel  string `env:"LOG_LEVEL" envDefault:"debug"`
	LogPretty bool   `env:"LOG_PRETTY" envDefault:"true"`
}

// NonceLockTTL 은 ms 설정을 time.Duration으로 변환한다.
func (c *Config) NonceLockTTL() time.Duration {
	return time.Duration(c.NonceLockTTLMs) * time.Millisecond
}

// Load 환경변수 파싱 — 필수 누락 시 error.
func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
