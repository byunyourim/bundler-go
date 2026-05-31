// Package redis 는 nonce 분산락 + 라운드로빈 커서를 제공한다. (TS의 lib/nonce-lock + redis 대응)
// 핫월렛 주소 단위로 nonce 직렬화를 보장 — 멀티프로세스 동시 트랜잭션 충돌 방지.
//
// 직렬화는 2단: (1) 프로세스 내 keyed mutex, (2) redis SET NX PX 분산락.
// redis 미설정 시 (1)만으로 동작(단일 인스턴스 전용).
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	retryCount = 10
	retryDelay = 100 * time.Millisecond
)

// releaseScript 는 토큰이 일치할 때만 락을 해제한다(TTL 만료 후 타 보유자 락 삭제 방지).
var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end`)

// Locker 는 nonce 분산락. rdb가 nil이면 인메모리 락만 사용.
type Locker struct {
	rdb *redis.Client
	ttl time.Duration

	mu      sync.Mutex
	local   map[string]*sync.Mutex
	cursors map[string]uint64
}

// NewLocker Locker 생성. rdb는 nil 가능(인메모리 전용).
func NewLocker(rdb *redis.Client, ttl time.Duration) *Locker {
	return &Locker{rdb: rdb, ttl: ttl, local: make(map[string]*sync.Mutex), cursors: make(map[string]uint64)}
}

// lockLocal 은 key별 프로세스 내 mutex를 잡고 해제 함수를 반환한다.
func (l *Locker) lockLocal(key string) func() {
	l.mu.Lock()
	m, ok := l.local[key]
	if !ok {
		m = &sync.Mutex{}
		l.local[key] = m
	}
	l.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// WithNonceLock 은 key(`nonce:{chainId}:{address}`) 락을 잡고 fn을 실행한다. (TS withNonceLock 대응)
func (l *Locker) WithNonceLock(ctx context.Context, key string, fn func() error) error {
	unlock := l.lockLocal(key)
	defer unlock()

	if l.rdb == nil {
		return fn()
	}
	return l.withRedisLock(ctx, key, fn)
}

func (l *Locker) withRedisLock(ctx context.Context, key string, fn func() error) error {
	lockKey := "lock:" + key
	token, err := randomToken()
	if err != nil {
		return err
	}

	acquired := false
	for i := 0; i < retryCount; i++ {
		ok, err := l.rdb.SetNX(ctx, lockKey, token, l.ttl).Result()
		if err != nil {
			return fmt.Errorf("redis lock acquire: %w", err)
		}
		if ok {
			acquired = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	if !acquired {
		return fmt.Errorf("failed to acquire nonce lock: %s", key)
	}
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = releaseScript.Run(relCtx, l.rdb, []string{lockKey}, token).Err()
	}()

	return fn()
}

// NextIndex 는 라운드로빈 커서를 1 증가시키고 mod n 한 인덱스를 반환한다(0..n-1).
// redis 미설정 시 프로세스 내 카운터로 폴백.
func (l *Locker) NextIndex(ctx context.Context, cursorKey string, n int) (int, error) {
	if n <= 1 {
		return 0, nil
	}
	if l.rdb == nil {
		return l.nextIndexLocal(cursorKey, n), nil
	}
	v, err := l.rdb.Incr(ctx, "walletcursor:"+cursorKey).Result()
	if err != nil {
		return 0, fmt.Errorf("redis cursor incr: %w", err)
	}
	idx := int(((v % int64(n)) + int64(n)) % int64(n))
	return idx, nil
}

func (l *Locker) nextIndexLocal(cursorKey string, n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cur := l.cursors[cursorKey]
	l.cursors[cursorKey] = cur + 1
	return int(cur % uint64(n))
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Dial 은 url 또는 host/port/password로 redis.Client를 만든다. 미설정 시 (nil, nil).
func Dial(url, host string, port int, password string) (*redis.Client, error) {
	if url != "" {
		opt, err := redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		return redis.NewClient(opt), nil
	}
	if host == "" {
		return nil, nil
	}
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
	}), nil
}
