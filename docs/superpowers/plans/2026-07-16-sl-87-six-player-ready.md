# SL-87 Six-Player Ready Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Solo와 Team matchmaking room이 6명의 human player, 6개의 WebSocket, 6개의 서로 다른 Ready ACK를 기다린 뒤 countdown과 gameplay를 정확히 한 번 시작하도록 만들어요.

**Architecture:** SL-86이 만든 `room.gameConfig`의 selected mode를 match 정원과 team/slot/spawn의 유일한 기준으로 사용해요. 기존 ready/countdown state machine은 인원 수를 고정하지 않은 구조이므로 새 state나 message를 추가하지 않고 실제 6-socket 통합 테스트로 계약을 고정해요. 프로덕션 수정은 6-player fallback을 막는 `fallbackSpawnTiles`의 Wall/Water spawn 결함만 고치고, AsyncAPI와 사람이 읽는 문서가 duel 2명과 Solo/Team 6명을 함께 설명하게 해요.

**Tech Stack:** Go 1.25, `nhooyr.io/websocket`, room-local fake clock, table-driven Go tests, AsyncAPI 3.0, Node.js docs validator, repository `make ci` validation.

## Global Constraints

- 실행 base에는 SL-86의 `GameConfig.SelectMode(string) (GameConfig, error)`, `GameModeSolo`, `GameModeTeam`, `room.gameConfig`, mode별 `POST /matchmaking/join` pool 분리, Solo/Team 정원·team/slot assignment가 있어야 해요.
- 실행 base의 tile 계약은 `TileWall`, `TileBush`, `TileWater`와 `tileBlocksPlayer(TileType) bool`을 포함하며, SL-87은 같은 player blocking policy를 fallback spawn 안전성에도 재사용해요.
- Solo와 Team은 정확히 6명의 human player와 6개의 서로 다른 WebSocket connection을 요구해요.
- `Ready` event는 선택 mode의 required player가 모두 join하고 모두 WebSocket에 attach된 뒤 6개 connection 모두에게 같은 payload로 한 번 전달해요.
- Countdown은 6개의 서로 다른 player session에서 `{"Type":"ready"}` ACK가 모두 도착한 뒤 시작해요. 같은 player의 중복 ACK는 quorum을 늘리거나 countdown을 다시 시작하지 않아요.
- `starting` snapshot은 `countdown: 5`로 connection당 한 번, `started` control은 5초 뒤 connection당 한 번 전달하고 gameplay ticker는 room당 하나만 만들어요.
- Ready payload와 첫 gameplay snapshot의 team/slot/spawn은 `room.gameConfig`에 대한 `simulation.PlayerAssignments` 결과와 같아야 해요.
- Fallback spawn candidate는 Wall과 Water를 제외하고, passable candidate가 남아 있는 동안 6명에게 서로 다른 좌표를 줘야 해요. Bush는 player가 통과할 수 있으므로 제외하지 않아요.
- 기존 `duel_1v1`의 2 WebSocket, 2 Ready ACK, 5초 countdown, 1회 start 회귀는 그대로 유지해요.
- Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 추가하지 않아요. Start 전 실제 disconnect는 기존 pre-start cancel 동작을 유지해요.
- Solo/Team GameEnd, elimination, friendly-fire 판정, score, respawn은 이 PR에서 바꾸지 않아요.
- 새 WebSocket message type이나 `Ready` payload의 `gameMode` field를 추가하지 않아요. Client는 SL-86 join response의 `gameMode`를 사용하고, `Ready.Players`의 team/slot/spawn만 mode-aware하게 확장해요.
- `api/openapi.yaml`은 SL-86 REST `gameMode` 계약이 그대로인지 확인만 하고 수정하지 않아요. WebSocket cardinality와 team enum 변경은 `api/asyncapi.yaml`에서 문서화해요.
- 모든 변경은 `sl-87-six-player-ready` branch, plan/code/docs 세 commit, 하나의 PR에만 포함해요.

## Binding Review Corrections

아래 항목은 이후의 예시 code보다 우선해요. 구현 중 충돌하는 예시가 있으면 이 기준을 따라요.

- SL-86 base `a5bc59e4d02597d5f0cded7e1a6f7a46d1b6859f`를 고정 base로 사용해요.
- 기존 quorum helper의 실제 signature는 `(*room).allMatchClientsAttached()`와 `(*room).allMatchPlayersReady()`예요.
- 6-socket lifecycle은 SL-86에서 이미 동작하므로 characterization test는 먼저 PASS를 확인해요. Task 1의 의도된 RED는 Wall/Water fallback spawn test에서만 만들어요.
- 실제 WebSocket acceptance는 attach 1~5 동안 Ready가 한 번도 전달되지 않고, 6번째 attach 뒤 각 connection에 Ready가 정확히 한 번 전달됨을 검증해요.
- 다섯 distinct ACK와 duplicate ACK 뒤에는 loading/no ticker를 확인해요. Duplicate 처리 동기화는 `lastActivityAt`이 아니라 같은 socket의 zero input과 기존 `waitForPendingInput`을 사용해요.
- 여섯 번째 ACK 뒤 connection별 다음 control을 정확히 한 번 읽어 `starting`인지 확인해요. quorum 이후 duplicate ACK를 한 번 더 보내도 countdown ticker는 계속 하나이고, 다음 control은 중복 `starting` 없이 `started`여야 해요. `readUntilSnapshotStatus`처럼 중복을 건너뛰는 helper는 쓰지 않아요.
- 첫 gameplay snapshot의 player 6명은 `ID`, `Team`, `Slot`, `Pos`를 `simulation.PlayerAssignments`와 전부 비교해 Ready assignment와 동일함을 증명해요.
- 기존 duel regression도 첫 distinct ACK와 duplicate ACK 뒤 loading/no ticker, 두 번째 distinct ACK 뒤 one countdown/one started/one gameplay ticker를 명시적으로 검증해요.
- fake-session regression은 attach 1~5의 write count 0과 마지막 attach 뒤 connection별 Ready write count 1을 고정해요.
- AsyncAPI `ReadyEventMessage.Players`는 3~5명을 허용하는 `minItems/maxItems`가 아니라 array schema `oneOf`로 정확히 2개 또는 6개만 허용하고 validator도 두 cardinality를 구조적으로 검사해요.
- Fallback unit test는 passable Bush candidate가 유지되고 Wall/Water만 제외됨을 함께 검증해요.
- `api/openapi.yaml`과 generated OpenAPI는 변경하지 않아요. Generated `internal/docs/api/*`는 ignored artifact라 검증만 하고 `git add`하지 않아요.
- final scope는 `git diff a5bc59e4d02597d5f0cded7e1a6f7a46d1b6859f..HEAD`로 확인해요.

