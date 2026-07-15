# SL-81 Stack 5 Runtime Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Shut the server down cleanly and expose useful structured logs and private Prometheus metrics.

**Architecture:** `cmd/server` constructs an application that owns the room store plus public and metrics HTTP servers. Room lifecycle hooks feed an injected logger/observer; the public mux stays unchanged while a separate loopback listener serves the private registry.

**Tech Stack:** Go `http.Server`, `signal.NotifyContext`, `log/slog`, Prometheus client_golang, systemd.

## Global Constraints

- Public HTTP uses `ReadHeaderTimeout=5s` and `IdleTimeout=60s`.
- Shutdown grace is 10 seconds and must fit systemd `TimeoutStopSec=15s`.
- Store close explicitly closes hijacked WebSockets; `http.Server.Shutdown` alone is insufficient.
- JSON logs include event, roomID/playerID where applicable, and error; never token/query/Authorization values.
- Metrics bind to `127.0.0.1:9090` by default and `/metrics` is not registered on the public mux.

---

### Task 1: Inject structured lifecycle logging

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/websocket.go`
- Create: `internal/rooms/logging_test.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Produces: `StoreConfig.Logger *slog.Logger`, a discard default logger, and stable event names `room_created`, `room_started`, `room_ended`, `room_expired`, `websocket_connected`, `websocket_disconnected`, `websocket_auth_rejected`, `websocket_io_error`.

- [ ] **Step 1: Write failing JSON log tests**

Capture a `slog.NewJSONHandler` buffer and assert event/roomID/playerID fields for lifecycle actions. Scan the buffer to ensure a known token, digest, `Authorization`, and `?token=` never appear.

- [ ] **Step 2: Run tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'TestStructuredLog' -count=1`

Expected: no lifecycle records are emitted.

- [ ] **Step 3: Add logger injection and event calls**

Emit logs after committed state transitions and on auth/read/write errors. Use `slog.String` fields and log only path IDs, never request URLs.

- [ ] **Step 4: Run room tests**

Run: `mise exec -- go test -race ./internal/rooms -count=10`

Expected: PASS.

- [ ] **Step 5: Commit structured logs**

```text
[SL-81] feat(observability): room lifecycle 구조화 로그 추가

- slog 기반 room과 WebSocket event 기록
- secret 비노출 회귀 테스트 추가
```

### Task 2: Add a private Prometheus registry

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/observability/metrics.go`
- Create: `internal/observability/metrics_test.go`
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/websocket.go`

**Interfaces:**
- Produces: rooms `Observer` interface with `SetActiveRooms(int)`, `SetConnectedClients(int)`, `ObserveTick(time.Duration)`; `observability.Metrics` implements it and exposes `Handler() http.Handler`.

- [ ] **Step 1: Add the pinned dependency**

Run: `mise exec -- go get github.com/prometheus/client_golang/prometheus@v1.23.2`

Expected: `go.mod` and `go.sum` contain the Prometheus client and transitive modules.

- [ ] **Step 2: Write failing registry tests**

Assert a fresh private registry contains `crawlstars_active_rooms`, `crawlstars_connected_clients`, and `crawlstars_tick_duration_seconds`; update the observer and verify gathered values/histogram count.

- [ ] **Step 3: Implement metrics and no-op observer**

Use `prometheus.NewRegistry`, explicit Gauge/Gauge/Histogram collectors, and no global registration. Default StoreConfig to a no-op observer so unit tests remain isolated.

- [ ] **Step 4: Instrument exact transitions**

Set gauges after room/client insertion/removal and observe duration around `state.Step`; never use roomID/playerID labels.

- [ ] **Step 5: Run metrics and race tests**

Run: `mise exec -- go test -race ./internal/observability ./internal/rooms -count=10`

Expected: PASS with no duplicate-registration panic.

- [ ] **Step 6: Commit metrics**

```text
[SL-81] feat(observability): private Prometheus metric 추가

- active room과 client gauge 및 tick histogram 추가
- private registry로 테스트와 runtime 등록 분리
```

### Task 3: Own server lifecycle and graceful shutdown

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`

**Interfaces:**
- Produces: `runtimeConfig`, `application{store, publicServer, metricsServer, logger}`, `newApplication(config)`, and `(*application).Run(ctx) error`.

- [ ] **Step 1: Write failing configuration/server tests**

Assert public timeout fields, metrics address default, public `/metrics` 404, private handler 200, `http.ErrServerClosed` treated as nil, and invalid debug/rate/proxy config rejected before listeners start.

- [ ] **Step 2: Refactor construction**

Make `newApplication` own one Store, a public `http.Server`, and a metrics `http.Server`. Keep `newMux` as a testable public-handler helper without metrics.

- [ ] **Step 3: Add coordinated Run/Shutdown**

Start both servers, wait for context cancellation or a non-`ErrServerClosed` serve error, call `store.Close()` to close WebSockets, then call both `Shutdown` methods with one 10-second context. Return zero/nil on signal-driven shutdown.

- [ ] **Step 4: Wire process signals**

Use `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` in `main` and create a JSON slog logger on stdout.

- [ ] **Step 5: Run main and room tests**

Run: `mise exec -- go test -race ./cmd/server ./internal/rooms -count=10`

Expected: PASS.

- [ ] **Step 6: Commit graceful lifecycle**

```text
[SL-81] fix(server): graceful shutdown과 HTTP timeout 연결

- SIGTERM에서 store와 HTTP server 순차 종료
- public과 private metrics listener 분리
```

### Task 4: Verify shutdown and document operations

**Files:**
- Modify: `scripts/deploy/crawl-stars-server.service`
- Modify: `ai-docs/deployment.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`
- Modify: `api/openapi.yaml` only to state that public `/metrics` is not an API route; do not add a path.

**Interfaces:**
- Produces: documented `METRICS_ADDR=127.0.0.1:9090` and shutdown verification.

- [ ] **Step 1: Add an integration shutdown test**

Start the application on ephemeral listeners, hold an in-flight HTTP request and WebSocket, cancel context, then assert the request completes, WebSocket receives normal close reason `server shutting down`, and Run returns within 15 seconds.

- [ ] **Step 2: Update systemd/runtime docs**

Add `Environment=METRICS_ADDR=127.0.0.1:9090`, keep `TimeoutStopSec=15s`, and document localhost Prometheus scraping plus public `/metrics` rejection.

- [ ] **Step 3: Run final stack validation**

Run: `make ci && mise exec -- go test -race ./... -count=10`

Expected: PASS; no public metrics route and no race report.

- [ ] **Step 4: Commit operational docs**

```text
[SL-81] docs(operations): shutdown과 metric 운영 절차 추가

- systemd 종료 시간과 private scrape 경로 문서화
- 구조화 log와 timeout 검증 절차 기록
```
