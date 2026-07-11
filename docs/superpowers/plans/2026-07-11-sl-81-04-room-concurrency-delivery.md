# SL-81 Stack 4 Room Concurrency and Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let rooms tick independently, remove per-tick global cleanup and socket writes, and close silent peers predictably.

**Architecture:** The store mutex owns only the room registry and store lifecycle; each room mutex owns gameplay and client state. Each connected client has a dedicated writer with a reliable control queue and a replaceable latest-snapshot slot, while one store janitor performs TTL cleanup.

**Tech Stack:** Go sync primitives, context, nhooyr WebSocket, fake clocks, race detector, benchmarks.

## Global Constraints

- Never acquire `Store.mu` while holding `room.mu`; deletion uses `deleteRoomIfSame(id, expected)` after releasing the room lock.
- Gameplay snapshots may coalesce; Ready, starting, terminal snapshot, GameEnd, and error messages may not be silently dropped or reordered.
- Normal writes use a 5-second deadline; heartbeat interval is 30 seconds and timeout is 90 seconds.
- Tick code never scans the complete store map.
- A cap rejection triggers one immediate cleanup/retry so janitor latency cannot create a false 409.

---

### Task 1: Add per-room ownership and parallel tick tests

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/handler_test.go`
- Modify: `internal/rooms/websocket_test.go`

**Interfaces:**
- Produces: `room.mu sync.Mutex`, `Store.mu sync.RWMutex`, `Store.getRoom(id) *room`, and `deleteRoomIfSame(id string, expected *room) bool`.

- [ ] **Step 1: Write a failing parallel-room test**

Inject a simulation step hook that blocks room A, tick room B, and assert B completes before A is released. Add concurrent input/list/delete/tick coverage under the race detector.

- [ ] **Step 2: Run the race test and verify RED**

Run: `mise exec -- go test -race ./internal/rooms -run 'TestStore.*Parallel|TestStore.*Concurrent' -count=1`

Expected: room B waits behind the single store mutex or the test times out.

- [ ] **Step 3: Move mutable fields behind `room.mu`**

Copy the room pointer under `Store.mu.RLock`, release the store lock, then lock the room for state/input/client mutations. For removal, mark/collect resources under the room lock, unlock, and call `deleteRoomIfSame`.

- [ ] **Step 4: Document lock ownership in code**

Add comments above `Store` and `room` listing protected fields and the forbidden room-to-store lock order.

- [ ] **Step 5: Run race tests repeatedly**

Run: `mise exec -- go test -race ./internal/rooms -count=20`

Expected: PASS without race reports or deadlock timeouts.

- [ ] **Step 6: Commit room-local synchronization**

```text
[SL-81] perf(rooms): room별 상태 락 분리

- store registry와 room gameplay lock 책임 분리
- 서로 다른 room tick 병렬성 회귀 테스트 추가
```

### Task 2: Replace tick cleanup with one janitor

**Files:**
- Modify: `internal/rooms/cleanup.go`
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/handler_test.go`

**Interfaces:**
- Produces: `janitorInterval = 30*time.Second`, `Store.startJanitor()`, `Store.cleanupExpired(now) int`, and Close-owned `janitorStop/janitorDone` channels.

- [ ] **Step 1: Refactor fake clock for independent tickers**

Make every `NewTicker` call return its own controllable channel so gameplay, countdown, and janitor ticks cannot consume one another's events.

- [ ] **Step 2: Write failing janitor/cap tests**

Assert tickRoom does not call cleanup, one janitor sweep removes all expired rooms, `Close` stops/waits for janitor, and a full store immediately reclaims expired rooms before returning 409.

- [ ] **Step 3: Implement the janitor lifecycle**

Start exactly one janitor during store construction. Snapshot registry entries under the store read lock, evaluate each room under its own lock, and delete-if-same outside the room lock. Keep cleanup calls only in the janitor and cap-pressure retry.

- [ ] **Step 4: Run TTL and race tests**

Run: `mise exec -- go test -race ./internal/rooms -run 'TestStore.*(Cleanup|Expired|Cap|Close|Janitor)' -count=20`