---

### Task 1: Safe Fallback Spawn과 6-Socket Ready Acceptance

**Files:**
- Modify: `internal/simulation/player_assignment.go`
- Test: `internal/simulation/player_assignment_test.go`
- Test: `internal/rooms/websocket_test.go`

**Interfaces:**
- Consumes: SL-86의 `GameConfig.SelectMode(string) (GameConfig, error)`, `room.gameConfig simulation.GameConfig`, `(*Store).joinMatchmaking(gameMode string) (matchmakingJoinResponse, error)`.
- Consumes: SL-86 response의 `matchmakingJoinResponse.GameMode string`, `roomResponse.GameMode string`.
- Consumes: 기존 `(*Store).markClientReady(roomID string, playerID string, expectedSession *clientSession)`, `(*room).allMatchClientsAttached() bool`, `(*room).allMatchPlayersReady() bool`.
- Consumes: `simulation.PlayerAssignments([]simulation.PlayerID, simulation.GameConfig) []simulation.PlayerAssignment`.
- Consumes: 현재 simulation package의 `tileBlocksPlayer(TileType) bool`, `TileWall`, `TileWater`.
- Produces: Wall/Water를 제외하는 `fallbackSpawnTiles(MapData) []spawnTile` invariant.
- Produces: `joinMatchmakingForLifecycle(*testing.T, http.Handler, string) matchmakingJoinResponse` test helper.
- Produces: `waitForMatchLifecycleState(*testing.T, *Store, string, MatchStatus, int, int)` test helper.
- Reuses: `waitForPendingInput(*testing.T, *Store, string, string)` for ordered duplicate-ACK synchronization.
- Produces: `TestWebSocketSixPlayerModesWaitForSixHumanReadyACKsAndStartOnce` acceptance regression.

- [ ] **Step 1: Write the failing six-player fallback safety test**

`internal/simulation/player_assignment_test.go`에 아래 test를 추가해요. Static 5x5 map의 center Wall과 의도적으로 넣은 Water를 모두 피하면서 Solo 6명에게 unique fallback을 주는지 고정해요.

```go
func TestPlayerAssignmentsSkipBlockingFallbackTilesForSixPlayers(t *testing.T) {
	config, err := StaticGameConfig().SelectMode(GameModeSolo)
	if err != nil {
		t.Fatalf("select solo mode: %v", err)
	}
	config.Map.Map[1][2] = TileWater
	config.Map.Map[2][1] = TileBush

	playerIDs := []PlayerID{
		"player-1", "player-2", "player-3",
		"player-4", "player-5", "player-6",
	}
	assignments := PlayerAssignments(playerIDs, config)
	if len(assignments) != 6 {
		t.Fatalf("expected six assignments, got %d", len(assignments))
	}

	blockedPositions := map[Vector2]TileType{
		config.Map.WorldPos(2, 2): TileWall,
		config.Map.WorldPos(2, 1): TileWater,
	}
	wantTiles := []spawnTile{
		{X: 1, Y: 1},
		{X: 3, Y: 3},
		{X: 3, Y: 1},
		{X: 1, Y: 3},
		{X: 1, Y: 2},
		{X: 3, Y: 2},
	}
	seen := make(map[Vector2]bool, len(assignments))
	for index, assignment := range assignments {
		if tile, blocked := blockedPositions[assignment.SpawnPosition]; blocked {
			t.Fatalf("assignment %d landed on blocking fallback tile %d at %+v", index, tile, assignment.SpawnPosition)
		}
		if seen[assignment.SpawnPosition] {
			t.Fatalf("assignment %d reused fallback spawn %+v", index, assignment.SpawnPosition)
		}
		seen[assignment.SpawnPosition] = true

		want := config.Map.WorldPos(wantTiles[index].X, wantTiles[index].Y)
		if assignment.SpawnPosition != want {
			t.Fatalf("assignment %d expected spawn %+v, got %+v", index, want, assignment.SpawnPosition)
		}
		if index == 4 && config.Map.Map[wantTiles[index].Y][wantTiles[index].X] != TileBush {
			t.Fatal("expected the fifth passable fallback candidate to remain Bush")
		}
	}
}
```

- [ ] **Step 2: Write the Solo/Team six-WebSocket lifecycle acceptance test**

`internal/rooms/websocket_test.go`에 아래 test와 helpers를 추가해요. 다섯 번째 ACK 뒤에는 `loading`, 여섯 번째 서로 다른 ACK 뒤에는 `starting`, 5초 뒤에는 `started`가 되며 countdown/gameplay ticker가 하나씩만 생성되는지 검증해요.

