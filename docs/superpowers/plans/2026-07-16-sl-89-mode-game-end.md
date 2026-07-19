# SL-89 Solo/Team Elimination and GameEnd Implementation Plan v3

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` task-by-task. Every task below must be green and committed before the next task starts.

**Goal:** Solo 중간 탈락, Solo 마지막 생존/동시 사망, Team 전멸/동시 전멸을 적용하고, normal GameEnd에서는 terminal client의 `snapshot -> GameEnd -> closeDone` 뒤에만 room registry와 player ID를 정리해요.

**Architecture:** Task 1은 pure helper만 추가하고 production calculator는 아직 Solo/Team에 연결하지 않아요. Task 2는 immutable result ledger와 모든 mutation guard를 추가해요. Task 3에서 mode calculator 활성화, terminal-aware tick, close barrier를 하나의 atomic green commit으로 연결해 early whole-room cleanup이 중간 commit에 노출되지 않게 해요. Shutdown은 forced-teardown 예외로 분리하고, janitor/debug delete는 normal GameEnd barrier를 우회하지 못하게 해요.

**Tech Stack:** Go 1.25, `nhooyr.io/websocket`, table-driven tests, fake/real WebSocket integration tests, AsyncAPI 3.0, `make ci`.

## Fixed Base

- Exact base: `9976d2c9c0534e146a27dc0677adf0aa2add8c71`.
- SL-86 mode catalog, SL-87 6-player Ready, SL-88 projectile eligibility/input ordering이 이 commit에 포함돼요.
- 이 plan에는 치환용 SHA placeholder가 없어요.

## Global Constraints

- `gameEndCalculator`, `room.calculateGameEnd`, room config injection seam을 보존해요.
- Final method는 `room.calculateGameEndResults(snapshot)`, ledger는 `finalizedGameEndResults map[string]gameEndResult`예요.
- Task 1에서는 current `calculateGameEndResults` production switch와 `tickRoomState` caller를 바꾸지 않아요.
- Solo initial empty/one-alive snapshot은 result 없음, terminal false예요.
- Solo intermediate loser는 Lose 한 번만 받고 해당 connection만 닫혀요. Survivor ticker는 계속돼요.
- 이전 Solo Lose는 terminal all-dead snapshot에서도 불변이고, 아직 result가 없던 player만 Draw를 새로 받아요.
- Team partial death는 result/close 없음, one-team elimination은 eliminated team Lose와 other team Win, same-tick both-team elimination은 all Draw예요.
- Duel Win/Lose/Draw wire shape를 유지해요.
- Terminal decision은 room lock 아래 `ending=true`와 gameplay detach를 고정하고, unlock 직후 `terminalResources.stop()`을 observer/marshal/enqueue보다 먼저 호출해요.
- Normal GameEnd만 `closeDone -> registry delete -> active-room observer -> player ID release -> room_ended -> gameEndCleanupDone`을 보장해요.
- `gameEndCleanupDone`은 마지막 정상 단계에서 직접 닫아요. `defer`로 닫지 않아요. Observer/logger callback panic은 기존처럼 propagate하고 completion channel은 열린 채로 남겨 false success를 만들지 않아요.
- Hard-TTL janitor와 debug clear/delete는 `ending` room을 제거하지 않아요.
- `Store.Shutdown`은 명시적 forced-teardown 예외예요. Registry/ID detach가 transport `closeDone`보다 빠를 수 있지만, shutdown은 force-close, room worker, writer, heartbeat, active-session join을 완료한 뒤 반환해요. Shutdown takeover는 `gameEndCleanupDone`을 닫거나 `room_ended`를 기록하지 않아요.
- `ending` 또는 finalized player는 add/start/matchmaking/reserve/attach/input/tick 경계에서 거부해요.
- OpenAPI, config JSON, simulation DTO/projectile code는 바꾸지 않아요.
- Generated `internal/docs/api/`와 `internal/docs/static/`은 stage하지 않아요.

---

### Task 0: Fixed Base Preflight

**Files:** Verify only.

- [ ] **Step 1: exact base ancestry와 clean tree를 확인**

```bash
test "$(git merge-base HEAD 9976d2c9c0534e146a27dc0677adf0aa2add8c71)" = "9976d2c9c0534e146a27dc0677adf0aa2add8c71"
git status --short
git diff --quiet 9976d2c9c0534e146a27dc0677adf0aa2add8c71 -- internal/rooms/game_end.go internal/rooms/game_end_test.go
```

Expected: 세 command 모두 exit 0, status output 없음.

- [ ] **Step 2: cumulative symbols와 SL-88 regression을 확인**

```bash
rg -n 'GameModeDuel1v1|GameModeSolo|GameModeTeam' internal/simulation/game_config.go
rg -n 'canProjectileHit|orderedInputsByPlayerID|playerTeam' internal/simulation/simulation.go
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestStep(ProjectileCollisionMatrix|NormalizesInputOrderThroughCollision)|TestValidateGameConfigRejectsUnsupportedTeamBehavior' -count=1
```

Expected: symbols 존재, test PASS. Task 0은 commit하지 않아요.

---

### Task 1: Pure Mode Result와 Terminal Helpers만 추가

**Files:**

- Modify: `internal/rooms/game_end.go`
- Modify: `internal/rooms/game_end_test.go`
- Verify unchanged: `internal/rooms/websocket.go`

**Interfaces:**

- Produces: `soloGameEndResults`, `teamGameEndResults`, `configuredTeamEliminations`, `livePlayerCount`, `shouldEndGame`.
- Preserves for now: current package `calculateGameEndResults` switch and `room.gameEndResults` runtime path.

- [ ] **Step 1: exact test helpers를 `game_end_test.go`에 추가**

```go
func selectedGameEndMode(t *testing.T, modeID string) simulation.GameConfig {
	t.Helper()
	config, err := simulation.StaticGameConfig().SelectMode(modeID)
	if err != nil { t.Fatalf("select mode %q: %v", modeID, err) }
	return config
}

