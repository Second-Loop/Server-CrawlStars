# SL-91 Matchmaking Bot Autofill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 첫 human matchmaking join 10초 뒤 mode 정원까지 빈 slot을 bot으로 원자적으로 채우고 기존 human-only Ready/start 흐름으로 연결합니다.

**Architecture:** Room이 one-shot `botFillTicker`와 `botFillStop`을 함께 소유합니다. Timer worker는 기존 `mutationMu -> matchmakingMu -> Store.mu -> room.mu` 순서로 human join과 직렬화하고, registry/ticker identity를 확인한 뒤 남은 slot 계산·ID 예약·append·matched 전이를 한 번에 수행합니다. 모든 stop/wait 작업은 core lock 밖에서 수행합니다.

**Tech Stack:** Go 1.24, `sync`, 기존 rooms clock/ticker abstraction, `log/slog`, fake clock, `httptest`, OpenAPI 3.1, AsyncAPI 3.0, Node.js docs validator.

## Global Constraints

- Base는 열린 PR #48의 `sl-90-basic-bot` head이며 SL-90의 `addBots`, bot input, human-only Ready quorum을 재사용합니다.
- Delay는 정확히 `10 * time.Second`이고 첫 matchmaking human의 `0 -> 1` 전이에서 한 번만 시작합니다.
- 정확히 10초의 human join과 timer 경쟁은 `matchmakingMu`를 먼저 획득한 transition이 이깁니다.
- Timer-first 이후 late join은 다른 room을 찾거나 만들고, active-room cap이면 기존 `409 room_cap_reached`를 유지합니다.
- Lock 순서는 `mutationMu -> matchmakingMu -> Store.mu -> room.mu`이며 core lock을 잡은 채 ticker stop, channel close, worker wait를 하지 않습니다.
- Bot ID 생성 실패는 부분 append 없이 rollback하고 `ERROR event=bot_fill_failed`를 한 번 기록합니다.
- Unmatched disconnect는 timer를 유지하고 matched/loading/starting disconnect는 기존 pre-start cancel을 유지합니다.
- Bot difficulty, disconnect replacement, reconnect grace, Ready timeout, production queue timeout, scheduler를 추가하지 않습니다.
- 각 task는 RED 확인 후 최소 구현, GREEN 확인, 독립 리뷰, commit 순서를 지킵니다.

---

### Task 1: Room-owned timer와 종료 자원 연결

**Files:**
- Modify: `internal/rooms/rooms.go`
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/cleanup.go`
- Create: `internal/rooms/bot_fill_test.go`
- Test: `internal/rooms/shutdown_test.go`

**Interfaces:**
- Consumes: `clock.NewTicker(time.Duration) ticker`, `Store.launchRoomWorker(func()) bool`, `roomResources.stop()`.
- Produces: `matchmakingBotFillDelay`, `room.botFillTicker`, `room.botFillStop`, `Store.armBotFillLocked`, `roomResources.detachBotFillLocked`, `Store.runBotFill`.

- [ ] **Step 1: Timer 시작·단일 소유·stop 회귀 테스트를 작성합니다**

```go
func TestMatchmakingBotFillTimerStartsOnHumanZeroToOneOnly(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	empty, err := store.createRoom()
	if err != nil {
		t.Fatal(err)
	}
	joined, err := store.joinMatchmaking(store.defaultGameMode())
	if err != nil {
		t.Fatal(err)
	}
	if joined.Room.ID != empty.ID {
		t.Fatalf("joined room=%q want existing empty room=%q", joined.Room.ID, empty.ID)
	}
	if got := clock.TickerCount(matchmakingBotFillDelay); got != 1 {
		t.Fatalf("bot-fill tickers=%d want 1", got)
	}
}

func TestMatchmakingBotFillTimerDoesNotResetAndStopsOnHumanFull(t *testing.T) {
	clock := newFakeClock()
	store := NewStoreWithClock(5, clock)
	t.Cleanup(store.Close)

	first, err := store.joinMatchmaking(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatal(err)
	}
	room := store.lookupRoom(first.Room.ID)
	room.mu.Lock()
	fillTicker := room.botFillTicker
	room.mu.Unlock()
	if _, err := store.joinMatchmaking(simulation.GameModeDuel1v1); err != nil {
		t.Fatal(err)
	}
	if got := fillTicker.(*fakeTicker).StopCount(); got != 1 {
		t.Fatalf("stop count=%d want 1", got)
	}
}
```

Shutdown test는 10초 전에 `Shutdown`을 호출하고 1초 timeout 안에 반환하며 captured bot-fill ticker의 `StopCount()==1`인지 확인합니다. Delete, clear, TTL cleanup, debug start도 같은 exact-once assertion을 사용합니다.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestMatchmakingBotFillTimer|TestShutdown.*BotFill' -count=1
```