```go
func TestWebSocketSixPlayerModesWaitForSixHumanReadyACKsAndStartOnce(t *testing.T) {
	tests := []struct {
		mode  string
		teams []string
		slots []int
	}{
		{
			mode:  simulation.GameModeSolo,
			teams: []string{"solo-1", "solo-2", "solo-3", "solo-4", "solo-5", "solo-6"},
			slots: []int{0, 0, 0, 0, 0, 0},
		},
		{
			mode:  simulation.GameModeTeam,
			teams: []string{"red", "blue", "red", "blue", "red", "blue"},
			slots: []int{0, 0, 1, 1, 2, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			fakeClock := newFakeClock()
			store := NewStoreWithClock(5, fakeClock)
			handler := debugHandler(t, store)
			server := httptest.NewServer(handler)
			defer server.Close()

			joined := make([]matchmakingJoinResponse, 6)
			for index := range joined {
				joined[index] = joinMatchmakingForLifecycle(t, handler, tt.mode)
				if joined[index].GameMode != tt.mode || joined[index].Room.GameMode != tt.mode {
					t.Fatalf("join %d expected mode %q, got top-level %q room %q", index, tt.mode, joined[index].GameMode, joined[index].Room.GameMode)
				}
				if index > 0 && joined[index].Room.ID != joined[0].Room.ID {
					t.Fatalf("join %d expected room %q, got %q", index, joined[0].Room.ID, joined[index].Room.ID)
				}
			}

			roomID := joined[0].Room.ID
			connections := make([]*websocket.Conn, 6)
			for index := 0; index < 5; index++ {
				conn := dialIssuedPlayer(t, server.URL, joined[index].WebSocketPath)
				connections[index] = conn
				defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
				waitForAttachedClient(t, store, roomID, joined[index].Player.ID)
			}
			waitForMatchLifecycleState(t, store, roomID, MatchStatusMatched, 5, 0)

			sixth := dialIssuedPlayer(t, server.URL, joined[5].WebSocketPath)
			connections[5] = sixth
			defer func() { _ = sixth.Close(websocket.StatusNormalClosure, "") }()
			waitForAttachedClient(t, store, roomID, joined[5].Player.ID)
			waitForMatchLifecycleState(t, store, roomID, MatchStatusLoading, 6, 0)

			readyEvents := make([]readyEventMessage, 6)
			for index, conn := range connections {
				readyEvents[index] = readReadyEventMessage(t, conn)
				if index > 0 {
					assertMatchingReadyEvents(t, readyEvents[0], readyEvents[index])
				}
			}
			if len(readyEvents[0].Players) != 6 {
				t.Fatalf("expected Ready event with six players, got %+v", readyEvents[0].Players)
			}

			room := store.lookupRoom(roomID)
			if room == nil {
				t.Fatalf("expected room %q", roomID)
			}
			room.mu.Lock()
			gameConfig := room.gameConfig
			room.mu.Unlock()
			if gameConfig.SelectedMode.ID != tt.mode {
				t.Fatalf("expected selected room mode %q, got %q", tt.mode, gameConfig.SelectedMode.ID)
			}

			playerIDs := make([]simulation.PlayerID, 0, 6)
			for _, issued := range joined {
				playerIDs = append(playerIDs, simulation.PlayerID(issued.Player.ID))
			}
			assignments := simulation.PlayerAssignments(playerIDs, gameConfig)
			if len(assignments) != 6 {
				t.Fatalf("expected six mode assignments, got %+v", assignments)
			}
			wantSpawnTiles := []struct{ X, Y int }{
				{X: 1, Y: 1},
				{X: 3, Y: 3},
				{X: 3, Y: 1},
				{X: 1, Y: 3},
				{X: 2, Y: 1},
				{X: 1, Y: 2},
			}
			for index, issued := range joined {
				wantSpawn := gameConfig.Map.WorldPos(wantSpawnTiles[index].X, wantSpawnTiles[index].Y)
				if assignments[index].SpawnPosition != wantSpawn {
					t.Fatalf("assignment %d expected passable fallback %+v, got %+v", index, wantSpawn, assignments[index].SpawnPosition)
				}
				assertReadyPlayerTeamSlot(t, readyEvents[0].Players, issued.Player.ID, tt.teams[index], tt.slots[index])
				assertReadyPlayerSpawn(t, readyEvents[0].Players, issued.Player.ID, assignments[index].SpawnPosition)
			}

			for index := 0; index < 5; index++ {
				writeWSJSON(t, connections[index], readyMessage{Type: "ready"})
			}
			waitForMatchLifecycleState(t, store, roomID, MatchStatusLoading, 6, 5)
			if got := fakeClock.TickerCount(time.Second); got != 0 {
				t.Fatalf("expected no countdown after five distinct ACKs, got %d ticker(s)", got)
			}

			writeWSJSON(t, connections[0], readyMessage{Type: "ready"})
			writeWSJSON(t, connections[0], inputMessage{})
			waitForPendingInput(t, store, roomID, joined[0].Player.ID)
			waitForMatchLifecycleState(t, store, roomID, MatchStatusLoading, 6, 5)
			if got := fakeClock.TickerCount(time.Second); got != 0 {
				t.Fatalf("expected duplicate ACK not to create countdown, got %d ticker(s)", got)
			}

			writeWSJSON(t, connections[5], readyMessage{Type: "ready"})
			waitForMatchLifecycleState(t, store, roomID, MatchStatusStarting, 6, 6)
			if !waitForFakeTickerCount(fakeClock, time.Second, 1, time.Second) {
				t.Fatal("expected one countdown ticker after six distinct ACKs")
			}
			if got := fakeClock.TickerCount(time.Second); got != 1 {
				t.Fatalf("expected exactly one countdown ticker, got %d", got)
			}

			var firstStarting matchSnapshotMessage
			for index, conn := range connections {
				starting := readSnapshotMessage(t, conn)
				if starting.Snapshot.Status != MatchStatusStarting {
					t.Fatalf("connection %d expected next control starting, got %+v", index, starting.Snapshot)
				}
				if starting.Snapshot.Countdown != 5 {
					t.Fatalf("connection %d expected countdown 5, got %+v", index, starting.Snapshot)
				}
				if index == 0 {
					firstStarting = starting
				} else {
					assertMatchingMatchSnapshots(t, firstStarting, starting)
				}
			}

			writeWSJSON(t, connections[0], readyMessage{Type: "ready"})
			writeWSJSON(t, connections[0], inputMessage{})
			waitForPendingInput(t, store, roomID, joined[0].Player.ID)
			if got := fakeClock.TickerCount(time.Second); got != 1 {
				t.Fatalf("expected duplicate ACK after quorum to keep one countdown ticker, got %d", got)
			}

			for range 5 {
				fakeClock.TickTicker(time.Second, 0)
			}
			waitForMatchLifecycleState(t, store, roomID, MatchStatusStarted, 6, 6)

			var firstStarted matchSnapshotMessage
			for index, conn := range connections {
				started := readSnapshotMessage(t, conn)
				if started.Snapshot.Status != MatchStatusStarted {
					t.Fatalf("connection %d expected next control started without duplicate starting, got %+v", index, started.Snapshot)
				}
				if index == 0 {
					firstStarted = started
				} else {
					assertMatchingMatchSnapshots(t, firstStarted, started)
				}
			}
			if got := fakeClock.TickerCount(gameplayInterval); got != 1 {
				t.Fatalf("expected exactly one gameplay ticker, got %d", got)
			}

			fakeClock.TickTicker(gameplayInterval, 0)
			firstGameplay := readSnapshotMessage(t, connections[0])
			if firstGameplay.Snapshot.Tick != 1 || len(firstGameplay.Snapshot.Players) != 6 {
				t.Fatalf("expected first six-player gameplay tick, got %+v", firstGameplay.Snapshot)
			}
			for index, got := range firstGameplay.Snapshot.Players {
				want := assignments[index]
				if got.ID != want.ID || got.Team != want.Team || got.Slot != want.Slot || got.Pos != want.SpawnPosition {
					t.Fatalf("gameplay player %d expected assignment %+v, got %+v", index, want, got)
				}
			}
			for index := 1; index < len(connections); index++ {
				gameplay := readSnapshotMessage(t, connections[index])
				assertMatchingSnapshots(t, firstGameplay, gameplay)
			}
		})
	}
}

func joinMatchmakingForLifecycle(t *testing.T, handler http.Handler, gameMode string) matchmakingJoinResponse {
	t.Helper()

	body := strings.NewReader(fmt.Sprintf(`{"gameMode":%q}`, gameMode))
	req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("join mode %q expected status 201, got %d with body %s", gameMode, rec.Code, rec.Body.String())
	}

	var joined matchmakingJoinResponse
	decodeResponse(t, rec, &joined)
	return joined
}

func waitForMatchLifecycleState(
	t *testing.T,
	store *Store,
	roomID string,
	wantStatus MatchStatus,
	wantClients int,
	wantReady int,
) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	var gotStatus MatchStatus
	var gotClients int
	var gotReady int
	for time.Now().Before(deadline) {
		room := store.lookupRoom(roomID)
		if room != nil {
			room.mu.Lock()
			gotStatus = room.matchStatus
			gotClients = len(room.clients)
			gotReady = len(room.readyPlayers)
			room.mu.Unlock()
			if gotStatus == wantStatus && gotClients == wantClients && gotReady == wantReady {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf(
		"room %s expected status=%q clients=%d ready=%d, got status=%q clients=%d ready=%d",
		roomID,
		wantStatus,
		wantClients,
		wantReady,
		gotStatus,
		gotClients,
		gotReady,
	)
}

```