func playersWithDeaths(players []simulation.PlayerData, deadIDs ...simulation.PlayerID) []simulation.PlayerData {
	dead := make(map[simulation.PlayerID]bool, len(deadIDs))
	for _, id := range deadIDs { dead[id] = true }
	result := append([]simulation.PlayerData(nil), players...)
	for i := range result {
		if dead[result[i].ID] { result[i].IsDead = true; result[i].HP = 0 }
	}
	return result
}

func assertGameEndDecision(t *testing.T, config simulation.GameConfig, snapshot simulation.Snapshot, want map[string]gameEndResult, terminal bool) {
	t.Helper()
	var got map[string]gameEndResult
	switch config.SelectedMode.ID {
	case simulation.GameModeSolo:
		got = soloGameEndResults(snapshot.Players)
	case simulation.GameModeTeam:
		got = teamGameEndResults(config.SelectedMode, snapshot.Players)
	case simulation.GameModeDuel1v1:
		got = duelGameEndResults(snapshot.Players)
	default:
		got = playerSurvivalGameEndResults(snapshot.Players)
	}
	if !reflect.DeepEqual(got, want) || shouldEndGame(config, snapshot) != terminal {
		t.Fatalf("got results=%+v terminal=%t, want results=%+v terminal=%t", got, shouldEndGame(config, snapshot), want, terminal)
	}
}
```

- [ ] **Step 2: RED table tests를 추가**

Required cases:

- `TestSoloGameEndDecisionEdges`: empty, initial one alive, six alive, one intermediate death, last survivor, first observed all-dead.
- `TestTeamGameEndRules`: partial red death, red eliminated, both teams eliminated.
- `TestDuelGameEndRulesRemainCompatible`: no death, one death, both dead.
- `TestCustomModeFallsBackToPlayerSurvival`: three-player custom FFA with one dead returns two Win + one Lose and terminal true.

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'Test(Solo|Team|Duel)GameEnd|TestCustomModeFallsBack' -count=1
```

Expected RED: undefined pure helper/`shouldEndGame`; test-helper undefined failure는 없어야 해요.

- [ ] **Step 3: pure helpers를 구현**