Expected: `matchmakingBotFillDelay` 또는 `room.botFillTicker`가 없어 compile failure.

- [ ] **Step 3: Timer resource와 one-shot worker를 구현합니다**

`internal/rooms/rooms.go`:

```go
const matchmakingBotFillDelay = 10 * time.Second
```

`room`에 다음 필드를 추가합니다.

```go
botFillTicker ticker
botFillStop   chan struct{}
```

`internal/rooms/cleanup.go`:

```go
func (r *roomResources) detachBotFillLocked(room *room) {
	if room.botFillTicker != nil {
		r.tickers = append(r.tickers, room.botFillTicker)
		room.botFillTicker = nil
	}
	if room.botFillStop != nil {
		r.stops = append(r.stops, room.botFillStop)
		room.botFillStop = nil
	}
}
```

`removeRoomLocked`가 countdown/gameplay와 함께 `detachBotFillLocked(room)`을 호출하게 합니다. Debug `startRoom`도 room lock 아래에서 bot-fill resource를 detach하고, room/mutation lock을 모두 해제한 뒤 resource를 stop합니다.

`internal/rooms/store.go`에 다음 계약을 구현합니다.

```go
func (s *Store) armBotFillLocked(room *room) roomResources {
	var resources roomResources
	if room.removed || room.ending || room.Status != RoomStatusWaiting ||
		room.matchStatus != "" || len(room.Players) != 1 || room.botFillTicker != nil {
		return resources
	}
	fillTicker := s.clock.NewTicker(matchmakingBotFillDelay)
	fillStop := make(chan struct{})
	room.botFillTicker = fillTicker
	room.botFillStop = fillStop
	if !s.launchRoomWorker(func() { s.runBotFill(room, fillTicker, fillStop) }) {
		resources.detachBotFillLocked(room)
	}
	return resources
}

func (s *Store) runBotFill(room *room, fillTicker ticker, stop <-chan struct{}) {
	select {
	case <-fillTicker.C():
		s.fillMatchmakingBots(room, fillTicker)
	case <-stop:
	}
}
```

`fillMatchmakingBots`는 Task 2에서 완성하되 Task 1에서는 identity를 확인하고 ticker/stop을 detach한 뒤 no-op으로 종료하는 최소 구현으로 둡니다. `tryJoinMatchmakingRoom`과 `createMatchmakingRoom`은 human 수가 `0 -> 1`이면 arm하고, human full이면 bot-fill resource를 detach합니다. 두 함수가 반환한 `roomResources`는 `joinMatchmaking`이 `matchmakingMu`를 해제한 뒤 `stop()`합니다.

- [ ] **Step 4: GREEN과 기존 lifecycle 회귀를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestMatchmakingBotFillTimer|TestShutdown.*BotFill|TestAddBots' -count=1
rtk go test ./internal/rooms -run 'TestMatchmakingBotFillTimer' -count=20
```

Expected: PASS, ticker stop count는 모든 종료 경로에서 1.

- [ ] **Step 5: Task 1을 commit합니다**

```bash
rtk git add internal/rooms/rooms.go internal/rooms/store.go internal/rooms/cleanup.go internal/rooms/bot_fill_test.go internal/rooms/shutdown_test.go
rtk git commit -m "[SL-91] feat(rooms): own matchmaking bot fill timer" -m "- arm one-shot timer on first matchmaking human
- detach timer and stop channel across every room lifecycle
- verify exact-once cleanup and bounded shutdown"
```

---

### Task 2: 남은 slot의 원자적 bot 충원과 join 경계

**Files:**
- Modify: `internal/rooms/store.go`
- Test: `internal/rooms/bot_fill_test.go`
- Test: `internal/rooms/bot_participant_test.go`
- Test: `internal/rooms/handler_test.go`
- Test: `internal/rooms/logging_test.go`

**Interfaces:**
- Consumes: Task 1의 `runBotFill`, `roomResources.detachBotFillLocked`, SL-90의 `reserveBotIDsLocked`, `appendParticipantLocked`, `advanceMatchLoadingLocked`.
- Produces: `Store.fillMatchmakingBots(room *room, expectedTicker ticker)`, `Store.appendBotsLocked(room *room, count int)`.

- [ ] **Step 1: 10초 경계와 mode별 충원 실패 테스트를 작성합니다**

Table test는 `duel_1v1`, `solo`, `team` 각각에서 human 수 `1..capacity-1`을 만들고 다음을 확인합니다.

```go
clock.TickTicker(matchmakingBotFillDelay, timerOrdinal)
waitForMatchStatus(t, store, roomID, MatchStatusMatched)

