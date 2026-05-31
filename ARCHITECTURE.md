# 아키텍처 (Go 멀티번들러)

기존 Next.js(TS) 번들러를 Go로 이식하고, 멀티프로세스에서 안전하게 동작하는
**체인별 핫월렛 풀 + redis 분산 nonce 락** 구조로 재설계했다.

- 도메인별 납작 패키지(`internal/<domain>`) + 인프라(`internal/platform/*`)
- 공용 의존성은 `internal/core.Deps`로 1회 조립해 주입
- `userop` 배치 큐와 `transfer`(sendViaAccount)는 **하나의 `Batcher`를 공유**

---

## 1. 컴포넌트 구조

```mermaid
flowchart TB
    subgraph clients["클라이언트"]
        worker["Worker / Adapter"]
    end

    subgraph http["HTTP (net/http ServeMux)"]
        rpc["POST /api/bundler[/chainId]<br/>userop.Handler"]
        tr["POST /api/transfer<br/>transfer.Handler"]
        dep["POST /api/create2/deploy<br/>deploy.Handler"]
        ep["POST /api/entrypoint/transfer<br/>entrypoint.Handler"]
        sp["POST /api/eoa-funded-split/...<br/>split.Handler"]
    end

    subgraph domain["도메인 로직"]
        batcher["userop.Batcher<br/>(배치 큐 + 핫월렛 풀)"]
        trsvc["transfer.Service"]
        spsvc["split.Service"]
    end

    subgraph core["core.Deps (공용 의존성)"]
        reg["chain.Registry<br/>(체인 설정)"]
        signers["signer.Manager<br/>(핫월렛 풀 + server_key1/2)"]
        clientsP["ethclient.Provider<br/>(체인별 client 캐시)"]
        locker["redis.Locker<br/>(nonce 락 + RR 커서)"]
    end

    subgraph infra["인프라"]
        kmsP["kms.Provider<br/>keystore(SQLite)+NHN KMS"]
        redisDB[("Redis")]
        chainRPC[("EVM RPC<br/>(체인별)")]
    end

    worker --> rpc & tr & dep & ep & sp
    rpc --> batcher
    tr --> trsvc
    sp --> spsvc
    trsvc -. sendViaAccount .-> batcher

    batcher & trsvc & spsvc --> core
    dep & ep --> core

    signers --> kmsP
    locker --> redisDB
    clientsP --> chainRPC
    batcher --> locker & signers & clientsP & reg
```

---

## 2. 핵심 흐름: `eth_sendUserOperation` (멀티번들러)

체인당 디스패처 1개가 op를 배치로 모으고, **redis 라운드로빈 커서**로 핫월렛을 골라
`handleOps`를 보낸다. 서로 다른 월렛은 nonce 락이 독립이라 동시 제출된다.

```mermaid
sequenceDiagram
    autonumber
    participant C as 클라이언트
    participant H as userop.Handler
    participant B as Batcher
    participant D as dispatcher(체인당 1)
    participant L as redis.Locker
    participant W as flush goroutine
    participant N as EVM RPC

    C->>H: POST /api/bundler/{chainId}<br/>eth_sendUserOperation
    H->>H: handleRPC → chainId 결정<br/>UserOp → PackedUserOp
    H->>B: Enqueue(chainId, op)
    B->>B: queueFor: 풀 로드(최초 1회)<br/>디스패처 시작
    B->>D: op → channel
    Note over C,H: 호출자는 결과(txHash)를<br/>res 채널에서 대기

    loop 디스패처 루프
        D->>D: 배치 그리디 수집(≤50)
        D->>L: NextIndex(chainId, N)<br/>INCR walletcursor % N
        L-->>D: idx (라운드로빈)
        D->>D: semaphore 획득(≤풀 크기)
        D->>W: go flush(pool[idx], batch)
    end

    W->>L: WithNonceLock(nonce:chainId:wallet)
    Note over L: 월렛별 전역 직렬화<br/>(SET NX PX, 멀티프로세스)
    L-->>W: 락 획득
    W->>N: PendingNonceAt(wallet) (캐시 안 함)
    N-->>W: nonce
    W->>N: handleOps(ops[], beneficiary=0x0)
    N-->>W: tx
    W->>N: WaitMined(tx)
    N-->>W: receipt
    W->>L: 락 해제
    W-->>C: 배치 내 모든 op에 txHash 전달
    H-->>C: { jsonrpc, id, result: txHash }
```

> 실패 시 `platform/txerror.Normalize`로 분류해 JSON-RPC `error.data`에
> `{code, category, ethersCode, txHash, retryable}`를 담는다.

---

## 3. 전송: `POST /api/transfer`

`source=wallet`은 server_key1/2로 직접 서명, `source=account`는 2-of-3 서명 UserOp를
같은 Batcher로 제출한다.

