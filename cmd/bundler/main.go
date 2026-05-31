// Command bundler 는 ERC-4337 번들러 + 트랜잭션 실행 HTTP 서버다.
// 기존 Next.js 번들러(api 라우트)를 Go HTTP 서버로 이식.
//
// 멀티번들러: 체인별 핫월렛 풀(signer) + redis 분산 nonce 락으로 멀티프로세스에서 동작한다.
// 실패 응답은 platform/txerror로 정밀 분류 — 어댑터가 code/category/retryable로
// 재시도·DLQ를 판단한다(README "에러 계약" 참고).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/byunyourim/stablecoin-bundler/internal/chain"
	"github.com/byunyourim/stablecoin-bundler/internal/core"
	"github.com/byunyourim/stablecoin-bundler/internal/deploy"
	"github.com/byunyourim/stablecoin-bundler/internal/entrypoint"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/env"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/ethclient"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/kms"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/logger"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/redis"
	"github.com/byunyourim/stablecoin-bundler/internal/signer"
	"github.com/byunyourim/stablecoin-bundler/internal/split"
	"github.com/byunyourim/stablecoin-bundler/internal/transfer"
	"github.com/byunyourim/stablecoin-bundler/internal/userop"
)

func main() {
	cfg, err := env.Load()
	if err != nil {
		slog.Error("env load failed", "err", err)
		os.Exit(1)
	}
	log := logger.New("bundler", parseLevel(cfg.LogLevel), cfg.LogPretty)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	reg := chain.NewRegistry()
	if err := reg.AssertConfig(); err != nil {
		log.Error("config invalid", "err", err)
		os.Exit(1)
	}

	// redis 분산 nonce 락 (미설정 시 인메모리 — 단일 인스턴스 전용).
	rdb, err := redis.Dial(cfg.RedisURL, cfg.RedisHost, cfg.RedisPort, cfg.RedisPassword)
	if err != nil {
		log.Error("redis dial failed", "err", err)
		os.Exit(1)
	}
	if rdb == nil {
		log.Warn("redis not configured — using in-memory nonce lock (single instance only)")
	}
	locker := redis.NewLocker(rdb, cfg.NonceLockTTL())

	// KMS + 암호화 keystore (운영). 미설정 시 env fallback 키로 동작.
	kmsProvider := buildKMS(cfg, log)
	defer func() { _ = kmsProvider.Close() }()

	signers := signer.NewManager(kmsProvider, locker, signer.EnvKeys{
		OwnerKey:   cfg.OwnerKey,
		BundlerKey: cfg.BundlerKey,
		ServerKey1: cfg.ServerKey1,
		ServerKey2: cfg.ServerKey2,
	})

	clients := ethclient.New(reg)
	defer clients.Close()

	deps := &core.Deps{
		Reg:     reg,
		Clients: clients,
		Signers: signers,
		Locker:  locker,
		LockTTL: cfg.NonceLockTTL(),
		Log:     log,
	}

	// userop 배치 큐는 transfer(sendViaAccount)와 공유.
	batcher := userop.NewBatcher(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /api/create2/deploy", deploy.NewHandler(deps).Deploy)
	mux.HandleFunc("POST /api/transfer", transfer.NewHandler(deps, batcher).Transfer)
	mux.HandleFunc("POST /api/entrypoint/transfer", entrypoint.NewHandler(deps).Transfer)
	mux.HandleFunc("POST /api/eoa-funded-split/execute-with-signatures", split.NewHandler(deps).Execute)
	mux.HandleFunc("POST /api/bundler", userop.NewHandler(deps, batcher).RPC)
	mux.HandleFunc("POST /api/bundler/{chainId}", userop.NewHandler(deps, batcher).RPC)

	srv := &http.Server{Addr: cfg.Listen, Handler: mux}

	go func() {
		log.Info("bundler listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("bundler shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
}

// buildKMS 는 NHN KMS Decryptor + SQLite keystore로 Provider를 만든다.
// NHN_APPKEY/KEY_DB_PATH 미설정 시 비활성(Provider.Enabled=false) — env 키 fallback으로 동작.
func buildKMS(cfg *env.Config, log *slog.Logger) *kms.Provider {
	var dec kms.Decryptor
	if cfg.NHNAppKey != "" {
		c, err := kms.NewNHNClient(cfg.NHNAppKey, cfg.NHNBaseURL)
		if err != nil {
			log.Error("NHN KMS init failed", "err", err)
			os.Exit(1)
		}
		dec = c
	}
	var ks kms.Keystore
	if cfg.KeyDBPath != "" {
		store, err := kms.OpenSQLite(cfg.KeyDBPath)
		if err != nil {
			log.Error("keystore open failed", "err", err, "path", cfg.KeyDBPath)
			os.Exit(1)
		}
		ks = store
	}
	provider := kms.NewProvider(dec, ks)
	if !provider.Enabled() {
		log.Warn("KMS/keystore not configured — using env fallback keys (local/test)")
	}
	return provider
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