room := store.lookupRoom(roomID)
room.mu.Lock()
players := append([]playerResponse(nil), room.Players...)
config := room.gameConfig
room.mu.Unlock()
if len(players) != config.MatchPlayerCount() {
	t.Fatalf("players=%d want=%d", len(players), config.MatchPlayerCount())
}
for index, player := range players {
	wantTeam, wantSlot, ok := config.TeamForPlayerIndex(index)
	if !ok || player.Team != string(wantTeam) || player.Slot != wantSlot {
		t.Fatalf("player[%d]=%+v want team=%q slot=%d", index, player, wantTeam, wantSlot)
	}
	if gotBot := index >= humanCount; player.IsBot != gotBot {
		t.Fatalf("player[%d].IsBot=%t want=%t", index, player.IsBot, gotBot)
	}
}
```

별도 barrier tests:

- Human-first: 두 번째 human join이 `matchmakingMu`를 획득한 뒤 credential entropy reader에서 멈춘 상태로 timer tick을 전달하고 release합니다. 기존 room에는 human 둘만 있어야 합니다.
- Timer-first: timer worker가 bot ID entropy reader에 진입한 뒤 late human join을 시작합니다. 기존 room은 bot-filled이고 human은 다른 room으로 가야 합니다.
- `maxActiveRooms=1`: timer-first late join handler 응답은 `409`와 `room_cap_reached`여야 합니다.
- Bot ID 두 번째 생성 실패: player/ID 수는 그대로이고 JSON log의 `level=ERROR`, `event=bot_fill_failed`, `room_id`가 정확히 한 번이어야 합니다.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestBotFill(AtTenSeconds|UsesEveryModeAssignment|HumanFirst|TimerFirst|RoomCap|LogsEntropyFailure)' -count=1
```

Expected: 10초 tick 뒤 room이 채워지지 않아 assertion failure.

- [ ] **Step 3: 원자적 fill transition을 구현합니다**

`Store.mu`와 `room.mu`를 이미 보유한 helper는 ID 생성 실패 시 `reserveBotIDsLocked`의 rollback을 그대로 사용합니다.

```go
func (s *Store) appendBotsLocked(room *room, count int) ([]playerResponse, error) {
	ids, err := s.reserveBotIDsLocked(count)
	if err != nil {
		return nil, err
	}
	bots := make([]playerResponse, 0, count)
	for _, id := range ids {
		bots = append(bots, s.appendParticipantLocked(room, id, true))
	}
	return bots, nil
}
```

`fillMatchmakingBots`는 아래 순서를 그대로 구현합니다.

```go
func (s *Store) fillMatchmakingBots(room *room, expectedTicker ticker) {
	if room == nil || !s.beginMutation() {
		return
	}

	s.matchmakingMu.Lock()
	var resources roomResources
	var deliveries []webSocketDelivery
	var fillErr error

	s.mu.Lock()
	if s.closed || s.rooms[room.ID] != room {
		s.mu.Unlock()
		s.matchmakingMu.Unlock()
		s.endMutation()
		return
	}
	room.mu.Lock()
	if room.removed || room.ending || room.Status != RoomStatusWaiting ||
		room.matchStatus != "" || room.botFillTicker != expectedTicker {
		room.mu.Unlock()
		s.mu.Unlock()
		s.matchmakingMu.Unlock()
		s.endMutation()
		return
	}
	resources.detachBotFillLocked(room)
	remaining := room.gameConfig.MatchPlayerCount() - len(room.Players)
	if remaining > 0 {
		_, fillErr = s.appendBotsLocked(room, remaining)
	}
	if fillErr == nil {
		s.markRoomMatchedIfFullLocked(room)
	}
	s.mu.Unlock()
	if fillErr == nil {
		deliveries = s.advanceMatchLoadingLocked(room)
	}
	failedSessions := tryEnqueueWebSocketDeliveries(deliveries)
	room.mu.Unlock()
	s.matchmakingMu.Unlock()
	s.endMutation()

	resources.stop()
	closeClientSessions(failedSessions, "control delivery failed")
	if fillErr != nil {
		s.logger.Error("bot fill failed", "event", "bot_fill_failed", "room_id", room.ID, "error", fillErr)
	}
}
```