Expected: PASS and no leaked goroutine wait.

- [ ] **Step 5: Commit janitor cleanup**

```text
[SL-81] perf(rooms): TTL cleanup janitor 분리

- tick 경로의 전체 room 순회 제거
- cap pressure cleanup과 janitor 종료 보장
```

### Task 3: Add an asynchronous client outbox

**Files:**
- Modify: `internal/rooms/websocket.go`
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/websocket_test.go`

**Interfaces:**
- Produces: `clientConn` interface (`Read`, `Write`, `Ping`, `Close`), `clientSession`, `enqueueSnapshot([]byte)`, `enqueueControl([]byte) bool`, and `marshalMessage(any) ([]byte, error)`.

- [ ] **Step 1: Write fake-connection outbox tests**

Block one connection's Write while another receives snapshots; assert fast delivery continues. Fill the snapshot slot and assert only the newest payload remains. Fill the control queue and assert the client is closed/released rather than silently dropping a control event.

- [ ] **Step 2: Run tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'TestClientOutbox|TestSlowClient' -count=1`

Expected: current sequential delivery blocks the fast client.

- [ ] **Step 3: Implement writer ownership**

Use a reliable control channel of size 8, a snapshot channel of size 1 with replacement, a done channel, and `sync.Once` close. The writer checks control first, then selects control/snapshot, and writes with a fresh 5-second context.

- [ ] **Step 4: Marshal once and classify messages**

Marshal a normal tick snapshot once before fanout. Enqueue the final gameplay snapshot as reliable control immediately before per-player GameEnd payloads. Ready/starting/error messages also use reliable control.

- [ ] **Step 5: Run ordering/slow-client tests**

Run: `mise exec -- go test -race ./internal/rooms -run 'Test(ClientOutbox|SlowClient|WebSocket.*Order|GameEnd)' -count=20`

Expected: PASS with final snapshot preceding GameEnd.

- [ ] **Step 6: Commit asynchronous delivery**

```text
[SL-81] perf(websocket): client별 writer와 snapshot coalescing 추가

- 느린 client를 tick 경로에서 분리
- control event 순서와 단일 marshal 보장
```

### Task 4: Add heartbeat cleanup

**Files:**
- Modify: `internal/rooms/websocket.go`
- Modify: `internal/rooms/websocket_test.go`
- Modify: `api/asyncapi.yaml`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`

**Interfaces:**
- Produces: configurable `heartbeatInterval`, `heartbeatTimeout`, and a client heartbeat loop that invokes the same idempotent release path as read/write failure.

- [ ] **Step 1: Write responsive/silent peer tests**

Use a fake `clientConn.Ping` and fake ticker. A responsive peer survives repeated ticks; a blocked/error peer closes once, releases once, cancels a pre-start match, and starts disconnected TTL for a started room.

- [ ] **Step 2: Implement heartbeat**

Run heartbeat independently from snapshot fanout, bound each Ping with a 90-second context, and route timeout through `clientSession.close` plus `Store.releaseClient`. Do not create bot replacement or reconnect grace.

- [ ] **Step 3: Run race tests and benchmark**

Run: `mise exec -- go test -race ./internal/rooms -count=20`

Run: `mise exec -- go test ./internal/rooms -run '^$' -bench 'Benchmark(StoreTickRoomsParallel|BroadcastFanout|SnapshotMarshal)' -benchmem -count=5`

Expected: tests pass and benchmark results are recorded in the PR body without a mandatory numeric threshold.

- [ ] **Step 4: Update heartbeat/cleanup docs**

Document 30s/90s timing, snapshot coalescing, reliable control events, janitor timing, and cap-pressure cleanup.

- [ ] **Step 5: Run official validation and commit**

Run: `make ci && mise exec -- go test -race ./internal/rooms`

Expected: PASS.

```text
[SL-81] feat(websocket): heartbeat와 silent peer 정리 추가

- ping timeout을 기존 room disconnect 정책에 연결
- janitor와 delivery 동시성 계약 문서화
```
