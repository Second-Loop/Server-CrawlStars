# SL-81 Stack 3 Transport Security Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent player takeover, hide destructive debug REST routes by default, and bound matchmaking abuse.

**Architecture:** Generate opaque IDs and a per-player session secret from `crypto/rand`, store only a SHA-256 digest beside private room state, and authenticate before WebSocket upgrade. Configure debug gating and a standard-library token bucket at handler construction so public routing fails closed.

**Tech Stack:** Go crypto/rand, SHA-256, constant-time comparison, net/http, net/netip, OpenAPI, AsyncAPI.

## Global Constraints

- Room/player IDs use 16 random bytes with a stable `room_`/`player_` prefix; session tokens use 32 random bytes and Raw URL Base64.
- Public room/player DTOs never contain a token or digest.
- `webSocketPath` includes `?token=<secret>` so the current client can use the returned path directly.
- Debug REST is 404 by default; enabled routes require `Authorization: Bearer <DEBUG_API_TOKEN>` and return 401 otherwise.
- Join limiting defaults to 10 requests/minute with burst 4 and trusts `CF-Connecting-IP` only from configured proxy CIDRs.

---

### Task 1: Generate opaque IDs and player sessions

**Files:**
- Create: `internal/rooms/identity.go`
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/handler_test.go`

**Interfaces:**
- Produces: `randomValue(reader io.Reader, prefix string, size int) (string, error)`, private `playerSession{digest [sha256.Size]byte}`, and response `playerSessionResponse{Player, SessionToken, WebSocketPath}`.

- [ ] **Step 1: Write failing identity/secret tests**

Inject a deterministic `io.Reader` through `StoreConfig.Random`; assert opaque prefixes, distinct values, 32-byte decoded token, digest-only room storage, and absence of token fields in room list/detail JSON.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'Test(Store.*Opaque|.*SessionSecret)' -count=1`

Expected: sequential IDs and missing session fields fail assertions.

- [ ] **Step 3: Implement identity generation and DTO separation**

Use `io.ReadFull` and `base64.RawURLEncoding`. Add `sessions map[string]playerSession` to private room state. Make room/player creation return typed internal results carrying the one-time raw token while `roomResponse` continues to expose only public player data.

- [ ] **Step 4: Test generation failure**

Use a reader that returns an error and assert HTTP 500 `internal_error` without partial room/player insertion or secret text in the response.

- [ ] **Step 5: Commit identity/session issuance**

```text
[SL-81] feat(rooms): opaque player session 발급

- room과 player ID를 crypto/rand 기반으로 전환
- session secret과 공개 응답 DTO 분리
```

### Task 2: Authenticate WebSocket upgrade and reconnect

**Files:**
- Modify: `internal/rooms/identity.go`
- Modify: `internal/rooms/websocket.go`
- Modify: `internal/rooms/websocket_test.go`
- Modify: `cmd/server/main_test.go`

**Interfaces:**
- Produces: `room.authenticatePlayer(playerID, rawToken string) bool` using constant-time digest comparison and tokenized `webSocketPath`.

- [ ] **Step 1: Add failing upgrade tests**

Test missing/wrong token rejected before upgrade with 401, correct token accepted, a disconnected player reconnects with the same token, another player's token fails, and duplicate live connection still returns 409.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'TestWebSocket.*Token' -count=1`

Expected: missing/wrong tokens currently connect.

- [ ] **Step 3: Validate token before reservation/Accept**

Hash the query token, compare with `subtle.ConstantTimeCompare`, and only then reserve the client. Never include `r.URL.RawQuery` or token values in errors.

- [ ] **Step 4: Run WebSocket tests repeatedly**

Run: `mise exec -- go test ./internal/rooms -run 'TestWebSocket' -count=10`

Expected: PASS.

- [ ] **Step 5: Commit upgrade authentication**

```text
[SL-81] fix(websocket): player session 인증 강제

- upgrade 전 session token 검증 추가
- token 재연결과 오용 회귀 테스트 보강
```

### Task 3: Gate debug REST routes

**Files:**
- Modify: `internal/rooms/handler.go`
- Modify: `internal/rooms/handler_test.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`
- Modify: `scripts/deploy/crawl-stars-server.service`

