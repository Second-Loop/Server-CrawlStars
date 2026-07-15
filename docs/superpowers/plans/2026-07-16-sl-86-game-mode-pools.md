# SL-86 Game Mode Pools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `POST /matchmaking/join`이 `duel_1v1`, `solo`, `team`을 선택하고 같은 mode의 waiting room만 재사용하도록 만들어요.

**Architecture:** 서버 config는 `mode.default`와 `mode.catalog`를 소유하고, resolve 시 기본 mode를 선택한 runtime `GameConfig`를 만들어요. matchmaking room은 생성 시 선택된 `GameConfig`를 immutable하게 소유하며 capacity, team/slot, Ready, simulation, GameEnd가 이 room-local config만 사용해요. REST 응답은 top-level join과 nested room에 같은 `gameMode`를 노출해 client가 추론하지 않게 해요.

**Tech Stack:** Go 1.24, `net/http`, embedded JSON server config, OpenAPI 3.1, AsyncAPI 3.0, repository `make ci` validation.

## Global Constraints

- `gameMode`는 정확히 `duel_1v1`, `solo`, `team`만 지원해요.
- body 없음, `{}`, `{"gameMode":""}`는 `duel_1v1`로 처리해요.
- 알 수 없는 non-empty mode는 HTTP 400 `invalid_game_mode`, malformed JSON은 HTTP 400 `invalid_request`를 반환해요.
- config는 duel=`red:1 + blue:1`, solo=`solo-1`…`solo-6` 각 1명, team=`red:3 + blue:3`으로 정의해요.
- 모든 mode는 현재 하나의 Map_0을 공유하고 `room.maxPlayers`의 기존 map/debug capacity 의미는 유지해요.
- room은 생성 시 선택 mode config를 고정하고 Store 전역 default mode를 gameplay 판단에 사용하지 않아요.
- join response의 `gameMode`와 nested `room.gameMode`는 항상 같은 값이어야 해요.
- `friendlyFire`는 catalog metadata로만 저장하며 실제 projectile 판정은 SL-88에서 활성화해요.
- production queue, rating, mode별 map, reconnect, bot fill은 이 PR에 추가하지 않아요.

---

### Task 1: Mode Catalog와 Runtime Selection

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`
- Modify: `internal/simulation/player_assignment_test.go`
- Modify: `server-config/game-config.json`

**Interfaces:**
- Consumes: 기존 `GameModeConfig`, `TeamConfig`, `ResolveGameConfig`, `StaticGameConfig`.
- Produces: `GameModeCatalogConfig`, `GameConfig.SelectMode(string)`, room이 소유할 selected `GameConfig`.

- [ ] **Step 1: Write failing catalog artifact and selection tests**

`internal/simulation/game_config_test.go`에 embedded config가 아래 세 mode를 정확히 제공하는 table test를 추가해요.

```go
want := map[string]GameModeConfig{
	GameModeDuel1v1: {ID: GameModeDuel1v1, PlayersPerMatch: 2, Teams: []TeamConfig{{Name: TeamRed, Size: 1}, {Name: TeamBlue, Size: 1}}, Rules: GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams, FriendlyFire: false}},
	GameModeSolo: {ID: GameModeSolo, PlayersPerMatch: 6, Teams: []TeamConfig{{Name: Team("solo-1"), Size: 1}, {Name: Team("solo-2"), Size: 1}, {Name: Team("solo-3"), Size: 1}, {Name: Team("solo-4"), Size: 1}, {Name: Team("solo-5"), Size: 1}, {Name: Team("solo-6"), Size: 1}}, Rules: GameModeRulesConfig{TeamBehavior: TeamBehaviorFreeForAll, FriendlyFire: false}},
	GameModeTeam: {ID: GameModeTeam, PlayersPerMatch: 6, Teams: []TeamConfig{{Name: TeamRed, Size: 3}, {Name: TeamBlue, Size: 3}}, Rules: GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams, FriendlyFire: false}},
}
```

`SelectMode`가 `solo`/`team`을 고르고 원본 config를 바꾸지 않는지, unknown mode를 reject하는지, duplicate ID·missing default·team sum mismatch·map capacity 초과를 reject하는지도 각각 테스트해요.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'Test(ServerGameConfigModeCatalog|SelectMode|ResolveGameConfigRejectsInvalidModeCatalog)' -count=1
```

Expected: `GameModeCatalogConfig`, `GameModeSolo`, `GameModeTeam`, `SelectMode`이 아직 없어 compile failure가 나요.

- [ ] **Step 3: Implement the catalog and selected runtime config**