- Solo: no death -> nil; alive >1 -> dead players Lose only; alive=1 after a death -> dead Lose + survivor Win; alive=0 -> current snapshot players Draw.
- Team: configured team별 live count를 계산하고 no elimination -> nil; all configured teams eliminated -> all Draw; otherwise eliminated team Lose, non-eliminated team 전원 Win.
- `shouldEndGame`: Duel/custom은 current survival result 존재 여부, Solo는 `alive < len(players) && alive <= 1`, Team은 configured elimination 하나 이상.
- 이 step에서는 `calculateGameEndResults`의 Solo/Team case를 추가하지 않아요.

- [ ] **Step 4: GREEN과 non-activation invariant를 확인**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'Test(Solo|Team|Duel)GameEnd|TestCustomModeFallsBack' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
git diff --exit-code -- internal/rooms/websocket.go
```

Expected: PASS, websocket runtime diff 없음.

- [ ] **Step 5: commit**

```bash
git add internal/rooms/game_end.go internal/rooms/game_end_test.go
git commit -m "[SL-89] feat(rooms): 모드별 종료 판정 helper 추가" -m "- production tick 활성화 전 pure Solo Team 결과를 고정해요" -m "- custom fallback과 0 1 player 경계를 검증해요"
```

---

### Task 2: Immutable Ledger와 모든 Mutation Guard

**Files:** `internal/rooms/store.go`, `internal/rooms/game_end.go`, `internal/rooms/cleanup.go`, `internal/rooms/websocket.go`, `internal/rooms/game_end_test.go`, `internal/rooms/handler_test.go`, `internal/rooms/websocket_test.go`.

**Interfaces:** Produces `ending`, `finalizedGameEndResults`, `gameEndCleanupDone`, `gameEndCleanupOnce`, `claimFinalizedGameEndResults`, `hasFinalizedGameEndResult`, `signalGameEndCleanupDone`.

- [ ] **Step 1: RED guard suite를 추가**

Required tests and exact boundaries:

- `TestRoomClaimsFinalizedGameEndResultOnlyOnce`: Lose 뒤 같은 ID Draw claim은 무시하고 new ID만 claim.
- `TestEndingRoomRejectsEveryMutation`: precondition에서 matchmaking acceptance가 true임을 확인한 뒤 `ending=true`; add/start -> `ErrRoomNotFound`, `canAcceptMatchmakingLocked` false, valid-token reserve -> `ErrRoomNotFound`, pre-created reservation attach false, current-session setInput no pending input, tick does not call Step.
- `TestFinalizedPlayerRejectsReserveAndInput`: wrong token -> `ErrUnauthorized`, valid token -> `ErrPlayerNotFound`, attached finalized player input ignored.
- `TestAttachRejectsReservationWhenFinalizationWinsConcurrentBoundary`: reservation을 먼저 만들고 main goroutine이 `room.mu`를 보유한 채 attach goroutine을 시작해 block; 같은 lock 아래 result claim 후 unlock; attach result false, rollback 뒤 reservation 없음.
- `TestAttachRejectsEndingAndStaleReservations`: ending reservation과 replaced reservation pointer 모두 false.
- `TestEndingRoomRejectsHardTTLAndDebugRemoval`: manual `ending=true` room의 `createdAt`을 hard lifetime 이전으로 옮긴 뒤 `cleanupExpired` 0, `clearRooms().Deleted` 0, `deleteRoom` false, registry/player IDs retained.

Concurrent boundary skeleton:

```go
room.mu.Lock()
started := make(chan struct{})
done := make(chan bool, 1)
go func() {
	close(started)
	_, attached := store.attachClientSession(reservation, newFakeClientConn(false))
	done <- attached
}()
<-started
room.claimFinalizedGameEndResults(map[string]gameEndResult{issued.Player.ID: gameEndResultLose})
room.mu.Unlock()
if <-done { t.Fatal("attach won after finalization owned room.mu") }
store.rollbackClientReservation(reservation)
```

- [ ] **Step 2: RED를 확인**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestRoomClaimsFinalized|TestEndingRoomRejects|TestFinalizedPlayerRejects|TestAttachRejects' -count=1
```