**Interfaces:**
- Produces: `HandlerConfig{EnableDebugAPI bool, DebugAPIToken string, JoinLimiter *IPRateLimiter, TrustedProxyPrefixes []netip.Prefix}` and `HandlerWithConfig(store, config) (http.Handler, error)`.

- [ ] **Step 1: Add failing gate tests**

Assert all REST `/rooms` collection/detail/start/player routes return 404 by default, enabled-without-token config fails construction, missing/wrong Bearer returns 401, and correct Bearer preserves existing behavior. Confirm the authenticated WebSocket path remains available when debug REST is disabled.

- [ ] **Step 2: Implement fail-closed handler configuration**

Keep `Handler(store)` as the public-default wrapper. Wrap only debug REST handlers with enable/auth checks; do not wrap WebSocket handlers sharing the prefix. Parse `ENABLE_DEBUG_API` and `DEBUG_API_TOKEN` in `cmd/server`.

- [ ] **Step 3: Add systemd secret location**

Set `EnvironmentFile=-/etc/crawl-stars-server/environment` without committing a token value. Document file permissions as root-owned `0600` in stack documentation.

- [ ] **Step 4: Run handler/main tests**

Run: `mise exec -- go test ./internal/rooms ./cmd/server -run 'Test.*Debug' -count=10`

Expected: PASS.

- [ ] **Step 5: Commit debug protection**

```text
[SL-81] fix(http): debug API를 기본 비활성화

- enable flag와 Bearer token으로 debug REST 보호
- systemd secret environment 경계 추가
```

### Task 4: Add trusted client IP rate limiting

**Files:**
- Create: `internal/rooms/rate_limit.go`
- Create: `internal/rooms/rate_limit_test.go`
- Modify: `internal/rooms/handler.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`

**Interfaces:**
- Produces: `NewIPRateLimiter(ratePerMinute float64, burst int, now func() time.Time) *IPRateLimiter`, `Allow(ip netip.Addr) (bool, time.Duration)`, and `clientIP(request, trustedPrefixes) netip.Addr`.

- [ ] **Step 1: Write deterministic token-bucket tests**

Use a fake clock to cover burst exhaustion, fractional refill, Retry-After rounding, IP isolation, stale-entry cleanup, untrusted forwarded-header spoofing, and trusted `CF-Connecting-IP`.

- [ ] **Step 2: Run tests and verify RED**

Run: `mise exec -- go test ./internal/rooms -run 'Test(IPRateLimiter|ClientIP|MatchmakingRate)' -count=1`

Expected: compilation fails because rate limiter types do not exist.

- [ ] **Step 3: Implement the limiter and join response**

Use a mutex-protected map of token buckets. On rejection return HTTP 429 JSON code `rate_limited` and integer-seconds `Retry-After`. Parse environment overrides for per-minute rate, burst, and comma-separated `TRUSTED_PROXY_CIDRS`; reject invalid values at startup.

- [ ] **Step 4: Run race and full tests**

Run: `mise exec -- go test -race ./internal/rooms ./cmd/server -count=10`

Expected: PASS without race output.

- [ ] **Step 5: Commit abuse limiting**

```text
[SL-81] feat(matchmaking): IP 기반 요청 제한 추가

- trusted proxy 경계를 둔 token bucket 적용
- 429와 Retry-After 회귀 테스트 추가
```

### Task 5: Update contracts and validate stack 3

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `api/asyncapi.yaml`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/project-map.md`

**Interfaces:**
- Produces: documented session response, token query, debug Bearer security, 401/429 responses, and opaque-ID semantics.

- [ ] **Step 1: Update all contract surfaces**

Use redacted examples such as `token=<player-session-token>`; never paste a generated token. Mark debug endpoints disabled by default and document trusted proxy configuration.

- [ ] **Step 2: Build docs and run full validation**

Run: `make docs-build && make ci && mise exec -- go test -race ./internal/rooms ./cmd/server`

Expected: all commands pass.

- [ ] **Step 3: Commit contract documentation**

```text
[SL-81] docs(security): session과 debug 보호 계약 반영

- OpenAPI와 AsyncAPI에 인증 및 rate limit 추가
- 운영 secret과 trusted proxy 경계 문서화
```