기존 `addBots`도 `appendBotsLocked`를 사용하되 precheck, registry pointer 재검증, `Store.mu` 해제 뒤 Ready delivery라는 기존 의미는 바꾸지 않습니다. Manual `addBots`가 정원을 완성한 경우에는 bot-fill resource를 detach하고 모든 core lock 밖에서 stop하며, 일부 slot만 추가한 경우에는 기존 deadline을 유지합니다.

- [ ] **Step 4: GREEN, repeat, race를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestBotFill|TestAddBots' -count=1
rtk go test ./internal/rooms -run 'TestBotFill(HumanFirst|TimerFirst)' -count=100
rtk go test -race ./internal/rooms -run 'TestBotFill|TestAddBots'
```

Expected: PASS; `-race` 경고 없음.

- [ ] **Step 5: Task 2를 commit합니다**

```bash
rtk git add internal/rooms/store.go internal/rooms/bot_fill_test.go internal/rooms/bot_participant_test.go internal/rooms/handler_test.go internal/rooms/logging_test.go
rtk git commit -m "[SL-91] feat(matchmaking): fill empty slots with bots after 10 seconds" -m "- serialize timer fill with human matchmaking joins
- preserve mode capacity, team assignment, and room-cap errors
- rollback and log bot identity failures atomically"
```

---

### Task 3: Stale worker, disconnect, Ready/start 회귀 강화

**Files:**
- Modify: `internal/rooms/store.go` only if regression tests expose a missing guard
- Modify: `internal/rooms/websocket.go` only if existing disconnect transition needs timer resource handoff
- Test: `internal/rooms/bot_fill_test.go`
- Test: `internal/rooms/websocket_test.go`
- Test: `internal/rooms/shutdown_test.go`

**Interfaces:**
- Consumes: Tasks 1–2의 ticker identity guard와 기존 `hasPreStartMatch`, `allMatchClientsAttached`, `allMatchPlayersReady`.
- Produces: deterministic stale/replacement, disconnect/fill, Ready/countdown/start guarantees.

- [ ] **Step 1: Lifecycle와 stale tick 회귀 테스트를 작성합니다**

다음 named tests를 추가합니다.

```go
func TestBotFillIgnoresStoppedBufferedTick(t *testing.T)
func TestBotFillIgnoresSameIDReplacementRoom(t *testing.T)
func TestBotFillTimersAreIndependentAcrossRooms(t *testing.T)
func TestBotFillUnmatchedDisconnectKeepsTimerAndCredentials(t *testing.T)
func TestBotFillMatchedDisconnectCancelsRoom(t *testing.T)
func TestBotFillDisconnectAndTimerRaceUsesRoomLockWinner(t *testing.T)
func TestBotFillReadyQuorumUsesHumansAndStartsOnce(t *testing.T)
func TestBotFillShutdownJoinsWorkerWaitingForMatchmakingLock(t *testing.T)
```

Stale replacement test는 original room의 ticker를 캡처하고 registry를 같은 ID의 replacement pointer로 교체한 뒤 captured ticker에 buffered tick을 보내며, 양쪽 room player 수와 `playerIDs` 수가 변하지 않았는지 확인합니다. Ready test는 bot fill 전·후 attach 순서를 모두 실행하고 human에게만 Ready를 보낸 뒤 human ACK만으로 countdown/gameplay ticker가 각각 하나 생성되는지 확인합니다.

- [ ] **Step 2: RED 또는 기존 GREEN을 기록합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestBotFill(Ignores|Timers|Unmatched|Matched|Disconnect|Ready|Shutdown)' -count=1
```

Expected: 새 테스트가 현재 구현의 빠진 guard를 드러내면 FAIL하고, 이미 Task 2가 만족한 항목은 PASS합니다. 모든 named test가 실제 assertion을 실행해야 합니다.

- [ ] **Step 3: 실패한 경계만 최소 수정합니다**

- Stale worker는 `s.rooms[room.ID] == room`과 `room.botFillTicker == expectedTicker`를 ID 예약 전에 모두 확인합니다.
- Cleanup은 room lock 아래 resource detach만 하고, `resources.stop()`과 worker wait는 모든 core lock 해제 뒤 실행합니다.
- Unmatched disconnect에서는 timer fields를 건드리지 않습니다.
- Timer가 matched 전이를 먼저 완료한 경우 기존 `hasPreStartMatch()` cancel path가 `removeRoomLocked`를 통해 bot-fill/countdown/gameplay resource를 함께 회수합니다.
- Ready/countdown/start에서는 새 quorum이나 새 loop를 만들지 않고 SL-90 helper를 그대로 호출합니다.