Expected: missing field/helper 또는 guard assertion FAIL.

- [ ] **Step 3: fields/helpers/guards를 구현**

- `newRoomLocked`: ledger map과 cleanup channel 초기화.
- add/start: `removed || ending` -> `ErrRoomNotFound`.
- matchmaking: `!removed && !ending` 포함.
- reserve: removed/ending check, player existence, token auth, finalized check 순서. Finalized valid token은 `ErrPlayerNotFound`.
- attach: existing `mutationMu -> Store.mu -> room.mu` lock order 아래 removed/ending/finalized/nil maps/stale pointer를 한 guard로 검사.
- input: removed/ending/finalized/stale session 거부.
- tick first guard: ending 거부.
- `room.isExpired`: `ending`이면 hard TTL 검사보다 먼저 false.
- `clearRooms`와 `deleteRoom`: room lock 아래 `ending`이면 skip/false. `detachRoomsForShutdown`은 이 guard를 사용하지 않아요.

- [ ] **Step 4: GREEN/full/race를 확인하고 commit**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestRoomClaimsFinalized|TestEndingRoomRejects|TestFinalizedPlayerRejects|TestAttachRejects' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test -race ./internal/rooms -run 'TestAttachRejectsReservationWhenFinalizationWinsConcurrentBoundary' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
git add internal/rooms/store.go internal/rooms/game_end.go internal/rooms/cleanup.go internal/rooms/websocket.go internal/rooms/game_end_test.go internal/rooms/handler_test.go internal/rooms/websocket_test.go
git commit -m "[SL-89] feat(rooms): 결과 ledger와 mutation guard 추가" -m "- 최초 GameEnd result를 불변으로 기록해요" -m "- ending finalized attach race를 모든 mutation 경계에서 막아요"
```

---

### Task 3: Mode Runtime Activation과 Normal Cleanup Barrier를 Atomic하게 연결

**Files:** `internal/rooms/game_end.go`, `internal/rooms/rooms.go`, `internal/rooms/messages.go`, `internal/rooms/cleanup.go`, `internal/rooms/websocket.go`, `internal/rooms/websocket_test.go`, `internal/rooms/observer_test.go`, `internal/rooms/logging_test.go`.

**Interfaces:** Produces `room.calculateGameEndResults`, `snapshotSessionsWithoutFinalizedGameEnd`, `clientSessions`, `roomResources.detachGameplayLocked`, `scheduleGameEndCleanup`, `finishGameEnd`, `waitForGameEndCleanup`.

- [ ] **Step 1: production-backed mode harness를 추가**

- `newModeTickHarness`는 `joinMatchmaking(mode)`를 `MatchPlayerCount()`번 호출해 실제 `store.playerIDs`와 player session을 만들어요.
- Room을 started fixture로 바꾼 뒤 각 requested player를 `reserveClient` + `attachClientSession`으로 연결해 실제 `activeSessions`, release callback, lifecycle monitor를 구성해요.
- Harness API는 아래 signatures로 고정해요.

```go
func newModeTickHarness(t *testing.T, mode string, observer Observer, blockWrites map[int]bool, connectedIndexes ...int) *modeTickHarness
func (h *modeTickHarness) playerID(index int) string
func (h *modeTickHarness) snapshot(tick simulation.Tick, deadIndexes ...int) simulation.Snapshot
func (h *modeTickHarness) setSnapshots(t *testing.T, snapshots ...simulation.Snapshot)
func (h *modeTickHarness) blockClose(t *testing.T, index int) (<-chan struct{}, func())
func waitForGameEndCleanup(t *testing.T, room *room)
```

`blockClose` exact safety contract:

```go
started := make(chan struct{})
allow := make(chan struct{})
var once sync.Once
release := func() { once.Do(func() { close(allow) }) }
conn.closeStarted = started
conn.closeBlock = allow
t.Cleanup(release)
return started, release
```

Harness cleanup은 먼저 모든 close release를 호출하고, session close, `store.Close()` 순서로 실행해 assertion failure에서도 hang하지 않아요.

- [ ] **Step 2: RED lifecycle tests를 추가**

- `TestTickRoomSoloIntermediateLoseKeepsSurvivorsRunning`: loser write를 block, two ticks 실행, ledger count 1/ending false/latest tick 2/ticker stop 0, survivor snapshots 지속, loser release 후 Lose + close.
- `TestTerminalCloseBarrierRetainsRegistryAndPlayerIDs`: winner close block 동안 room pointer가 registry에 남고 모든 harness player ID가 `store.playerIDs`에 있으며 cleanup channel open/ticker stop 1; release 뒤 cleanup signal, registry nil, 모든 IDs absent.
- `TestTerminalResourcesStopBeforeObserver`: blocking tick observer entry 시 ticker stop 1과 stop channel closed.
- `TestGameEndCleanupSignalRequiresSuccessfulCallbacks`: synchronous `finishGameEnd`에 one-shot panicking active-room observer를 사용해 panic을 recover하고 cleanup channel이 open인지 확인; observer를 noop으로 복구해 test cleanup.

- [ ] **Step 3: runtime을 한 번에 전환**

- Package `calculateGameEndResults`에 Duel/Solo/Team/default switch를 활성화하고 room method를 `calculateGameEndResults`로 rename.
- Tick lock 아래 Step -> ledger claim -> delivery/session capture -> terminal decision -> `ending=true` -> gameplay detach 순서.
- Unlock 직후 `terminalResources.stop()`, 그 뒤 tick observer, marshal, snapshot/terminal enqueue.
- Intermediate claimed session만 `snapshot -> GameEnd -> close("player eliminated")`; survivor snapshot/ticker는 유지.
- Terminal captured sessions 모두의 `closeDone`을 cleanup worker가 기다려요.
- `finishGameEnd`는 normal ownership을 얻은 경우에만 observer/registry/IDs/log/resources close를 끝낸 뒤 마지막 statement로 `signalGameEndCleanupDone()`을 호출해요. `removed=false`, stale registry, callback panic, shutdown takeover에서는 signal하지 않아요.
- `scheduleGameEndCleanup` launch가 shutdown gate 때문에 false면 signal하지 않아요.

- [ ] **Step 4: GREEN/full/race 후 commit**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestTickRoomSoloIntermediate|TestTerminalCloseBarrier|TestTerminalResourcesStopBeforeObserver|TestGameEndCleanupSignalRequiresSuccessfulCallbacks|TestActiveRoomGauge|TestRoomEnded' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test -race ./internal/rooms -run 'TestTickRoomSoloIntermediate|TestTerminalCloseBarrier|TestTerminalResourcesStopBeforeObserver' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
git add internal/rooms/game_end.go internal/rooms/rooms.go internal/rooms/messages.go internal/rooms/cleanup.go internal/rooms/websocket.go internal/rooms/websocket_test.go internal/rooms/observer_test.go internal/rooms/logging_test.go
git commit -m "[SL-89] feat(rooms): 모드 종료와 close barrier 활성화" -m "- pure result 활성화와 terminal-aware tick을 atomic하게 연결해요" -m "- 정상 callback과 closeDone 뒤에만 cleanup 완료를 알립니다"
```

