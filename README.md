# StableCoin Bundler (Go)

ERC-4337 번들러 + 트랜잭션 실행 서비스. CREATE2 지갑 배포, native/erc20 전송,
EntryPoint 가스풀 관리, EOA-funded-split 정산, UserOperation 번들링을 REST/JSON-RPC로 제공한다.
기존 Next.js(TypeScript) 번들러를 Go로 재작성한 프로젝트.

---

## 아키텍처: 도메인별 납작 패키지 (리스너·어댑터와 동일)

기술 레이어로 나누지 않고 도메인 패키지를 `internal/` 바로 아래에 두며, 인프라만 `platform/`으로 묶는다.

```
cmd/bundler/main.go          # HTTP 서버 진입점 (net/http ServeMux)

internal/
  # ── 도메인 (각 패키지가 HTTP 핸들러 + 로직) ──
  deploy/                    # POST /api/create2/deploy       (CREATE2 지갑 배포)
  transfer/                  # POST /api/transfer             (native/erc20)
  entrypoint/                # POST /api/entrypoint/transfer  (가스풀 deposit/withdraw)
  split/                     # POST /api/eoa-funded-split/... (정산 split)
  userop/                    # POST /api/bundler[/{chainId}]  (ERC-4337 JSON-RPC + batch)
  chain/                     # 체인별 RPC/Factory/EntryPoint 설정
  evm/                       # CREATE2 주소계산, ABI 헬퍼

  # ── 플랫폼 ──
  platform/
    txerror/                 # ★ 트랜잭션/RPC 실패 정밀 분류 (아래 참고)
    httpx/                   # 구조화 에러 응답 헬퍼
    ethclient/               # 체인별 go-ethereum provider/signer
    redis/                   # nonce 분산락
    kms/                     # 서명 키 관리 (개인키 평문 금지)
    env/ logger/
```

라우트 매핑은 기존 Next.js `app/api/*`와 1:1.

---

## ★ 에러 계약 (TS 번들러에서 그대로 이식)

기존 TS 번들러 `lib/tx-error.ts`의 `normalizeTxError`를 `platform/txerror`로 옮겼다.
**출력 계약은 동일**, 추출 내부만 ethers → go-ethereum으로 적응했다.

| 추출원 | ethers (TS) | go-ethereum (Go) |
|--------|-------------|------------------|
| revert reason | `error.reason` / `error.revert` | `rpc.DataError.ErrorData()` → `abi.UnpackRevert` |
| 노드 RPC 코드 | `error.info.error.code` | `rpc.Error.ErrorCode()` |
| 그 외 | 문자열 휴리스틱 | 문자열 휴리스틱 (nonce/funds/timeout/network) |

**동일하게 유지되는 분류** (어댑터 `platform/bundler.ClassifyResponse`가 그대로 소비):

| 원인 | code | status | retryable |
|------|------|--------|-----------|
| revert (AC*/EP*/PM*, CALL_EXCEPTION) | `AC007` 등 | 422 | ❌ → DLQ |
| 잔액/가스 부족 | `INSUFFICIENT_FUNDS` | 422 | ❌ |
| nonce 경합 | `BUNDLER_NONCE_CONFLICT` | 409 | ✅ |
| 타임아웃/네트워크/서버 | `RPC_TIMEOUT`/`RPC_NETWORK_ERROR`/`RPC_SERVER_ERROR` | 503 | ✅ |
| 미상 | `BUNDLER_SEND_FAILED` | 500 | ✅ |

응답 body: `{ error, code, category, ethersCode, reason, txHash, retryable }`
(JSON-RPC는 `error.data`에 `code/category/retryable`). 어댑터·번들러 code는 반드시 일치.

---

## 기술 스택

| 영역 | 선택 | 이유 |
|------|------|------|
| 언어 | Go 1.25 | 리스너·어댑터와 통일 |
| 체인 연동 | go-ethereum (`ethclient`/`abi`/`rpc`/`crypto`) | ethers 대응 |
| HTTP | 표준 `net/http` (ServeMux, Go 1.22+ 패턴 라우팅) | 경량, 의존 0 |
| nonce 락/캐시 | redis/go-redis | TS ioredis 대응 |
| 키 관리 | KMS / 암호화 keystore | 개인키 평문 금지 |
| 로깅 | log/slog (pino 포맷) | ELK 인입 유지 (리스너·어댑터와 동일) |
| 에러 | `platform/txerror` | TS tx-error.ts 계약 이식 |

> DB: 기존 번들러는 keystore용 SQLite만 사용. 키는 KMS로 가는 게 우선이라 범용 DB는 도입하지 않음.

---

## 시작하기

```bash
brew install go golangci-lint
go get github.com/ethereum/go-ethereum
go get github.com/redis/go-redis/v9
go get github.com/caarlos0/env/v11
go mod tidy

cp .env.example .env
make build && make test
make run   # :3000
```

> 현재 상태: **골격(skeleton)**. `platform/txerror`·`logger`·`httpx`는 구현 완료,
> 도메인 핸들러와 ethclient/redis/kms는 `panic("not implemented")` / `TODO(골격)`.

---

## 관련 프로젝트

| 프로젝트 | 역할 | 연동 |
|----------|------|------|
| StableCoinBC_Adapter | 오케스트레이션 | → 이 번들러를 HTTP로 호출, 구조화 에러 code 소비 |
| StableCoinBC_Adapter_Listener | 입금 감지 | (어댑터 경유) |