- [ ] **Step 4: GREEN과 stress 검증을 실행합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'TestBotFill(Ignores|Timers|Unmatched|Matched|Disconnect|Ready|Shutdown)' -count=100
rtk go test -race ./internal/rooms -run 'TestBotFill|TestWebSocketCloseBeforeMatchStart'
```

Expected: 100회 PASS, race warning과 1초 timeout 없음.

- [ ] **Step 5: Task 3을 commit합니다**

```bash
rtk git add internal/rooms/store.go internal/rooms/websocket.go internal/rooms/bot_fill_test.go internal/rooms/websocket_test.go internal/rooms/shutdown_test.go
rtk git commit -m "[SL-91] test(rooms): harden bot fill lifecycle races" -m "- reject stale timer and replacement-room work
- preserve unmatched reconnect and matched cancellation
- prove human-only Ready and once-only start under races"
```

---

### Task 4: API 계약, ai-docs, 최종 검증

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `api/asyncapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `internal/docs/docs_test.go`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/workflow.md`

**Interfaces:**
- Consumes: Tasks 1–3의 최종 runtime behavior.
- Produces: SL-91 완료 상태와 10초 matchmaking contract를 고정하는 served docs와 validator.

- [ ] **Step 1: Served-contract와 source validator를 RED로 갱신합니다**

`internal/docs/docs_test.go`에 다음 marker를 요구합니다.

```go
for _, marker := range []string{
	"첫 human matchmaking join부터 10초",
	"남은 participant slot을 bot으로 충원",
	"active-room cap이면 room_cap_reached",
	"human session만 Ready ACK",
} {
	assertBodyContains(t, asyncAPI, marker)
}
```

`docs-ui/scripts/validate.mjs`에서는 기존 “SL-91은 아직 구현하지 않음” marker를 제거하고 동일한 완료 marker를 OpenAPI/AsyncAPI에서 확인합니다.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/docs -run 'TestHandlerServes.*BotFill' -count=1
rtk node docs-ui/scripts/validate.mjs
```

Expected: 기존 미구현 문구와 누락된 10초 contract 때문에 FAIL.

- [ ] **Step 3: 계약과 문서를 실제 동작에 맞춥니다**

- OpenAPI matchmaking join 설명에 timer 시작점, mode capacity, late join/room cap 결과를 적습니다.
- AsyncAPI Ready 설명에 bot-filled full participant list와 human-only attach/ACK를 적습니다.
- `ai-docs/`에서 SL-91을 “미구현/다음 작업” 목록에서 제거하고 room-owned timer, lock/cleanup 책임, failure/no-retry 경계를 기록합니다.
- ADR에는 중앙 scheduler 대신 room-owned one-shot ticker를 선택한 이유와 first-lock-wins 정책을 기록합니다.
- ClientTick/ACK는 SL-94 범위로 남깁니다.

- [ ] **Step 4: 문서와 전체 CI를 검증합니다**

Run:

```bash
rtk go test ./internal/docs -count=1
rtk node docs-ui/scripts/validate.mjs
rtk make docs-build
rtk go test ./internal/rooms -run 'TestBotFill' -count=100
rtk go test -race ./internal/rooms
rtk make ci
rtk git diff --check
```

Expected: 모두 PASS, diff-check 출력 없음.

- [ ] **Step 5: Task 4를 commit합니다**

```bash
rtk git add api/openapi.yaml api/asyncapi.yaml docs-ui/scripts/validate.mjs internal/docs/docs_test.go ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/decisions.md ai-docs/project-map.md ai-docs/workflow.md
rtk git commit -m "[SL-91] docs(api): document timed matchmaking bot fill" -m "- publish 10-second fill and late-join behavior
- preserve human-only Ready and room-cap contracts
- refresh architecture, workflow, and validation markers"
```

---

## Final SL-91 Review Gate

- [ ] `git diff sl-90-basic-bot...HEAD`가 SL-91 runtime/docs와 공통 planning artifact만 포함하는지 확인합니다.
- [ ] Spec compliance reviewer와 code quality reviewer의 Critical/Important 지적을 모두 해결합니다.
- [ ] `rtk make ci`, targeted `-count=100`, relevant `-race`를 fresh run으로 다시 실행합니다.
- [ ] Linear SL-91에 결정·validation·PR link를 기록하고 상태를 `In Review`로 옮깁니다.
- [ ] Branch를 push하고 base `sl-90-basic-bot`인 ready-for-review PR을 생성합니다.