`internal/simulation/game_config.go`에 다음 shape를 추가해요.

```go
type GameConfig struct {
	Version      int                     `json:"version"`
	TickRate     int                     `json:"tickRate"`
	Tile         TileConfig              `json:"tile"`
	Player       PlayerTypeSetConfig     `json:"player"`
	Projectile   ProjectileTypeSetConfig `json:"projectile"`
	ModeCatalog  GameModeCatalogConfig   `json:"mode"`
	SelectedMode GameModeConfig          `json:"-"`
	Map          MapData                 `json:"map"`
}

type GameModeCatalogConfig struct {
	Default string           `json:"default"`
	Catalog []GameModeConfig `json:"catalog"`
}

const (
	GameModeDuel1v1 = "duel_1v1"
	GameModeSolo    = "solo"
	GameModeTeam    = "team"
	TeamBehaviorTwoTeams = "two_teams"
	TeamBehaviorFreeForAll = "free_for_all"
)

func (config GameConfig) SelectMode(id string) (GameConfig, error) {
	for _, mode := range config.ModeCatalog.Catalog {
		if mode.ID == id {
			selected := config
			selected.SelectedMode = mode
			return selected, nil
		}
	}
	return GameConfig{}, fmt.Errorf("unknown game mode %q", id)
}
```

`ResolveGameConfig`는 catalog 전체를 validate한 뒤 default mode를 `SelectedMode`로 선택하고, 각 mode의 `PlayersPerMatch <= map.maxPlayers`를 확인해요. 기존 `MatchPlayerCount`, `MatchTeamForPlayerIndex`, `TeamForPlayerIndex`는 `SelectedMode`만 읽어요. `StaticGameConfig`도 동일한 세 mode catalog와 duel default를 제공해요.

`server-config/game-config.json`의 기존 단일 `mode`를 다음 구조로 바꿔요.

```json
"mode": {
  "default": "duel_1v1",
  "catalog": [
    {"id":"duel_1v1","playersPerMatch":2,"teams":[{"name":"red","size":1},{"name":"blue","size":1}],"rules":{"teamBehavior":"two_teams","friendlyFire":false}},
    {"id":"solo","playersPerMatch":6,"teams":[{"name":"solo-1","size":1},{"name":"solo-2","size":1},{"name":"solo-3","size":1},{"name":"solo-4","size":1},{"name":"solo-5","size":1},{"name":"solo-6","size":1}],"rules":{"teamBehavior":"free_for_all","friendlyFire":false}},
    {"id":"team","playersPerMatch":6,"teams":[{"name":"red","size":3},{"name":"blue","size":3}],"rules":{"teamBehavior":"two_teams","friendlyFire":false}}
  ]
}
```

- [ ] **Step 4: Verify GREEN and assignment tables**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -count=1
```

Expected: package PASS. Team assignment is `red/0, blue/0, red/1, blue/1, red/2, blue/2`; Solo assignment is `solo-1/0` through `solo-6/0`.

- [ ] **Step 5: Commit**

```bash
git add internal/simulation/game_config.go internal/simulation/game_config_test.go internal/simulation/player_assignment_test.go server-config/game-config.json
git commit -m "[SL-86] feat(config): 게임 모드 catalog 추가" -m "- duel, solo, team 정원과 팀 구성을 검증해요" -m "- room이 선택할 runtime config helper를 추가해요"
```

### Task 2: Join Request와 Mode별 Waiting Pool

**Files:**
- Modify: `internal/rooms/errors.go`
- Modify: `internal/rooms/handler.go`
- Modify: `internal/rooms/handler_test.go`
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/store.go`

**Interfaces:**
- Consumes: Task 1의 `GameConfig.SelectMode`와 selected config assignment helpers.
- Produces: `matchmakingJoinRequest`, `joinMatchmaking(gameMode string)`, room-local config, REST `gameMode` fields.

- [ ] **Step 1: Write failing handler contract tests**

`internal/rooms/handler_test.go`에 다음 table을 추가해요.

