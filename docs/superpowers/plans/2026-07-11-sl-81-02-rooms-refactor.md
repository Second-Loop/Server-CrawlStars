# SL-81 Stack 2 Rooms Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `internal/rooms/rooms.go` by responsibility and replace string/routing hazards without changing HTTP or WebSocket behavior.

**Architecture:** Characterize the existing wire contract first, then move code into focused files. A Go 1.22+ `ServeMux` owns route matching while explicit fallback and HEAD handlers preserve the repository's JSON 404/405 behavior.

**Tech Stack:** Go 1.25 `net/http`, sentinel errors, `errors.Is`, httptest, nhooyr WebSocket tests.

## Global Constraints

- This stack is behavior-preserving; no session token, random ID, debug gate, rate limit, lock redesign, or writer queue is added here.
- Preserve JSON bodies/statuses for unknown route, unknown room/player, unsupported method, `/rooms/`, and explicit HEAD requests.
- Keep exported constructors and `rooms.Handler(store)` usable by current callers.
- Every mechanical move is followed by the focused room tests before additional refactoring.

---

### Task 1: Characterize routing and wire errors

**Files:**
- Modify: `internal/rooms/handler_test.go`
- Modify: `internal/rooms/websocket_test.go`

**Interfaces:**
- Consumes: current `rooms.Handler` behavior.
- Produces: table tests fixing method/path/status/content-type/error-code behavior before routing changes.

- [ ] **Step 1: Add route characterization cases**

Cover GET/POST/DELETE `/rooms`, `/rooms/`, unknown nested routes, HEAD on room list/detail/WebSocket paths, PUT on known paths, trailing slash, and percent-encoded IDs. Assert `application/json`, status, and error code.

- [ ] **Step 2: Run tests against the old handler**

Run: `mise exec -- go test ./internal/rooms -run 'TestHandler(Route|Method|Trailing|Head)' -count=1`

Expected: PASS; these tests capture the pre-refactor contract.

- [ ] **Step 3: Commit characterization tests**

```text
[SL-81] test(rooms): REST routing 계약 고정

- method와 trailing slash 응답 회귀 테스트 추가
- WebSocket upgrade 전 JSON 오류 계약 고정
```

### Task 2: Introduce typed room errors

**Files:**
- Create: `internal/rooms/errors.go`
- Modify: `internal/rooms/handler_test.go`
- Modify: `internal/rooms/rooms.go`

**Interfaces:**
- Produces: `ErrRoomNotFound`, `ErrPlayerNotFound`, `ErrPlayerAlreadyConnected`, `ErrRoomFull`, `ErrRoomHasNoPlayers`, and `ErrActiveRoomCapReached` package sentinels.

- [ ] **Step 1: Add failing `errors.Is` unit tests**

Assert store operations return each sentinel and no handler uses `err.Error()` branching.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'TestStore.*TypedError' -count=1`

Expected: compilation fails because sentinel errors do not exist.

- [ ] **Step 3: Add sentinels and replace string comparisons**

Define errors with `errors.New`, return them directly, and map them to existing status/error codes with `errors.Is` in the handler. Remove `errString` after its last use.

- [ ] **Step 4: Run all room tests**

Run: `mise exec -- go test ./internal/rooms -count=1`

Expected: PASS with identical response bodies.

- [ ] **Step 5: Commit typed errors**

```text
[SL-81] refactor(rooms): 문자열 오류 분기 제거

- room lifecycle 오류를 sentinel error로 전환
- HTTP 오류 매핑을 errors.Is 기준으로 정리
```

### Task 3: Split rooms.go into focused files

**Files:**
- Create: `internal/rooms/handler.go`
- Create: `internal/rooms/store.go`
- Create: `internal/rooms/websocket.go`
- Create: `internal/rooms/messages.go`
- Create: `internal/rooms/cleanup.go`
- Modify: `internal/rooms/rooms.go`
- Modify: `internal/rooms/websocket_test.go`

**Interfaces:**
- `handler.go`: `Handler`, route handlers, JSON response helpers.
- `store.go`: store/room types, constructors, room/player/match lifecycle.
- `websocket.go`: upgrade/read/release/delivery logic.
- `messages.go`: REST and WebSocket DTOs/converters.
- `cleanup.go`: TTL policy, `roomResources`, `Store.Close`.
- `rooms.go`: package constants and clock/ticker adapters only, or delete it when empty.

- [ ] **Step 1: Move one responsibility at a time**

Use `apply_patch` for each move and preserve function/type names. After each destination file, run `gofmt` and `mise exec -- go test ./internal/rooms -count=1`.

Expected: every intermediate move passes before the next move.

- [ ] **Step 2: Move the test-only snapshot decoder**

Delete production `snapshotMessage` only after adding the equivalent unexported decoder type to `websocket_test.go`; keep `roomSnapshotMessage` in `messages.go`.

- [ ] **Step 3: Replace `itoa`**

Use `strconv.Itoa` at both ID sequence call sites and delete the custom helper.

- [ ] **Step 4: Verify package structure**

Run: `wc -l internal/rooms/*.go`

Expected: no production file exceeds 500 lines and responsibilities match the interface list above.

- [ ] **Step 5: Commit the file split**

```text
[SL-81] refactor(rooms): handler와 lifecycle 책임 분리

- rooms.go 책임을 handler store websocket message cleanup으로 분리
- test 전용 decoder와 표준 변환 함수 정리
```

### Task 4: Move routing to ServeMux patterns

**Files:**
- Modify: `internal/rooms/handler.go`
- Modify: `internal/rooms/handler_test.go`
- Modify: `internal/rooms/websocket_test.go`

**Interfaces:**
- Produces: internal `newRouter(store *Store) *http.ServeMux` and explicit JSON fallback handlers.

- [ ] **Step 1: Add a failing route-registration test**

Assert `request.Pattern` is populated for known routes and path values come from `r.PathValue("roomID")`/`r.PathValue("playerID")`, while characterization cases stay unchanged.

- [ ] **Step 2: Implement method/path patterns**

Register exact patterns for matchmaking, room collection, room detail, start, player creation, and WebSocket paths. Register explicit `HEAD` and path-only fallbacks so Go's implicit HEAD/plain-text 405 behavior cannot replace the existing JSON contract. Register `/rooms/{$}` and `/` fallbacks to avoid automatic redirect surprises.

- [ ] **Step 3: Run route and WebSocket tests**

Run: `mise exec -- go test ./internal/rooms -run 'Test(Handler|WebSocket)' -count=10`

Expected: PASS on all 10 runs.

- [ ] **Step 4: Run official validation**

Run: `make ci`

Expected: PASS.

- [ ] **Step 5: Commit ServeMux routing**

```text
[SL-81] refactor(rooms): ServeMux pattern routing 적용

- 수동 path split을 method pattern과 PathValue로 교체
- 기존 JSON 404와 405 응답 유지
```