---

### Task 4: Prior Lose, Team Six-Client, Real Duel Wire Regression

**Files:** Modify `internal/rooms/websocket_test.go`.

- [ ] **Step 1: prior Lose 뒤 remaining same-tick Draw wire test를 추가**

`TestTickRoomSoloPriorLoseRemainsLoseAndOnlyRemainingPlayersDraw`:

1. Six production-backed clients, tick 1에서 index 0만 dead.
2. Index 0의 terminal snapshot/Lose/close를 읽고 detach를 기다려요.
3. Tick 2에서 all dead snapshot을 공급해요.
4. Index 1~5만 snapshot/Draw/close를 받고 index 0 event channel에는 새 GameEnd가 없는지 확인해요.
5. Ledger는 index 0 Lose, index 1~5 Draw이고 cleanup 뒤 registry/IDs가 비었는지 확인해요.

- [ ] **Step 2: Team wire tests를 추가**

- Partial death tick: six snapshots, zero terminal, stop 0.
- Red elimination tick: red three Lose, blue three Win, close, cleanup, stop 1.
- Same-tick both-team elimination: six Draw payloads, terminal tick value, close, cleanup.

- [ ] **Step 3: real-WebSocket Duel tests를 close ACK aware로 변경**

```go
func readWebSocketClose(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("expected normal close frame, got %v", err)
	}
}
```