```go
tests := []struct {
	name string
	body string
	wantStatus int
	wantMode string
	wantCode string
}{
	{"no body defaults", "", http.StatusCreated, simulation.GameModeDuel1v1, ""},
	{"empty object defaults", `{}`, http.StatusCreated, simulation.GameModeDuel1v1, ""},
	{"empty mode defaults", `{"gameMode":""}`, http.StatusCreated, simulation.GameModeDuel1v1, ""},
	{"solo", `{"gameMode":"solo"}`, http.StatusCreated, simulation.GameModeSolo, ""},
	{"team", `{"gameMode":"team"}`, http.StatusCreated, simulation.GameModeTeam, ""},
	{"unknown", `{"gameMode":"ranked"}`, http.StatusBadRequest, "", "invalid_game_mode"},
	{"malformed", `{"gameMode":`, http.StatusBadRequest, "", "invalid_request"},
}
```

성공 응답은 `response.GameMode == response.Room.GameMode`, room list/detail도 같은 mode를 반환한다고 assert해요. `duel`, `solo`, `team`, `solo` 순 join으로 다른 mode는 다른 room, 두 solo 요청은 같은 room이 되는 test를 추가해요.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'TestMatchmakingJoin(GameMode|SeparatesModePools|ResponseMode)' -count=1
```

Expected: request/response `gameMode`와 mode-aware join signature가 없어 compile failure 또는 assertion failure가 나요.

- [ ] **Step 3: Implement strict optional JSON decoding**

`messages.go`에 DTO를 추가해요.

```go
type matchmakingJoinRequest struct {
	GameMode string `json:"gameMode"`
}

type roomResponse struct {
	ID string `json:"id"`
	GameMode string `json:"gameMode"`
	// existing fields remain unchanged
}

type matchmakingJoinResponse struct {
	GameMode string `json:"gameMode"`
	Room roomResponse `json:"room"`
	// existing fields remain unchanged
}
```

`handler.go`에서 rate-limit 통과 뒤 body를 decode해요. EOF는 빈 요청으로 허용하고, 첫 JSON 뒤 non-whitespace token이 있으면 `invalid_request`로 reject해요. empty mode는 Store default ID로 바꾸고 unknown mode는 `invalid_game_mode`로 매핑해요.

- [ ] **Step 4: Implement immutable room mode ownership and pool filtering**

`room`에 다음 필드를 추가하고 생성자에 selected config를 넘겨요.

```go
type room struct {
	ID string
	mu sync.Mutex
	gameConfig simulation.GameConfig
	// existing fields
}

func (s *Store) joinMatchmaking(gameMode string) (matchmakingJoinResponse, error)
func (s *Store) newRoomLocked(roomID string, gameConfig simulation.GameConfig) *room
```

matchmaking join은 `s.gameConfig.SelectMode(gameMode)`를 한 번 실행하고, `room.gameConfig.SelectedMode.ID == gameMode`인 waiting room만 재사용해요. capacity와 player assignment는 `room.gameConfig.MatchPlayerCount()`와 `room.gameConfig.TeamForPlayerIndex()`를 사용해요. debug `POST /rooms`는 default selected config를 사용해 기존 동작을 유지해요. response builder는 room config의 selected mode ID를 top-level과 nested room에 한 번에 복사해요.

- [ ] **Step 5: Verify GREEN and legacy regressions**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'Test(MatchmakingJoin|DebugRoom|Room)' -count=1
```

Expected: focused tests PASS, no-body client remains duel, cross-mode pools never share a room.

- [ ] **Step 6: Commit**

```bash
git add internal/rooms/errors.go internal/rooms/handler.go internal/rooms/handler_test.go internal/rooms/messages.go internal/rooms/store.go
git commit -m "[SL-86] feat(rooms): 모드별 waiting pool 분리" -m "- join 요청과 응답에 gameMode 계약을 추가해요" -m "- room이 선택 mode config를 소유하도록 변경해요"
```

### Task 3: Room Lifecycle의 Selected Config 전파

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/websocket.go`
- Modify: `internal/rooms/websocket_test.go`
- Modify: `internal/rooms/game_end.go`
- Modify: `internal/rooms/game_end_test.go`

**Interfaces:**
- Consumes: Task 2의 `room.gameConfig`.
- Produces: Ready assignment, simulation state, countdown capacity, GameEnd가 모두 같은 selected config를 사용하는 invariant.

- [ ] **Step 1: Write failing propagation regressions**

Solo room과 Team room을 만든 뒤 Ready payload의 team/slot이 selected catalog와 같고, `startRoomLocked`가 만든 simulation snapshot도 같은 assignment를 쓰는 테스트를 추가해요. GameEnd helper 호출에는 room의 selected mode가 전달되는지 custom config test로 검증해요.

```go
wantTeam := []playerResponse{
	{Team: "red", Slot: 0}, {Team: "blue", Slot: 0},
	{Team: "red", Slot: 1}, {Team: "blue", Slot: 1},
	{Team: "red", Slot: 2}, {Team: "blue", Slot: 2},
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -run 'Test(RoomSelectedMode|ReadyEventUsesRoomMode|StartRoomUsesRoomMode|GameEndUsesRoomMode)' -count=1
```

Expected: remaining Store-global config call sites make at least one selected-mode assertion fail.

- [ ] **Step 3: Replace Store-global gameplay config reads**

`readyEventDeliveries`, `readyEventPlayers`, `simulationPlayers`, `startRoomLocked`, Ready/client capacity checks, gameplay tick interval, `calculateGameEndResults` 호출이 모두 caller가 lock한 room의 `gameConfig`를 사용하게 바꿔요. Store `gameConfig`는 새 room의 default/catalog source로만 남겨요.

```go
if room.state == nil {
	room.state = simulation.NewStateWithConfig(
		simulationPlayers(room.Players, room.gameConfig),
		simulation.Config{Game: room.gameConfig},
	)
}
```

- [ ] **Step 4: Verify GREEN and room package**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/rooms -count=1
```

Expected: package PASS with duel lifecycle regressions preserved.

- [ ] **Step 5: Commit**

```bash
git add internal/rooms/store.go internal/rooms/messages.go internal/rooms/websocket.go internal/rooms/websocket_test.go internal/rooms/game_end.go internal/rooms/game_end_test.go
git commit -m "[SL-86] refactor(rooms): room별 mode config 전파" -m "- Ready와 simulation이 선택 mode를 공유해요" -m "- 전역 default mode gameplay 참조를 제거해요"
```

### Task 4: REST Contract와 Architecture 문서화

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`
- Modify: `internal/docs/docs_test.go`
- Generated: `internal/docs/api/openapi.yaml`

**Interfaces:**
- Consumes: Tasks 1-3의 실제 request/response/runtime 이름.
- Produces: client가 그대로 구현할 수 있는 `gameMode` schema와 room ownership 문서.

- [ ] **Step 1: Add contract marker assertions before docs**

`internal/docs/docs_test.go` 또는 existing docs validator marker에 다음 literal 계약을 검증해요.

```text
gameMode
duel_1v1
solo
team
invalid_game_mode
invalid_request
```

- [ ] **Step 2: Run docs test and verify RED**

Run:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -count=1
```

Expected: source OpenAPI에 request/response mode schema가 없어 marker/schema assertion이 실패해요.

- [ ] **Step 3: Update OpenAPI and human docs**

OpenAPI에 optional request body와 enum을 추가해요.

```yaml
MatchmakingJoinRequest:
  type: object
  properties:
    gameMode:
      type: string
      enum: [duel_1v1, solo, team]
      default: duel_1v1
```

`Room`과 `MatchmakingJoinResponse`의 required field에 `gameMode`를 넣고, 400 examples로 `invalid_game_mode`와 `invalid_request`를 추가해요. `api-reference.md`는 no-body/empty compatibility와 예제 request를, `protocol.md`는 same-mode pool과 room-local config invariant를, `architecture.md`는 catalog와 room ownership을, `project-map.md`는 현재 활성 계약을 기록해요. `decisions.md`에는 mode catalog를 Store global active mode가 아니라 room-local selected config로 고정하는 ADR을 추가해요.

- [ ] **Step 4: Generate and validate docs**

Run:

```bash
make docs-build
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/docs -count=1
npx --yes --package @asyncapi/cli asyncapi validate api/asyncapi.yaml
```

Expected: docs build and tests PASS, AsyncAPI remains valid because no WebSocket shape changes in SL-86.

- [ ] **Step 5: Run full validation**

Run:

```bash
make ci
```

Expected: docs validation/build, `go vet`, all Go tests, server build, 14 deploy regressions, shell syntax checks all exit 0.

- [ ] **Step 6: Commit**

```bash
git add api/openapi.yaml ai-docs/api-reference.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/project-map.md ai-docs/decisions.md internal/docs/api/openapi.yaml internal/docs/docs_test.go
git commit -m "[SL-86] docs(api): 게임 모드 매칭 계약 문서화" -m "- join과 room gameMode schema를 공개해요" -m "- mode catalog와 room 소유권 결정을 기록해요"
```

## Final Review Checklist

- [ ] Linear SL-86의 모든 scope/acceptance 항목이 Task 1-4 중 하나에 연결돼요.
- [ ] 모든 구현 단계에 exact path, command, expected result가 있어요.
- [ ] `GameConfig.SelectMode` → `room.gameConfig` → response/Ready/simulation/GameEnd type 흐름이 일관돼요.
- [ ] `make ci` 직후 clean worktree에서 final whole-branch review를 수행해요.