- [ ] **Step 3: Run the new tests and verify RED**

Run the focused fallback RED:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run TestPlayerAssignmentsSkipBlockingFallbackTilesForSixPlayers -count=1
```

Expected: FAIL with `assignment 4 landed on blocking fallback tile 1`; the current preferred center fallback is the StaticMap Wall at `(2,2)`.

Before adding fallback-specific position assertions, run the six-socket lifecycle characterization:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run TestWebSocketSixPlayerModesWaitForSixHumanReadyACKsAndStartOnce -count=1
```

Expected: PASS. SL-86 already supplies the mode-local quorum lifecycle. Record this as characterization evidence, then let the focused simulation fallback test be the only intended RED. After Step 4, add the binding-review exact Ready/control/gameplay assignment assertions and run this rooms test again as GREEN.

- [ ] **Step 4: Filter fallback spawn candidates through the player blocking policy**

`internal/simulation/player_assignment.go`의 `fallbackSpawnTiles` 내부 `appendUnique` closure를 아래 코드로 교체해요. Bounds check 뒤 `tileBlocksPlayer`를 사용하므로 Wall과 Water만 제외하고 Ground, SpawnPoint, Bush는 유지해요.

```go
	appendUnique := func(tile spawnTile) {
		if seen[tile] {
			return
		}
		if tile.Y < 0 || tile.Y >= len(gameMap.Map) {
			return
		}
		if tile.X < 0 || tile.X >= len(gameMap.Map[tile.Y]) {
			return
		}
		if tileBlocksPlayer(gameMap.Map[tile.Y][tile.X]) {
			return
		}
		seen[tile] = true
		tiles = append(tiles, tile)
	}
```

- [ ] **Step 5: Run focused tests and verify GREEN**

Run:

```bash
mise exec -- gofmt -w internal/simulation/player_assignment.go internal/simulation/player_assignment_test.go internal/rooms/websocket_test.go
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestPlayerAssignments(SkipBlockingFallbackTilesForSixPlayers|UseMapDerivedFallbackSpawnsWhenNoSpawnTiles|UseFallbackOnlyAfterSpawnPointsAreExhausted|AvoidFallbackTilesAlreadyUsedBySpawnPoints)' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestWebSocket(MatchmakingUsesSnapshotStatusForReadyCountdownAndStart|SixPlayerModesWaitForSixHumanReadyACKsAndStartOnce)' -count=1
```

Expected: `gofmt` prints nothing and both test commands PASS. Solo Ready uses `solo-1/0` through `solo-6/0`; Team Ready uses `red/0, blue/0, red/1, blue/1, red/2, blue/2`; five ACKs do not create a countdown ticker; six ACKs create exactly one countdown ticker and one gameplay ticker; the existing duel test remains green.

- [ ] **Step 6: Commit the safe six-player lifecycle**

```bash
git add internal/simulation/player_assignment.go internal/simulation/player_assignment_test.go internal/rooms/websocket_test.go
git commit -m "[SL-87] feat(rooms): 6인 Ready 시작 흐름 고정" -m "- Solo와 Team의 6 WebSocket 및 human Ready quorum을 검증해요" -m "- fallback spawn이 Wall과 Water를 피하게 해요"
```

### Task 2: Mode-Aware Ready WebSocket Contract와 Architecture 문서화

**Files:**
- Modify: `api/asyncapi.yaml`
- Verify only: `api/openapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `internal/docs/docs_test.go`
- Generated: `internal/docs/api/asyncapi.yaml`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`

**Interfaces:**
- Consumes: Task 1의 실제 Solo/Team team/slot/spawn 순서와 six-client quorum behavior.
- Produces: AsyncAPI `info.version: 0.3.0`, mode-aware Ready cardinality `oneOf` exact length 2 or 6, expanded Team enum.
- Produces: source spec validator와 embedded served-spec regression.
- Produces: ADR-0029의 room-local Ready quorum 및 safe fallback 결정.

- [ ] **Step 1: Add failing source and embedded contract guards**

`docs-ui/scripts/validate.mjs`의 기존 AsyncAPI assertion 묶음에 아래 코드를 추가해요.

```js
for (const marker of [
  "duel_1v1은 2명, solo와 team은 6명",
  "6개의 서로 다른 WebSocket connection",
  "각 player가 보낸 ready ACK",
  "Ready timeout",
  "solo-6",
]) {
  assert(asyncAPIText.includes(marker), `api/asyncapi.yaml must document ${marker}`);
}
assert(asyncAPIText.includes("info:\n  title: Server Crawl Stars WebSocket API\n  version: 0.3.0"), "api/asyncapi.yaml must publish version 0.3.0");
const modeTeamEnum = "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]";
assert(asyncAPIText.split(modeTeamEnum).length - 1 === 2, "ReadyPlayer and PlayerData must expose every mode team value");
for (const exactItems of [2, 6]) {
  const marker = [
    "              type: array",
    `              minItems: ${exactItems}`,
    `              maxItems: ${exactItems}`,
  ].join("\n");
  assert(asyncAPIText.includes(marker), `Ready Players must allow exactly ${exactItems} items`);
}
```

