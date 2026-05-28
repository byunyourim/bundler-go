// Command bundler 는 ERC-4337 번들러 + 트랜잭션 실행 HTTP 서버다.
// 기존 Next.js 번들러(api 라우트)를 Go HTTP 서버로 이식.
//
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
	"syscall"
	"time"

	"github.com/byunyourim/stablecoin-bundler/internal/deploy"
	"github.com/byunyourim/stablecoin-bundler/internal/entrypoint"
	"github.com/byunyourim/stablecoin-bundler/internal/split"
	"github.com/byunyourim/stablecoin-bundler/internal/transfer"
	"github.com/byunyourim/stablecoin-bundler/internal/userop"
	"github.com/byunyourim/stablecoin-bundler/internal/platform/logger"
)

func main() {
	log := logger.New("bundler", slog.LevelInfo, true)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// TODO(골격): env.Load() → ethclient/redis/kms wiring 후 핸들러에 주입.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /api/create2/deploy", deploy.NewHandler().Deploy)
	mux.HandleFunc("POST /api/transfer", transfer.NewHandler().Transfer)
	mux.HandleFunc("POST /api/entrypoint/transfer", entrypoint.NewHandler().Transfer)
	mux.HandleFunc("POST /api/eoa-funded-split/execute-with-signatures", split.NewHandler().Execute)
	mux.HandleFunc("POST /api/bundler", userop.NewHandler().RPC)
	mux.HandleFunc("POST /api/bundler/{chainId}", userop.NewHandler().RPC)

	srv := &http.Server{Addr: ":3000", Handler: mux}

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
