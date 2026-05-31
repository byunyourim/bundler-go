# StableCoin Bundler (Go)

ERC-4337 번들러 + 트랜잭션 실행 서비스. CREATE2 지갑 배포, native/erc20 전송,
EntryPoint 가스풀 관리, EOA-funded-split 정산, UserOperation 번들링을 REST/JSON-RPC로 제공한다.
기존 Next.js(TypeScript) 번들러를 Go로 재작성한 프로젝트.

> 📊 컴포넌트 구조·시퀀스 다이어그램·전체 흐름은 [ARCHITECTURE.md](./ARCHITECTURE.md) 참고 (mermaid).

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
  evm/                       # CREATE2 주소계산, 가스/서명/숫자 헬퍼
  signer/                    # ★ 멀티번들러 핫월렛 풀 + server_key1/2 (아래 참고)
  core/                      # 도메인 공용 의존성(Deps) + 관리 tx 헬퍼

  # ── 플랫폼 ──
  platform/
    txerror/                 # ★ 트랜잭션/RPC 실패 정밀 분류 (아래 참고)
    httpx/                   # 구조화 에러 응답 헬퍼
    ethclient/               # 체인별 go-ethereum client 캐시
    redis/                   # nonce 분산락 + 라운드로빈 커서
    kms/                     # 암호화 keystore(SQLite) + NHN KMS 복호화 (개인키 평문 금지)
    env/ logger/
```

라우트 매핑은 기존 Next.js `app/api/*`와 1:1.

---

## ★ 멀티번들러 (TS 대비 구조 개선)

기존 TS는 단일 번들러 키(`owner_key`)를 모든 체인에서 공유하고, 배치 큐가 nonce를
프로세스 메모리에 들고 있어 **멀티프로세스에서 nonce가 충돌**했다. Go 버전은 두 축으로 개선한다.

- **체인별 핫월렛 풀** — `bundler_key_<chainId>_0..N` 키를 풀로 로드(`internal/signer`).
  `userop` 배치 큐는 체인당 디스패처 1개가 채널에서 op를 그리디하게 모아 배치를 만들고,
  **redis 라운드로빈 커서**(`Locker.NextIndex` = `INCR walletcursor:{chainId} % N`)로 풀에서
  월렛을 골라 `handleOps`를 보낸다. 동시 flush는 풀 크기만큼 허용(semaphore)하고, 서로 다른
  월렛은 nonce 락이 독립이라 **체인당 동시 in-flight tx N개** → 단일 EOA의 nonce 직렬화 병목
  제거. 커서가 redis에 있어 멀티프로세스에서도 부하가 전역 분산된다(redis 미설정 시 프로세스
  내 카운터로 폴백). 핫월렛 1개(또는 `OWNER_KEY`/`owner_key`)면 풀 크기 1로 기존 TS와 동일.

- **멀티프로세스 안전 nonce** — nonce는 캐시하지 않고 `WithNonceLock(nonce:{chainId}:{wallet})`
  안에서 매번 온체인 `PendingNonceAt`를 읽는다. redis 락이 월렛별 nonce 줄을 전역 단일
  직렬화하므로 프로세스를 N개 띄워도 안전. redis 미설정 시 인메모리 락(단일 인스턴스 전용).

- **결정론 작업은 풀 제외** — 배포 주소는 deployer에 의존하므로 `create2 deploy`와
  `entrypoint deposit/withdraw`는 라운드로빈하지 않고 `PrimaryBundler`(풀 `[0]`, 고정)를 쓴다.

`server_key1`/`server_key2`(2-of-3 공동서명자)와 EOA 직접 전송은 풀 대상이 아니라 단일 키.

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
| nonce 락/커서 | redis/go-redis | TS ioredis 대응 (멀티프로세스 직렬화) |
| 키 관리 | NHN KMS + 암호화 keystore(SQLite) | 개인키 평문 금지, 핫월렛 풀 |
| keystore 드라이버 | modernc.org/sqlite | pure-Go (cgo 불필요) |
| 로깅 | log/slog (pino 포맷) | ELK 인입 유지 (리스너·어댑터와 동일) |
| 에러 | `platform/txerror` | TS tx-error.ts 계약 이식 |

> DB: keystore용 SQLite만 사용(readonly). 키는 KMS로 가는 게 우선이라 범용 DB는 도입하지 않음.

---

## 시작하기

```bash
brew install go golangci-lint
go mod tidy   # go-ethereum / go-redis / caarlos0/env / modernc.org/sqlite

cp .env.example .env
make build && make test
make run   # :3000
```

> 현재 상태: **이식 완료**. 전 도메인(userop·transfer·deploy·entrypoint·split) +
> 멀티번들러 핫월렛 풀(`signer`)·KMS keystore(`kms`)·분산락(`redis`) 구현. `make test` 통과.

---

## 관련 프로젝트

| 프로젝트 | 역할 | 연동 |
|----------|------|------|
| StableCoinBC_Adapter | 오케스트레이션 | → 이 번들러를 HTTP로 호출, 구조화 에러 code 소비 |
| StableCoinBC_Adapter_Listener | 입금 감지 | (어댑터 경유) |
