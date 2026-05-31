// Package core 는 도메인 핸들러가 공유하는 런타임 의존성 묶음을 정의한다.
// main이 1회 조립해 각 도메인에 주입한다.
package core

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/byunyourim/stablecoin-bundler/internal/chain"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/ethclient"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/redis"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

// Deps 는 체인 설정·클라이언트·서명자·분산락 등 공용 의존성.
type Deps struct {
	Reg     *chain.Registry
	Clients *ethclient.Provider
	Signers *signer.Manager
	Locker  *redis.Locker
	LockTTL time.Duration
	Log     *slog.Logger
}

// NonceLockKey 는 nonce 분산락 키(`nonce:{chainId}:{address}`)를 만든다.
func NonceLockKey(chainID int64, address string) string {
	return "nonce:" + strconv.FormatInt(chainID, 10) + ":" + address
}