`internal/docs/docs_test.go`에 아래 served artifact test를 추가해요.

```go
func TestHandlerServesModeAwareSixPlayerReadyContract(t *testing.T) {
	asyncAPI := request(Handler(), http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	for _, want := range []string{
		"version: 0.3.0",
		"duel_1v1은 2명, solo와 team은 6명",
		"6개의 서로 다른 WebSocket connection",
		"각 player가 보낸 ready ACK",
		"Ready timeout",
		"solo-6",
		"minItems: 2",
		"maxItems: 2",
		"minItems: 6",
		"maxItems: 6",
	} {
		assertBodyContains(t, asyncAPI, want)
	}
}
```

- [ ] **Step 2: Run contract guards and verify RED**

Run:

```bash
node docs-ui/scripts/validate.mjs
```

Expected: FAIL with `api/asyncapi.yaml must document duel_1v1은 2명, solo와 team은 6명`.

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -run TestHandlerServesModeAwareSixPlayerReadyContract -count=1
```

Expected: FAIL because the embedded AsyncAPI artifact is still version `0.2.0` and does not contain `solo-6` or `maxItems: 6`.

- [ ] **Step 3: Update the AsyncAPI source with exact six-player semantics**

`api/asyncapi.yaml`의 version을 `0.3.0`으로 올리고 `info.description`의 matchmaking 문단을 아래 문장으로 교체해요. 기존 token, heartbeat, delivery-order 문장은 그대로 둬요.

```yaml
  version: 0.3.0
  description: |
    E2 개발용 WebSocket 계약입니다. Client는 `POST /matchmaking/join` 또는 debug room API로 받은 room/player로 연결합니다.
    연결할 때 발급 응답의 player session token을 정확히 한 개의 `token` query parameter로 전달합니다.
    Matchmaking room은 선택 mode의 required player가 모두 연결되면 `Type: Ready` 이벤트로 렌더 준비에 필요한 map과 player별 team/slot/spawn을 전달합니다.
    duel_1v1은 2명, solo와 team은 6명의 human player와 6개의 서로 다른 WebSocket connection을 요구합니다. Countdown은 각 player가 보낸 ready ACK가 모두 모인 뒤 한 번만 시작합니다.
    같은 player의 중복 ready ACK는 quorum을 늘리거나 countdown을 다시 시작하지 않습니다. Server는 `starting` 신호를 한 번 보낸 뒤 5초를 내부에서 세고 `started` gameplay snapshot을 broadcast합니다.
    Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 제공하지 않습니다. Start 전 실제 disconnect는 pre-start cancel로 room을 삭제합니다.
    Server는 연결마다 독립적인 30초 heartbeat를 실행하고 각 Ping에 90초 deadline을 둡니다. Ping error/timeout은 read/write failure와 같은 close-once disconnect 경로를 사용합니다.
    일반 gameplay snapshot은 client별 latest-only slot에서 coalescing합니다. Ready, starting, started, error는 reliable control 순서를 유지하고, 종료 시에는 terminal snapshot -> GameEnd -> close 순서를 보장합니다.
    Token은 일회용이 아니며 room/player session이 존재하는 동안 재사용할 수 있습니다. 다만 matched/loading/starting 단계의 실제 disconnect는 pre-start cancel로 room을 삭제하고, started room도 TTL과 hard lifetime 이후에는 사라집니다. Failed upgrade는 room을 취소하지 않아 같은 발급 경로로 재시도할 수 있습니다.
    발급 응답의 `sessionToken`, tokenized `webSocketPath`, inbound query는 모두 같은 raw secret을 담으므로 전체 query 문자열과 함께 log에 남기면 안 됩니다.
```

`channels.roomPlayer.description`의 Ready 관련 두 문장과 `operations.receiveReady.summary`를 아래 내용으로 교체해요.

```yaml
      Matchmaking room에서는 selected mode required player가 모두 연결되면 `Type: Ready` 이벤트를 받습니다.
      duel_1v1의 2개 또는 solo/team의 6개 player session이 각각 ready ACK를 보낸 뒤 starting 신호와 started snapshot을 받습니다.
```

```yaml
  receiveReady:
    action: receive
    channel:
      $ref: "#/channels/roomPlayer"
    title: Receive Ready event
    summary: selected mode의 required client가 모두 연결된 뒤 map과 player별 team, slot, spawn 정보를 받습니다.
    messages:
      - $ref: "#/channels/roomPlayer/messages/readyEvent"
```

`components.messages.ReadyEventMessage`를 아래 complete 6-player Team example로 교체해요.

```yaml
    ReadyEventMessage:
      name: ReadyEventMessage
      title: Ready event
      summary: 서버가 selected mode의 client 렌더 준비에 필요한 map과 player assignment를 전달합니다.
      payload:
        $ref: "#/components/schemas/ReadyEventMessage"
      examples:
        - name: teamReadyEvent
          summary: team mode 6명이 5x5 fallback map에서 받는 예시입니다. Fallback spawn은 Wall과 Water를 제외합니다.
          payload:
            Type: Ready
            Map:
              width: 5
              height: 5
              index: 0
              maxPlayers: 6
              tileSize: 1.2
              map:
                - [1, 1, 1, 1, 1]
                - [1, 0, 0, 0, 1]
                - [1, 0, 1, 0, 1]
                - [1, 0, 0, 0, 1]
                - [1, 1, 1, 1, 1]
            Players:
              - Id: player_VuTsRqPoNmLkJiHgFeDcBa
                Team: red
                Slot: 0
                SpawnPosition: {x: -1.2, y: 1.2}
              - Id: player_AbCdEfGhIjKlMnOpQrStUv
                Team: blue
                Slot: 0
                SpawnPosition: {x: 1.2, y: -1.2}
              - Id: player_wXyZ0123456789AbCdEfGh
                Team: red
                Slot: 1
                SpawnPosition: {x: 1.2, y: 1.2}
              - Id: player_q1W2e3R4t5Y6u7I8o9P0aS
                Team: blue
                Slot: 1
                SpawnPosition: {x: -1.2, y: -1.2}
              - Id: player_LmNoPqRsTuVwXyZaBcDeFg
                Team: red
                Slot: 2
                SpawnPosition: {x: 0, y: 1.2}
              - Id: player_hGfEdCbAzYxWvUtSrQpOnM
                Team: blue
                Slot: 2
                SpawnPosition: {x: -1.2, y: 0}
