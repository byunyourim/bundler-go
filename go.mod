module github.com/byunyourim/stablecoin-bundler

go 1.25

// 리스너·어댑터와 버전 통일 (stablecoin/go.work + `go work sync`).
//   go-ethereum v1.17.3
// 추가 예정(추가 시 세 모듈 동일 버전): redis/go-redis, caarlos0/env
require github.com/ethereum/go-ethereum v1.17.3