Win/Lose와 Draw tests는 internal room pointer를 terminal 전 capture하고, 양 client의 GameEnd 뒤 close frame을 읽어 handshake를 ACK한 다음 `waitForGameEndCleanup`을 호출해요. 기존 `waitForRoomDeleted` 단독 assertion은 cleanup completion 증거로 사용하지 않아요.

- [ ] **Step 4: GREEN/race/commit**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestTickRoom(SoloPrior|Team)|TestWebSocketSends(GameEnd|Draw)' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test -race ./internal/rooms -run 'TestTickRoom(SoloPrior|Team)' -count=1
git add internal/rooms/websocket_test.go
git commit -m "[SL-89] test(rooms): Solo Team Duel wire 계약 고정" -m "- prior Lose와 new Draw만 전달되는지 검증해요" -m "- real WebSocket close ACK 뒤 cleanup을 기다려요"
```

---

### Task 5: Ending Removal Barrier Integration과 Shutdown Exception 고정

**Files:** `internal/rooms/handler_test.go`, `internal/rooms/shutdown_test.go`, `internal/rooms/websocket_test.go`, `internal/rooms/logging_test.go`.

- [ ] **Step 1: RED bypass tests를 추가**

- `TestEndingRoomRejectsHardTTLAndDebugRemovalBeforeCloseDone`: Task 2 unit guard를 실제 terminal close barrier와 통합해, hard lifetime 이후 `cleanupExpired` returns 0, `clearRooms().Deleted` 0, `deleteRoom` false, registry/IDs retained; release 뒤 normal cleanup succeeds.
- `TestShutdownIsForcedExceptionToGameEndCloseBarrier`: terminal close block 뒤 Shutdown 시작; closeDone open 상태에서도 registry nil과 IDs empty가 될 수 있음을 확인; release/force 후 Shutdown이 writer/heartbeat/activeSessions/worker를 join; `gameEndCleanupDone` open, `room_ended` zero.

- [ ] **Step 2: Task 2 guard와 Task 3 shutdown-takeover semantics를 검증**

- 이 task는 production 동작을 새로 활성화하지 않는 integration-test commit이에요.
- `detachRoomsForShutdown`만 ending guard를 우회하는 forced path인지 확인해요.
- Shutdown takeover에서 `finishGameEnd`가 normal completion signal/`room_ended`를 만들지 않는지 확인해요.

- [ ] **Step 3: GREEN/full/race/commit**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestEndingRoomRejectsHardTTLAndDebugRemoval|TestShutdownIsForcedException' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test -race ./internal/rooms -run 'TestEndingRoomRejectsHardTTLAndDebugRemoval|TestShutdownIsForcedException' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
git add internal/rooms/handler_test.go internal/rooms/shutdown_test.go internal/rooms/websocket_test.go internal/rooms/logging_test.go
git commit -m "[SL-89] test(rooms): cleanup barrier와 Shutdown 예외 고정" -m "- hard TTL과 debug delete가 terminal close를 앞서지 않는지 검증해요" -m "- Shutdown forced teardown 예외를 회귀로 고정해요"
```

---

### Task 6: AsyncAPI와 Human Docs 동기화

**Files:** `api/asyncapi.yaml`, `docs-ui/scripts/validate.mjs`, `internal/docs/docs_test.go`, `ai-docs/protocol.md`, `ai-docs/api-reference.md`, `ai-docs/architecture.md`, `ai-docs/project-map.md`, `ai-docs/decisions.md`; verify `api/openapi.yaml` unchanged.

- [ ] **Step 1: RED markers를 source/embedded validation에 추가**

Required markers: `Solo 중간 탈락`, `이전 Lose는 유지`, `Team 일부 사망`, `마지막 생존자`, `ticker를 terminal decision 즉시 중단`, `closeDone 뒤 registry와 player ID를 정리`, `Shutdown은 forced-teardown 예외`, `duel_1v1`.