```

`components.schemas.ReadyEventMessage`, `ReadyAckMessage`, `MatchStatus`, `ReadyPlayer.Team`, `PlayerData.Team`을 아래 shape와 문장으로 갱신해요.

```yaml
    ReadyEventMessage:
      type: object
      required: [Type, Map, Players]
      description: 선택 mode의 required client 모두에게 보내는 렌더 준비 이벤트입니다. `Map.map`은 JSON number array row이고 `Players`는 room-local mode assignment 순서입니다.
      properties:
        Type:
          type: string
          const: Ready
        Map:
          $ref: "#/components/schemas/MapData"
        Players:
          oneOf:
            - type: array
              minItems: 2
              maxItems: 2
              items:
                $ref: "#/components/schemas/ReadyPlayer"
            - type: array
              minItems: 6
              maxItems: 6
              items:
                $ref: "#/components/schemas/ReadyPlayer"
    ReadyAckMessage:
      type: object
      required: [Type]
      description: >-
        각 human player의 `Type: Ready` 렌더 준비가 끝난 뒤 해당 player의
        WebSocket session에서 한 번 이상 보내는 idempotent ACK입니다.
      properties:
        Type:
          type: string
          const: ready
```

```yaml
    MatchStatus:
      type: string
      enum: [starting, started]
      description: |
        starting은 selected mode의 서로 다른 모든 player가 ready ACK를 보낸 뒤 server 내부 countdown이 시작됐음을 알리는 1회 신호입니다.
        started는 countdown이 끝나고 room의 단일 gameplay ticker와 snapshot stream이 시작된 상태입니다.
```

```yaml
        Team:
          type: string
          enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]
```

위 `Team` block을 `ReadyPlayer`와 `PlayerData` 양쪽에 동일하게 적용해요. `SnapshotMessage`의 `startingSignal.summary`는 아래 문장으로 바꿔요.

```yaml
          summary: selected mode의 모든 human client가 ready ACK를 보낸 뒤 connection당 한 번 받는 countdown 시작 신호입니다.
```

- [ ] **Step 4: Update human docs and add ADR-0029**

`ai-docs/api-reference.md`의 `시뮬레이션 시작 트리거` 목록을 아래 block으로 교체해요.

```markdown
시뮬레이션 시작 quorum:

| gameMode | Join 정원 | WebSocket | 서로 다른 Ready ACK | team/slot |
| --- | ---: | ---: | ---: | --- |
| `duel_1v1` | 2 | 2 | 2 | `red/0`, `blue/0` |
| `solo` | 6 | 6 | 6 | `solo-1/0`부터 `solo-6/0` |
| `team` | 6 | 6 | 6 | `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2` |

- Required player가 모두 join해도 `room.status`는 Ready/start 전까지 `waiting`입니다.
- Required player가 모두 WebSocket에 연결되면 모든 connection이 같은 `Type: "Ready"` event를 받습니다.
- Ready의 `Players[].Team`, `Slot`, `SpawnPosition`은 room이 선택한 mode config의 assignment 결과입니다.
- 다섯 Solo/Team player만 ACK한 상태에서는 countdown을 시작하지 않습니다. 여섯 번째 서로 다른 player ACK 뒤 `starting/countdown: 5`를 한 번 보냅니다.
- 같은 player의 중복 ACK는 quorum을 늘리지 않고 countdown이나 gameplay ticker를 다시 만들지 않습니다.
- Server는 5초를 내부에서 센 뒤 `started`를 한 번 보내고 room-local 30Hz snapshot을 시작합니다.
- Ready timeout, reconnect grace, participant replacement, bot fill은 없습니다. Start 전 실제 WebSocket close는 match cancel입니다.
- 1명으로 디버그할 때는 `POST /rooms/{roomID}/start`를 호출합니다.
```

`ai-docs/api-docs.md`의 Ready ACK 예시 다음에 아래 표와 설명을 추가해요.

```markdown
| mode | Ready `Players` 길이 | 필요한 human ACK |
| --- | ---: | ---: |
| `duel_1v1` | 2 | 2 |
| `solo` | 6 | 6 |
| `team` | 6 | 6 |

Solo는 `solo-1`부터 `solo-6`까지 각 slot 0을 사용합니다. Team은 join 순서대로 `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2`를 사용합니다. Ready spawn과 첫 gameplay snapshot position은 같은 room-local `PlayerAssignments` 결과입니다. Fallback map에서는 Wall과 Water를 spawn candidate에서 제외합니다.
```

`ai-docs/protocol.md`의 기존 `## 2인 검증 시나리오` block을 아래 complete duel/6-player block으로 교체해요.

```markdown
## Duel 2인 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 secret-bearing `webSocketPath`로 연결하되 raw path/query를 log에 남기지 않습니다.
3. 두 connection이 같은 `Type: "Ready"` event를 받고, 이 event의 `Map.map` row가 숫자 배열이어야 합니다.
4. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
5. 한 player가 movement input을 보내면 두 connection이 같은 `Snapshot.Tick`과 player `Pos`를 받아야 합니다.
6. Red와 blue spawn은 Ready event의 `Players[].SpawnPosition`으로 확인합니다.
7. Hit tick에서 projectile은 `IsDestroyed: true`, target은 HP 감소로 보여야 합니다.
8. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
9. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
10. `GameEnd` 이후 해당 room은 정리되어야 합니다.
11. Invalid JSON 이후에도 다음 snapshot stream은 유지되어야 합니다.

## Solo/Team 6인 검증 시나리오

1. 같은 `gameMode`로 `POST /matchmaking/join`을 6번 호출하고 여섯 응답의 `room.id`와 `gameMode`가 같은지 확인합니다.
2. 여섯 secret-bearing `webSocketPath`로 서로 다른 WebSocket connection을 열고 raw token/query를 log에 남기지 않습니다.
3. 다섯 connection만 attach된 동안 room은 `matched`이며 Ready event와 countdown은 시작하지 않습니다.
4. 여섯 번째 connection이 attach되면 여섯 connection 모두 같은 `Ready` event를 받습니다.
5. `solo`는 `solo-1/0`부터 `solo-6/0`, `team`은 `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2`를 확인합니다.
6. Ready의 six spawn이 room-local `PlayerAssignments`와 같고 fallback 사용 시 Wall/Water가 아니며 서로 다른지 확인합니다.
7. 서로 다른 다섯 player만 ready ACK를 보내면 `loading`을 유지하고 countdown ticker가 없어야 합니다.
8. 이미 ready인 player가 ACK를 반복해도 quorum은 5로 유지됩니다.
9. 여섯 번째 서로 다른 player가 ACK하면 `starting/countdown: 5`를 connection당 한 번 받고 countdown ticker는 하나여야 합니다.
10. 5초 뒤 `started`를 connection당 한 번 받고 gameplay ticker는 하나여야 합니다.
11. 첫 gameplay tick은 `Tick: 1`, `Players` 길이 6이고 여섯 connection에서 같은 payload여야 합니다.

Ready timeout, reconnect grace, reconnect participant replacement, bot fill은 이 흐름에 없습니다. Start 전 실제 disconnect는 기존 pre-start cancel로 room과 남은 connection을 정리합니다.
```