```mermaid
sequenceDiagram
    autonumber
    participant C as 클라이언트
    participant H as transfer.Handler
    participant S as transfer.Service
    participant L as redis.Locker
    participant B as Batcher
    participant N as EVM RPC

    C->>H: POST /api/transfer<br/>{type, source, from, to, amount}
    H->>H: 검증(주소/금액)

    alt source = wallet (EOA 직접)
        S->>S: resolveSourceWallet<br/>(from == server_key1/2 ?)
        S->>L: WithNonceLock(nonce:chainId:wallet)
        L-->>S: 락
        S->>N: FeeFromProvider + EstimateGas
        S->>N: PendingNonceAt → SignAndSend
        N-->>S: txHash (채굴 대기 없음)
    else source = account (2-of-3 → UserOp)
        S->>L: WithNonceLock(nonce:chainId:account:from)
        L-->>S: 락
        S->>N: account.nonce() (온체인)
        S->>S: getDigest → sign65(server_key1)<br/>+ sign65(server_key2)
        S->>S: Pack executeWithSignatures → UserOp
        S->>B: Enqueue(chainId, userOp)
        Note over B,N: §2 흐름으로 handleOps 제출
        B-->>S: txHash
    end
    H-->>C: { txHash, from }
```

---

## 4. 결정론 작업: deploy / entrypoint (PrimaryBundler 고정)

배포 주소는 deployer에 의존하므로 라운드로빈하지 않고 풀의 `[0]`(PrimaryBundler)을 쓴다.

```mermaid
sequenceDiagram
    autonumber
    participant C as 클라이언트
    participant H as deploy.Handler / entrypoint.Handler
    participant Sg as signer.Manager
    participant Dx as core.Deps.SendManagedTx
    participant L as redis.Locker
    participant N as EVM RPC

    C->>H: POST /api/create2/deploy<br/>또는 /api/entrypoint/transfer
    H->>Sg: PrimaryBundler(chainId) = pool[0]
    opt deploy
        H->>N: factory.computeAddress (예측 주소)
    end
    H->>Dx: SendManagedTx(primary, method, args, value?)
    Dx->>L: WithNonceLock(nonce:chainId:primary)
    L-->>Dx: 락
    Dx->>N: PendingNonceAt → Transact(method) → WaitMined
    N-->>Dx: receipt
    Dx-->>H: tx, receipt
    Note over H: deploy: Deployed 이벤트 → 배포 주소<br/>entrypoint: gasFee = gasUsed×effectivePrice
    H-->>C: 결과 JSON
```

---

## 5. 정산: EOA-funded-split (EIP-712)

payer(server_key1/2) EOA가 트랜잭션·자금 출처. server_key1·2가 PayerSplit을 EIP-712 서명.

```mermaid
sequenceDiagram
    autonumber
    participant C as 클라이언트
    participant H as split.Handler
    participant S as split.Service
    participant L as redis.Locker
    participant R as Router(온체인)
    participant N as EVM RPC

    C->>H: POST /api/eoa-funded-split/execute-with-signatures
    H->>H: 검증 + permit 검사
    S->>L: WithNonceLock(nonce:chainId:payer)
    L-->>S: 락
    S->>R: signerEpoch() / payerNonce(payer)
    S->>N: PendingNonceAt(payer) (EOA nonce)
    opt ERC20 & permit 미사용 & allowance 부족
        S->>N: approve(router, needed) → WaitReceipt
    end
    S->>S: EIP-712 PayerSplit 로컬 해시
    S->>R: hashTypedDataPayerSplit(...) 대조 검증
    S->>S: sign65(server_key1) + sign65(server_key2)
    S->>N: executeWithSignatures(...) (value=nativeTotal)
    N-->>S: txHash
    Note over S,R: EFS024/nonce 에러 시 payerNonce 재조회 후 1회 재시도
    H-->>C: { txHash, payer, routerAddress, nonceUsed, approveTxHash? }
```

---

## 6. 멀티프로세스 동시성 모델

```mermaid
flowchart LR
    subgraph p1["프로세스 A"]
        d1["dispatcher(chain X)"]
    end
    subgraph p2["프로세스 B"]
        d2["dispatcher(chain X)"]
    end

    d1 & d2 -->|"NextIndex / WithNonceLock"| R[("Redis")]

    subgraph pool["체인 X 핫월렛 풀"]
        w0["W0 — nonce:X:W0"]
        w1["W1 — nonce:X:W1"]
        w2["W2 — nonce:X:W2"]
    end

    R -. "월렛별 락 전역 단일 직렬화" .-> w0 & w1 & w2
    w0 & w1 & w2 --> chain[("EVM (체인 X)")]
```

- **선택**: `walletcursor:{chainId}` redis INCR → 풀을 라운드로빈 (전역 분산)
- **직렬화**: `nonce:{chainId}:{wallet}` 락 — 어느 프로세스가 어느 월렛을 쓰든 그 월렛의
  nonce 줄은 전역에서 하나로 직렬화. nonce는 캐시하지 않고 락 안에서 온체인 pending을 읽는다.
- **병렬성**: 서로 다른 월렛은 락이 독립 → 체인당 동시 in-flight tx = 풀 크기 N
- redis 미설정 시 둘 다 프로세스 내 폴백(단일 인스턴스 전용)

검증: `internal/userop/batcher_test.go`
(라운드로빈 분산 / 월렛별 직렬화 / 교차 월렛 병렬성 / nonce 무충돌, `go test -race`).
