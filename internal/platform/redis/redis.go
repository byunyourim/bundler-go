// Package redis 는 nonce 분산락을 제공한다. (TS의 lib/nonce-lock + redis 대응)
// 디플로이어/payer 계정 단위로 nonce 직렬화를 보장 — 동시 트랜잭션 충돌 방지.
package redis

import (
	"context"
	"time"
)

// Locker 는 nonce 분산락.
type Locker struct {
	ttl time.Duration
	// TODO(골격): *redis.Client
}

// NewLocker Locker 생성.
func NewLocker(ttl time.Duration) *Locker {
	return &Locker{ttl: ttl}
}

// WithNonceLock 은 key(`nonce:{chainId}:{address}`) 락을 잡고 fn을 실행한다.
//
// TODO(골격): SET NX PX + Lua 해제. (TS withNonceLock 대응)
func (l *Locker) WithNonceLock(ctx context.Context, key string, fn func() error) error {
	panic("not implemented")
}