같은 `ai-docs/protocol.md`의 fallback 설명은 아래 문장으로 교체해요.

```markdown
Player spawn은 map의 `TileSpawnPoint(2)`를 join 순서대로 먼저 사용합니다. SpawnPoint가 부족하면 map에서 유도한 fallback candidate를 사용하되 player blocking policy와 같은 기준으로 Wall과 Water를 제외합니다. Ground와 Bush는 후보가 될 수 있고, 사용 가능한 candidate가 남아 있는 동안 이미 쓴 spawn과 겹치지 않습니다.
```

`ai-docs/architecture.md`의 `Simple matchmaking`과 `Mode/team rule` 목록을 아래 핵심 bullet로 갱신해요.

```markdown
- Room은 생성 시 SL-86 selected `GameConfig`를 고정하고 required player 수, team/slot/spawn, Ready quorum, simulation start가 모두 이 config를 사용합니다.
- `duel_1v1`은 2명, `solo`와 `team`은 6명이 같은-mode waiting pool에서 match를 완성합니다.
- Selected mode의 required player가 모두 attach되면 같은 Ready payload를 broadcast하고, required player session 각각의 ready ACK가 모이면 countdown을 한 번 시작합니다.
- `readyPlayers map[string]bool`이 player identity별 quorum을 소유하므로 duplicate ACK는 idempotent합니다.
- `startMatchCountdownLocked`와 `startRoomLocked`의 기존 status guard가 countdown ticker와 gameplay ticker를 room당 하나로 제한합니다.
- `PlayerAssignments`는 SpawnPoint를 먼저 쓰고 fallback candidate에서 `tileBlocksPlayer`가 true인 Wall/Water를 제외합니다.
- Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 추가하지 않습니다.
```

`ai-docs/project-map.md`의 현재 상태와 요청 흐름에는 아래 bullet을 반영해요.

```markdown
- `duel_1v1` 2명과 `solo`/`team` 6명의 mode별 matchmaking pool
- Solo/Team 6 WebSocket, 6 human Ready ACK, 1회 countdown/start regression
- room-local mode config 기반 team/slot/spawn과 Wall/Water-safe fallback assignment
```

요청 흐름의 Ready 문단은 아래 문장으로 교체해요.

```markdown
Required player가 모두 join해도 REST `room.status`는 `waiting`입니다. Selected mode의 required player 2명 또는 6명이 각각 WebSocket에 연결하면 모든 connection이 같은 `Ready` event를 받고, 각 human player의 ready ACK가 모두 모이면 `starting/countdown: 5`를 한 번 broadcast합니다. 5초 뒤 `started`를 한 번 보내고 room-local gameplay ticker 하나를 시작합니다.
```

`ai-docs/decisions.md` 끝에 아래 ADR을 추가해요.

```markdown
## ADR-0029: SL-87 Ready Quorum은 Room-local Mode Config를 따른다

상태: 승인됨

맥락: SL-86은 `duel_1v1`, `solo`, `team`의 waiting pool과 room-local selected config를 제공합니다. 기존 Ready state machine은 required count를 받을 수 있지만 실제 6 WebSocket, 6 human ACK, duplicate ACK, single-start behavior가 end-to-end로 고정되지 않았습니다. 또한 5x5 StaticMap의 preferred fallback 가운데 center `(2,2)`가 Wall이라 다섯 번째 player가 blocking tile에서 시작할 수 있었습니다.

결정:

- `duel_1v1`은 2명, `solo`와 `team`은 6명의 human player와 서로 다른 WebSocket session을 required quorum으로 사용합니다.
- Room의 selected `GameConfig`가 required count와 team/slot/spawn의 유일한 기준입니다.
- Required client가 모두 attach된 뒤 같은 Ready payload를 보내고, `readyPlayers map[string]bool`에 required player identity가 모두 들어온 뒤 countdown을 한 번 시작합니다.
- Duplicate ACK는 idempotent하고 `starting`, `started`, countdown ticker, gameplay ticker를 추가로 만들지 않습니다.
- Fallback spawn candidate는 player collision policy를 재사용해 Wall과 Water를 제외합니다. Passable candidate가 남아 있는 동안 spawn position은 중복하지 않습니다.
- Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 추가하지 않습니다. Start 전 실제 disconnect는 기존 pre-start cancel을 유지합니다.
- Solo/Team GameEnd와 elimination rule은 별도 issue 범위로 남깁니다.

결과:

- Solo와 Team은 6개의 실제 WebSocket과 6개의 human Ready ACK 없이는 시작하지 않습니다.
- Ready assignment와 첫 gameplay snapshot은 같은 room-local config와 spawn 결과를 사용합니다.
- Config fallback에서도 여섯 player가 Wall/Water가 아닌 unique spawn으로 시작합니다.
- 기존 duel 2-player Ready/countdown/start wire behavior는 유지됩니다.
```

- [ ] **Step 5: Generate docs and verify GREEN**

Run:

```bash
make docs-build
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -run 'TestHandler(ServesRawSpecs|ServesModeAwareSixPlayerReadyContract)' -count=1
rg -n 'gameMode|duel_1v1|invalid_game_mode' api/openapi.yaml
npx --yes --package @asyncapi/cli asyncapi validate api/asyncapi.yaml
```