```bash
node docs-ui/scripts/validate.mjs
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -run TestHandlerServesRawSpecs -count=1
```

Expected: missing marker FAIL.

- [ ] **Step 2: source docs를 갱신**

- GameEnd wire fields/enums unchanged.
- Solo/Team/Duel result table, prior Lose/new Draw semantics, intermediate vs terminal close를 문서화.
- Normal order: stop -> tick observer/encode/enqueue -> closeDone -> registry/observer/ID/log -> cleanup signal.
- Hard TTL/debug removal은 ending room을 건드리지 않음.
- Shutdown은 registry/ID를 먼저 detach할 수 있는 forced exception이며 normal completion signal/`room_ended`를 만들지 않음을 문서화.

- [ ] **Step 3: docs GREEN과 scope를 확인하고 commit**

```bash
make docs-build
cmp api/asyncapi.yaml internal/docs/api/asyncapi.yaml
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -run TestHandlerServesRawSpecs -count=1
npx --yes --package @asyncapi/cli asyncapi validate api/asyncapi.yaml
git diff --exit-code 9976d2c9c0534e146a27dc0677adf0aa2add8c71..HEAD -- api/openapi.yaml client-config/game-config.json server-config/game-config.json internal/simulation
git add api/asyncapi.yaml docs-ui/scripts/validate.mjs internal/docs/docs_test.go ai-docs/protocol.md ai-docs/api-reference.md ai-docs/architecture.md ai-docs/project-map.md ai-docs/decisions.md
git commit -m "[SL-89] docs(protocol): 모드별 종료와 cleanup 예외 문서화" -m "- prior Lose와 Team terminal 의미를 설명해요" -m "- normal close barrier와 Shutdown exception을 구분해요"
```

---

### Task 7: Full Verification과 Scope Audit

**Files:** Verify only.

- [ ] **Step 1: focused/full/race suites**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'Test(Solo|Team|Duel)|TestCustomModeFallsBack|TestRoomClaimsFinalized|TestEndingRoom|TestFinalizedPlayer|TestAttachRejects|TestTerminal|TestShutdownIsForcedException|TestWebSocketSends' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test -race ./internal/rooms -run 'TestAttachRejectsReservationWhenFinalizationWinsConcurrentBoundary|TestTickRoom(Solo|Team)|TestTerminalResources|TestEndingRoomRejectsHardTTL|TestShutdownIsForcedException' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
make ci
```

Expected: all PASS, no race report.

- [ ] **Step 2: final scope audit**

```bash
git diff --check 9976d2c9c0534e146a27dc0677adf0aa2add8c71..HEAD
git diff --exit-code 9976d2c9c0534e146a27dc0677adf0aa2add8c71..HEAD -- api/openapi.yaml client-config/game-config.json server-config/game-config.json internal/simulation
git diff --name-only 9976d2c9c0534e146a27dc0677adf0aa2add8c71..HEAD
git status --short
git check-ignore -v internal/docs/api/asyncapi.yaml
```

Expected: whitespace/scope violations 없음, tracked tree clean, generated docs ignored.

## Final Acceptance Checklist

- [ ] Every task commit is independently green; no task activates Solo/Team pure results against the old early whole-room tick.
- [ ] All named test helpers have exact definitions.
- [ ] Prior Solo Lose stays immutable and only unresolved survivors receive terminal Draw.
- [ ] Every ending/finalized mutation boundary and a real concurrent attach boundary are tested.
- [ ] Production-backed harness proves player ID retention before closeDone and release afterward.
- [ ] Real Duel clients ACK close frames before normal cleanup completion is asserted.
- [ ] Stop precedes observer; callback panic never closes cleanup completion falsely.
- [ ] Janitor/debug delete cannot bypass ending barrier.
- [ ] Shutdown forced exception is implemented, tested, and documented separately from normal GameEnd.
- [ ] Team partial/one-team/both-team and Duel Win/Lose/Draw regressions pass.
- [ ] Docs, race, package, CI, generated-file, and scope checks pass.
