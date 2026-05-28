// Package logger 구조화 로깅 제공 (TS의 lib/logger = pino 대응).
//
// 운영 로그는 ELK로 수집된다. 리스너·어댑터와 동일한 pino 출력 포맷:
//   - level : 문자열 라벨 (trace/debug/info/warn/error)
//   - time  : KST ISO8601 "2006-01-02T15:04:05.000+09:00"
//   - msg / module / pid / hostname
//
// LOG_PRETTY 개발 모드는 text 핸들러(ELK용 아님).
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

const kstLayout = "2006-01-02T15:04:05.000-07:00"

var (
	kst      = time.FixedZone("KST", 9*60*60)
	hostname = resolveHostname()
)

// New TS 번들러 pino 포맷과 동일한 구조화 로거 생성.
func New(module string, level slog.Level, pretty bool) *slog.Logger {
	return build(os.Stdout, module, level, pretty)
}

func build(w io.Writer, module string, level slog.Level, pretty bool) *slog.Logger {
	var h slog.Handler
	if pretty {
		h = slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: pinoReplace})
	}
	return slog.New(h).With(
		slog.String("module", module),
		slog.Int("pid", os.Getpid()),
		slog.String("hostname", hostname),
	)
}

// ParseLevel LOG_LEVEL 문자열을 slog.Level로 변환(기본 info).
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "fatal":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func pinoReplace(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		return slog.String(slog.TimeKey, a.Value.Time().In(kst).Format(kstLayout))
	case slog.LevelKey:
		lvl, _ := a.Value.Any().(slog.Level)
		return slog.String(slog.LevelKey, pinoLabel(lvl))
	default:
		return a
	}
}

func pinoLabel(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	case l >= slog.LevelDebug:
		return "debug"
	default:
		return "trace"
	}
}

func resolveHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