Expected: docs source validation and generation PASS, both served-spec tests PASS, `rg` finds the SL-86 request/response `gameMode`, `duel_1v1`, and `invalid_game_mode` markers, and AsyncAPI CLI reports a valid AsyncAPI 3.0 document. `internal/docs/api/asyncapi.yaml` is byte-for-byte generated from `api/asyncapi.yaml`; `api/openapi.yaml` and `internal/docs/api/openapi.yaml` remain unchanged from SL-86.

- [ ] **Step 6: Commit the WebSocket contract and architecture docs**

```bash
git add api/asyncapi.yaml docs-ui/scripts/validate.mjs internal/docs/docs_test.go ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/project-map.md ai-docs/decisions.md
git commit -m "[SL-87] docs(api): 6인 Ready 계약 문서화" -m "- mode별 WebSocket 및 human ACK quorum을 공개해요" -m "- room-local assignment와 safe fallback 결정을 기록해요"
```

### Task 3: Duel Regression, Full Validation, and One-PR Scope Check

**Files:**
- Verify: `internal/simulation/player_assignment.go`
- Verify: `internal/simulation/player_assignment_test.go`
- Verify: `internal/rooms/websocket_test.go`
- Verify: `api/asyncapi.yaml`
- Verify: `api/openapi.yaml`
- Verify: `internal/docs/api/asyncapi.yaml`
- Verify: `ai-docs/api-reference.md`
- Verify: `ai-docs/api-docs.md`
- Verify: `ai-docs/protocol.md`
- Verify: `ai-docs/architecture.md`
- Verify: `ai-docs/project-map.md`
- Verify: `ai-docs/decisions.md`

**Interfaces:**
- Consumes: plan commit과 Tasks 1-2의 두 SL-87 commits.
- Produces: repeated focused regression evidence, official repository validation, exact one-PR file scope.

- [ ] **Step 1: Repeat the Solo/Team and duel lifecycle regressions**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run TestPlayerAssignmentsSkipBlockingFallbackTilesForSixPlayers -count=10
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestWebSocket(MatchmakingUsesSnapshotStatusForReadyCountdownAndStart|SixPlayerModesWaitForSixHumanReadyACKsAndStartOnce)' -count=10
```

Expected: both commands PASS all 10 runs. The existing duel test still observes 2 Ready ACKs, one `starting/countdown: 5`, one `started`, and first gameplay tick 1; Solo and Team observe the equivalent 6-player quorum with one countdown/gameplay ticker.

- [ ] **Step 2: Run package and official validation**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation ./internal/rooms ./internal/docs -count=1
npx --yes --package @asyncapi/cli asyncapi validate api/asyncapi.yaml
make ci
```

Expected: all three Go packages PASS, AsyncAPI CLI validation succeeds, and `make ci` exits 0 after docs validation/build, format check, vet, all Go tests, server build, deploy regressions, and shell syntax checks.

- [ ] **Step 3: Verify the fixed-base SL-87 scope and whitespace**

Run:

```bash
git diff --check a5bc59e4d02597d5f0cded7e1a6f7a46d1b6859f..HEAD
git diff --name-only a5bc59e4d02597d5f0cded7e1a6f7a46d1b6859f..HEAD
git diff --exit-code a5bc59e4d02597d5f0cded7e1a6f7a46d1b6859f..HEAD -- api/openapi.yaml
```

Expected: `git diff --check` prints nothing. The name-only output contains exactly these paths:

```text
ai-docs/api-docs.md
ai-docs/api-reference.md
ai-docs/architecture.md
ai-docs/decisions.md
ai-docs/project-map.md
ai-docs/protocol.md
api/asyncapi.yaml
docs-ui/scripts/validate.mjs
docs/superpowers/plans/2026-07-16-sl-87-six-player-ready.md
internal/docs/docs_test.go
internal/rooms/websocket_test.go
internal/simulation/player_assignment.go
internal/simulation/player_assignment_test.go
```

- [ ] **Step 4: Prepare the single review-ready PR scope**

Use branch `sl-87-six-player-ready` and PR title:

```text
[SL-87] Solo/Team 6인 Ready 시작 흐름 추가
```

Use this PR body:

```markdown
## 왜 해당 PR을 올렸나요?

- Solo/Team match가 6명의 실제 client 준비를 기다린다는 서버 계약이 필요해요.
- 5x5 fallback map에서 다섯 번째 player가 Wall에 spawn할 수 있었어요.

## 무엇을 어떻게 수정했나요?

- Solo/Team의 6 WebSocket, 6 human Ready ACK, 1회 countdown/start를 통합 테스트로 고정해요.
- Fallback spawn candidate에서 Wall과 Water를 제외해요.
- AsyncAPI와 서버 문서에 mode별 quorum과 team/slot/spawn 계약을 반영해요.
```

Expected: PR에는 SL-87 파일만 있고 ready timeout, reconnect, bot, Solo/Team GameEnd, friendly-fire 구현은 포함되지 않아요.

## Final Review Checklist

- [ ] Solo와 Team 각각 6번 join한 결과가 같은 mode room 하나에 모여요.
- [ ] 여섯 번째 WebSocket attach 전에는 Ready를 시작하지 않고, attach 후 6개 connection에 같은 Ready payload를 보내요.
- [ ] Ready의 Solo team/slot은 `solo-1/0`부터 `solo-6/0`, Team은 `red/0, blue/0, red/1, blue/1, red/2, blue/2`예요.
- [ ] Ready spawn과 첫 gameplay snapshot은 같은 `room.gameConfig` assignment를 사용해요.
- [ ] 다섯 distinct ACK와 duplicate ACK만으로 countdown을 시작하지 않아요.
- [ ] 여섯 번째 distinct ACK 뒤 countdown ticker, `starting`, `started`, gameplay ticker가 각각 한 번만 생겨요.
- [ ] Static fallback six-player assignment가 Wall/Water를 피하고 unique 좌표를 사용해요.
- [ ] 기존 duel 2-player lifecycle test가 10회 반복 통과해요.
- [ ] AsyncAPI `ReadyPlayer.Team`과 `PlayerData.Team` enum이 red/blue와 solo-1..solo-6을 모두 포함해요.
- [ ] OpenAPI REST mode 계약은 SL-86과 동일하고 새 WebSocket message type이 없어요.
- [ ] `make ci`, AsyncAPI CLI validation, `git diff --check`가 모두 통과해요.
- [ ] plan/code/docs 세 commit과 하나의 PR이 SL-87 범위만 포함해요.
