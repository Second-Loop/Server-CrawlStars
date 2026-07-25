# SL-82 CharacterType Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `POST /matchmaking/join`에서 선택한 CharacterType을 canonical room participant에 저장하고, REST 응답부터 Ready와 gameplay Snapshot까지 보존하면서 캐릭터별 HP와 공통 speed/radius를 server config에서 적용해요.

**Architecture:** `simulation.CharacterType`과 config v2의 명시적 numeric lookup을 먼저 만들어요. Join decoder는 `json.RawMessage`로 missing과 explicit `null`을 구분하고, Store는 gameMode를 먼저 선택한 뒤 CharacterType을 검증해 mutation 전에 실패시켜요. 검증된 값은 기존 `room.Players []playerResponse`에 저장하고 Ready와 `simulation.PlayerData` projection이 같은 값을 복사해 별도 side map 없이 전파해요.

**Tech Stack:** Go 1.25/mise, `net/http`, `encoding/json`, `log/slog`, table-driven Go tests, OpenAPI 3.1, AsyncAPI 3.0, Node.js source validator, Redocly CLI 2.38.0, AsyncAPI CLI 6.0.2

## Global Constraints

- CharacterType은 `0=shelly`, `1=colt`, `2=lily`이며 배열 index가 아니라 명시적 config key예요. 이후 재번호화하지 않아요.
- HP는 Shelly `4000`, Colt `3100`, Lily `4100`이고 세 캐릭터의 v1 speed/radius는 모두 `2`와 `0.5`예요.
- SL-82에서는 세 캐릭터 모두 기존 attack budget `4 charges / 30 recharge ticks`를 유지해요. 탄창 `3/3/2`와 공격 동작은 SL-83 범위예요.
- Join의 missing `characterType`만 Shelly `0`으로 보정해요. Explicit `null`, 비정수, 문자열, bool, object, array, 음수, `3` 이상은 `400 invalid_character_type`이에요.
- 오류 우선순위는 `429 rate_limited` → `400 invalid_request` → `400 invalid_game_mode` → `400 invalid_character_type`이에요. Closed Store의 기존 `500 internal_error` 경계는 바꾸지 않아요.
- 누락 호환 경로만 deprecated예요. OpenAPI property에 `deprecated: true`, `default: 0`, `nullable`을 넣지 않아요. 설명과 legacy example로 SL-98 필수화 경계를 표시해요.
- REST는 required `characterType`, Ready/Snapshot은 required `CharacterType`을 사용하며 `omitempty`를 쓰지 않아요. Shelly의 zero value도 wire에 보여야 해요.
- Bot과 debug-created player는 생성 지점에서 `simulation.CharacterTypeShelly`를 명시해요. Bot 선택 정책은 추가하지 않아요.
- Client config는 version `2`로 올리되 legacy `playerTypes: ["default"]`와 `playerRadius: 0.5`를 유지하고 additive `characters` catalog를 추가해요.
- Server config와 `StaticGameConfig()`는 동일한 version 2의 3종 catalog를 사용해요. Embedded load 실패는 application warning 뒤 static v2 fallback을 사용해요.
- AsyncAPI `info.version`은 기존 계약 버전 관례에 따라 `0.6.0`으로 올리고 OpenAPI `info.version`은 `0.1.0`을 유지해요.
- Unknown JSON field와 duplicate key의 기존 `encoding/json` 동작은 이번 범위에서 바꾸지 않아요.
- Starting/started control Snapshot의 `Players: null`과 reconnect, bot, GameEnd lifecycle은 유지해요.
- Client Unity parser의 v2 지원은 이 서버 저장소 밖의 통합 검증이에요. 이 저장소는 artifact test와 Node validator로 schema와 mapping만 고정해요.

## File Structure

| 책임 | 파일 | 변경 내용 |
| --- | --- | --- |
| Config identity | `internal/simulation/game_config.go` | CharacterType, exact v2 validation, order-independent lookup, static catalog |
| Config artifacts | `client-config/game-config.json`, `server-config/game-config.json` | additive client catalog와 authoritative server stats |
| Config regression | `internal/simulation/game_config_test.go`, `cmd/server/main_test.go`, `internal/rooms/store_config_test.go` | mapping, presence, fallback, version 회귀 |
| Simulation state | `internal/simulation/simulation.go`, `internal/simulation/simulation_test.go` | PlayerData identity와 character별 stat normalization |
| Join ownership | `internal/rooms/errors.go`, `messages.go`, `handler.go`, `store.go` | raw presence, error ordering, canonical participant, warning |
| Room integration | `internal/rooms/character_type_test.go`, 기존 rooms 테스트 | REST/Ready/Snapshot/bot/reconnect/death 회귀 |
| Contract source | `api/openapi.yaml`, `api/asyncapi.yaml` | optional request, required responses, examples, AsyncAPI 0.6.0 |
| Contract verification | `docs-ui/scripts/validate.mjs`, `build.mjs`, `internal/docs/docs_test.go` | cross-file drift와 served docs 검증 |
| Durable docs | `ai-docs/api-reference.md`, `api-docs.md`, `protocol.md`, `architecture.md`, `decisions.md`, `project-map.md` | 현재 계약, 책임, ADR, 후속 SL-98 경계 |

---

### Task 1: Config v2 CharacterType Catalog와 Fallback

**Files:**

- Modify: `client-config/game-config.json`
- Modify: `server-config/game-config.json`
- Modify: `internal/simulation/game_config.go:9-123,229-318,392-407`
- Modify: `internal/simulation/simulation.go:9-20`
- Modify: `internal/simulation/player_assignment.go:32-49`
- Modify: `internal/simulation/game_config_test.go:12-151,495-555`
- Modify: `cmd/server/main.go:408-416`
- Modify: `cmd/server/main_test.go`
- Modify: `internal/rooms/store.go:123-171`
- Create: `internal/rooms/store_config_test.go`
- Modify: `docs-ui/scripts/validate.mjs:1-12,433-455`

**Interfaces:**

- Consumes: 기존 `LoadGameConfig(io.Reader) (GameConfig, error)`, `ResolveGameConfig(GameConfig) (GameConfig, error)`, `StaticGameConfig() GameConfig` 흐름을 유지해요.
- Produces: `simulation.CharacterType`, `CharacterTypeShelly/Colt/Lily`, `GameConfigVersion`, `GameConfig.PlayerType(CharacterType) (PlayerTypeConfig, bool)`, Shelly를 반환하는 `DefaultPlayerType()`을 Task 2와 Task 3에 제공해요.

- [ ] **Step 1: Character catalog와 config presence 실패 테스트를 먼저 작성**

`internal/simulation/game_config_test.go`의 client helper에 `Characters`를 추가하고 다음 테스트를 작성해요.

```go
type clientCharacterConfig struct {
	CharacterType CharacterType `json:"characterType"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Role          string        `json:"role"`
}

type clientSharedGameConfig struct {
	Version            int                     `json:"version"`
	TileSize           float64                 `json:"tileSize"`
	PlayerRadius       float64                 `json:"playerRadius"`
	PlayerTypes        []string                `json:"playerTypes"`
	Characters         []clientCharacterConfig `json:"characters"`
	ProjectileRadius   float64                 `json:"projectileRadius"`
	ProjectileTypes    []string                `json:"projectileTypes"`
	ContainsServerMode bool                    `json:"mode"`
}

func TestClientAndServerCharacterCatalogMappingsMatch(t *testing.T) {
	client := loadClientSharedGameConfig(t)
	server := loadServerGameConfig(t)
	want := map[CharacterType]string{
		CharacterTypeShelly: "shelly",
		CharacterTypeColt:   "colt",
		CharacterTypeLily:   "lily",
	}
	if client.Version != GameConfigVersion || server.Version != GameConfigVersion {
		t.Fatalf("client/server version = %d/%d, want %d", client.Version, server.Version, GameConfigVersion)
	}
	clientMapping := make(map[CharacterType]string, len(client.Characters))
	for _, character := range client.Characters {
		clientMapping[character.CharacterType] = character.ID
	}
	serverMapping := make(map[CharacterType]string, len(server.Player.Types))
	for _, playerType := range server.Player.Types {
		serverMapping[playerType.CharacterType] = playerType.ID
	}
	if len(client.Characters) != len(want) || len(clientMapping) != len(client.Characters) {
		t.Fatalf("client character catalog is not exact/unique: entries=%d mapping=%v", len(client.Characters), clientMapping)
	}
	if len(server.Player.Types) != len(want) || len(serverMapping) != len(server.Player.Types) {
		t.Fatalf("server character catalog is not exact/unique: entries=%d mapping=%v", len(server.Player.Types), serverMapping)
	}
	if !reflect.DeepEqual(clientMapping, want) || !reflect.DeepEqual(serverMapping, want) {
		t.Fatalf("character mapping drift: client=%v server=%v want=%v", clientMapping, serverMapping, want)
	}
	if !reflect.DeepEqual(client.PlayerTypes, []string{"default"}) || client.PlayerRadius != 0.5 {
		t.Fatalf("legacy client mirror changed: playerTypes=%v playerRadius=%v", client.PlayerTypes, client.PlayerRadius)
	}
}

func TestGameConfigPlayerTypeLookupIsIndependentOfCatalogOrder(t *testing.T) {
	config := StaticGameConfig()
	slices.Reverse(config.Player.Types)
	for characterType, wantHP := range map[CharacterType]float64{
		CharacterTypeShelly: 4000,
		CharacterTypeColt:   3100,
		CharacterTypeLily:   4100,
	} {
		got, ok := config.PlayerType(characterType)
		if !ok || got.HP != wantHP {
			t.Fatalf("PlayerType(%d) = %+v, %t; want HP %v", characterType, got, ok, wantHP)
		}
	}
	if got := config.DefaultPlayerType(); got.CharacterType != CharacterTypeShelly || got.ID != "shelly" {
		t.Fatalf("DefaultPlayerType() = %+v, want Shelly", got)
	}
}

func TestResolveGameConfigRejectsUnsupportedVersion(t *testing.T) {
	config := StaticGameConfig()
	config.Version = 1
	if _, err := ResolveGameConfig(config); err == nil || !strings.Contains(err.Error(), "version must be 2") {
		t.Fatalf("ResolveGameConfig(version 1) error = %v, want exact-version rejection", err)
	}
}

func TestPlayerTypeConfigRejectsMissingOrNullCharacterType(t *testing.T) {
	for name, payload := range map[string]string{
		"missing": `{"id":"shelly","radius":0.5,"hp":4000,"speed":2,"maxAttackCharges":4,"attackRechargeTicks":30}`,
		"null":    `{"characterType":null,"id":"shelly","radius":0.5,"hp":4000,"speed":2,"maxAttackCharges":4,"attackRechargeTicks":30}`,
	} {
		t.Run(name, func(t *testing.T) {
			var playerType PlayerTypeConfig
			if err := json.Unmarshal([]byte(payload), &playerType); err == nil {
				t.Fatal("expected missing/null characterType to fail")
			}
		})
	}
}
```

`TestResolveGameConfigRejectsInvalidCharacterCatalog`은 fresh `StaticGameConfig()`마다 다음 mutation을 하나씩 적용하고 오류 문자열에 `character` 또는 `player type`이 있는지 확인해요.

```go
tests := []struct {
	name   string
	mutate func(*GameConfig)
}{
	{"duplicate numeric", func(c *GameConfig) { c.Player.Types[1].CharacterType = CharacterTypeShelly }},
	{"duplicate string", func(c *GameConfig) { c.Player.Types[1].ID = "shelly" }},
	{"missing lily", func(c *GameConfig) { c.Player.Types = c.Player.Types[:2] }},
	{"unknown numeric", func(c *GameConfig) { c.Player.Types[2].CharacterType = CharacterType(3) }},
	{"stable mapping drift", func(c *GameConfig) { c.Player.Types[1].ID = "lily" }},
}
```

- [ ] **Step 2: Config focused tests가 현재 실패하는지 확인**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/simulation -run 'Test(ClientAndServerCharacterCatalogMappingsMatch|GameConfigPlayerTypeLookupIsIndependentOfCatalogOrder|ResolveGameConfigRejectsUnsupportedVersion|PlayerTypeConfigRejectsMissingOrNullCharacterType|ResolveGameConfigRejectsInvalidCharacterCatalog)$' -count=1
```

Expected: `undefined: CharacterType` 또는 version/catalog assertion으로 FAIL해요.

- [ ] **Step 3: CharacterType type, presence decoder, exact validation, lookup을 구현**

`internal/simulation/game_config.go`에 다음 public contract를 추가해요.

```go
const GameConfigVersion = 2

type CharacterType int

const (
	CharacterTypeShelly CharacterType = 0
	CharacterTypeColt   CharacterType = 1
	CharacterTypeLily   CharacterType = 2
)

func expectedCharacterID(characterType CharacterType) (string, bool) {
	switch characterType {
	case CharacterTypeShelly:
		return "shelly", true
	case CharacterTypeColt:
		return "colt", true
	case CharacterTypeLily:
		return "lily", true
	default:
		return "", false
	}
}

type PlayerTypeConfig struct {
	CharacterType       CharacterType `json:"characterType"`
	ID                  string        `json:"id"`
	Radius              float64       `json:"radius"`
	HP                  float64       `json:"hp"`
	Speed               float64       `json:"speed"`
	MaxAttackCharges    int           `json:"maxAttackCharges"`
	AttackRechargeTicks int           `json:"attackRechargeTicks"`
}

func (config *PlayerTypeConfig) UnmarshalJSON(data []byte) error {
	var wire struct {
		CharacterType       json.RawMessage `json:"characterType"`
		ID                  string          `json:"id"`
		Radius              float64         `json:"radius"`
		HP                  float64         `json:"hp"`
		Speed               float64         `json:"speed"`
		MaxAttackCharges    int             `json:"maxAttackCharges"`
		AttackRechargeTicks int             `json:"attackRechargeTicks"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.CharacterType) == 0 || bytes.Equal(bytes.TrimSpace(wire.CharacterType), []byte("null")) {
		return fmt.Errorf("game config player type characterType must be present")
	}
	var characterType CharacterType
	if err := json.Unmarshal(wire.CharacterType, &characterType); err != nil {
		return fmt.Errorf("decode game config player type characterType: %w", err)
	}
	*config = PlayerTypeConfig{
		CharacterType:       characterType,
		ID:                  wire.ID,
		Radius:              wire.Radius,
		HP:                  wire.HP,
		Speed:               wire.Speed,
		MaxAttackCharges:    wire.MaxAttackCharges,
		AttackRechargeTicks: wire.AttackRechargeTicks,
	}
	return nil
}

func (config GameConfig) PlayerType(characterType CharacterType) (PlayerTypeConfig, bool) {
	for _, playerType := range config.Player.Types {
		if playerType.CharacterType == characterType {
			return playerType, true
		}
	}
	return PlayerTypeConfig{}, false
}

func (config GameConfig) DefaultPlayerType() PlayerTypeConfig {
	if playerType, ok := config.PlayerType(CharacterTypeShelly); ok {
		return playerType
	}
	playerType, _ := StaticGameConfig().PlayerType(CharacterTypeShelly)
	return playerType
}
```

`ResolveGameConfig`의 첫 version check는 다음처럼 고정하고 player loop를 `validatePlayerTypeCatalog`로 옮겨요.

```go
if config.Version != GameConfigVersion {
	return GameConfig{}, fmt.Errorf("game config version must be %d", GameConfigVersion)
}
```

`validatePlayerTypeCatalog`는 `seenTypes`, `seenIDs`를 만들고 각 entry에서 `expectedCharacterID` 결과와 actual ID를 비교한 뒤 `0/1/2`가 모두 존재하는지 검사해요. 기존 radius/HP/speed/attack positive 검사도 이 helper 안에서 그대로 실행해요.

- [ ] **Step 4: Client/server/static catalog를 exact 값으로 교체**

`client-config/game-config.json`의 전체 top-level shape는 다음과 같아요.

```json
{
  "version": 2,
  "tileSize": 1.2,
  "playerRadius": 0.5,
  "playerTypes": ["default"],
  "characters": [
    {"characterType": 0, "id": "shelly", "name": "Shelly", "role": "damage_dealer"},
    {"characterType": 1, "id": "colt", "name": "Colt", "role": "damage_dealer"},
    {"characterType": 2, "id": "lily", "name": "Lily", "role": "assassin"}
  ],
  "projectileRadius": 0.3,
  "projectileTypes": ["default"]
}
```

`server-config/game-config.json`은 version을 `2`로 바꾸고 `player.types`를 다음 값으로 교체해요. Projectile, mode, map object는 byte-for-byte 의미를 유지해요.

```json
[
  {"characterType": 0, "id": "shelly", "radius": 0.5, "hp": 4000, "speed": 2, "maxAttackCharges": 4, "attackRechargeTicks": 30},
  {"characterType": 1, "id": "colt", "radius": 0.5, "hp": 3100, "speed": 2, "maxAttackCharges": 4, "attackRechargeTicks": 30},
  {"characterType": 2, "id": "lily", "radius": 0.5, "hp": 4100, "speed": 2, "maxAttackCharges": 4, "attackRechargeTicks": 30}
]
```

`StaticGameConfig()`도 같은 slice를 반환하게 하고 `internal/simulation/simulation.go`의 `DefaultPlayerHP`를 `4000.0`으로 바꿔요. `store.go`, `resolveStateGameConfig`, `resolveAssignmentGameConfig`의 `Version <= 0` shortcut은 `Version != GameConfigVersion`으로 바꿔 version 1이 우회되지 않게 해요.

- [ ] **Step 5: Embedded load fallback 테스트와 주입 seam을 추가**

`cmd/server/main.go`는 production signature를 유지하고 reader seam만 추가해요.

```go
func loadGameConfig(logger *slog.Logger) simulation.GameConfig {
	return loadGameConfigFrom(serverconfig.Reader(), logger)
}

func loadGameConfigFrom(reader io.Reader, logger *slog.Logger) simulation.GameConfig {
	gameConfig, err := simulation.LoadGameConfig(reader)
	if err != nil {
		logger.Warn("game_config_fallback", "error", err.Error())
		return simulation.StaticGameConfig()
	}
	return gameConfig
}
```

`cmd/server/main_test.go`에 malformed JSON과 version 1 두 case를 넣고 returned config가 `GameConfigVersion`, exact 3종 mapping이며 JSON log의 `msg`가 `game_config_fallback`인지 확인해요. `internal/rooms/store_config_test.go`는 invalid injected version 1 Store가 static v2 catalog와 static map을 갖는지 확인해요.

- [ ] **Step 6: Config validator를 v2와 order-independent mapping으로 갱신**

`docs-ui/scripts/validate.mjs`의 기존 version 1/default player assertion을 다음 기준으로 교체해요.

```js
const expectedCharacters = new Map([[0, "shelly"], [1, "colt"], [2, "lily"]]);
assert(clientGameConfig.version === 2, "client config version must be 2");
assert(serverGameConfig.version === 2, "server config version must be 2");
assert(JSON.stringify(clientGameConfig.playerTypes) === JSON.stringify(["default"]), "legacy playerTypes must stay [default]");
assert(clientGameConfig.playerRadius === 0.5, "legacy playerRadius must stay 0.5");
assert(Array.isArray(clientGameConfig.characters) && clientGameConfig.characters.length === 3, "client catalog must contain exactly 3 entries");
assert(Array.isArray(serverGameConfig.player.types) && serverGameConfig.player.types.length === 3, "server catalog must contain exactly 3 entries");
const clientCharacters = new Map(clientGameConfig.characters.map(({ characterType, id }) => [characterType, id]));
const serverCharacters = new Map(serverGameConfig.player.types.map(({ characterType, id }) => [characterType, id]));
assert(clientCharacters.size === clientGameConfig.characters.length, "client characterType IDs must be unique");
assert(serverCharacters.size === serverGameConfig.player.types.length, "server characterType IDs must be unique");
assert(new Set(clientGameConfig.characters.map(({ id }) => id)).size === 3, "client string IDs must be unique");
assert(new Set(serverGameConfig.player.types.map(({ id }) => id)).size === 3, "server string IDs must be unique");
assert(JSON.stringify([...clientCharacters].sort()) === JSON.stringify([...expectedCharacters].sort()), "client character mapping drift");
assert(JSON.stringify([...serverCharacters].sort()) === JSON.stringify([...expectedCharacters].sort()), "server character mapping drift");
```

Name/role은 client metadata이므로 server drift 비교에서는 제외하되, client catalog 자체는 Shelly/Colt/Lily의 exact name과 `damage_dealer/damage_dealer/assassin` role인지 검사해요. Server 각 entry의 HP/speed/radius/attack 값도 별도로 검사해요.

- [ ] **Step 7: Task 1 검증과 커밋**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/simulation ./cmd/server ./internal/rooms -run 'Test(Client|Server|Static|Resolve|PlayerType|DefaultPlayerType|LoadGameConfig|StoreConfig)' -count=1
rtk node docs-ui/scripts/validate.mjs
rtk git diff --check
```

Expected: Go focused tests와 Node validator exit `0`, `git diff --check` 출력 없음.

Commit:

```sh
rtk git add client-config/game-config.json server-config/game-config.json internal/simulation/game_config.go internal/simulation/simulation.go internal/simulation/player_assignment.go internal/simulation/game_config_test.go cmd/server/main.go cmd/server/main_test.go internal/rooms/store.go internal/rooms/store_config_test.go docs-ui/scripts/validate.mjs
rtk git commit -m "[SL-82] feat(config): CharacterType catalog와 lookup 추가" -m "- client/server config를 v2 3종 catalog로 전환" -m "- numeric mapping과 static fallback 검증 추가"
```

---

### Task 2: Simulation Character Stats와 Positive Override

**Files:**

- Modify: `internal/simulation/simulation.go:39-61,122-137,229-243`
- Modify: `internal/simulation/simulation_test.go:453-477,984-1045`
- Modify: `internal/rooms/websocket_test.go:4650-4860,5626-5631`

**Interfaces:**

- Consumes: Task 1의 `CharacterType`과 `GameConfig.PlayerType(CharacterType)`을 사용해요.
- Produces: required JSON field를 가진 `simulation.PlayerData.CharacterType`과 per-player HP/Speed/Radius normalization을 Task 3의 room projection에 제공해요. `NewStateWithConfig(players []PlayerData, config Config) *State` signature는 바꾸지 않아요.

- [ ] **Step 1: 캐릭터별 초기화와 override 보존 실패 테스트를 작성**

`internal/simulation/simulation_test.go`에 다음 table tests를 추가해요.

```go
func TestNewStateWithConfigUsesCharacterTypeStats(t *testing.T) {
	players := []PlayerData{
		{ID: "shelly", CharacterType: CharacterTypeShelly},
		{ID: "colt", CharacterType: CharacterTypeColt},
		{ID: "lily", CharacterType: CharacterTypeLily},
	}
	snapshot := NewStateWithConfig(players, Config{Game: StaticGameConfig()}).Step(nil)
	want := map[PlayerID]struct {
		characterType CharacterType
		hp            float64
	}{
		"shelly": {CharacterTypeShelly, 4000},
		"colt":   {CharacterTypeColt, 3100},
		"lily":   {CharacterTypeLily, 4100},
	}
	for _, player := range snapshot.Players {
		expected := want[player.ID]
		if player.CharacterType != expected.characterType || player.HP != expected.hp || player.Speed != 2 || player.Radius != 0.5 {
			t.Fatalf("player %q = %+v, want type=%d hp=%v speed=2 radius=0.5", player.ID, player, expected.characterType, expected.hp)
		}
	}
}

func TestNewStateWithConfigKeepsMixedCharacterStatsIndependent(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{
		{ID: "colt", CharacterType: CharacterTypeColt},
		{ID: "lily", CharacterType: CharacterTypeLily},
	}, Config{Game: StaticGameConfig()})
	first := state.Step(nil)
	first.Players[0].HP = 1
	second := state.Step(nil)
	assertPlayerHP(t, second, "colt", 3100, false)
	assertPlayerHP(t, second, "lily", 4100, false)
}

func TestNewStateWithConfigPreservesPositiveCharacterStatOverrides(t *testing.T) {
	snapshot := NewStateWithConfig([]PlayerData{{
		ID:            "fixture",
		CharacterType: CharacterTypeLily,
		HP:            77,
		Speed:         3,
		Radius:        0.25,
	}}, Config{Game: StaticGameConfig()}).Step(nil)
	got := snapshot.Players[0]
	if got.HP != 77 || got.Speed != 3 || got.Radius != 0.25 || got.CharacterType != CharacterTypeLily {
		t.Fatalf("positive fixture override changed: %+v", got)
	}
}
```

기존 attack tests에는 CharacterType `0/1/2` 각각을 넣은 subtest를 추가해 네 번 공격이 승인되고 30 tick 전에 recharge되지 않는 동일 동작을 확인해요.

- [ ] **Step 2: Simulation focused tests가 현재 실패하는지 확인**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/simulation -run 'TestNewStateWithConfig(UsesCharacterTypeStats|KeepsMixedCharacterStatsIndependent|PreservesPositiveCharacterStatOverrides)$' -count=1
```

Expected: `PlayerData.CharacterType` 미정의 또는 모든 player가 Shelly stats를 받아 FAIL해요.

- [ ] **Step 3: PlayerData identity와 per-character normalization을 구현**

`PlayerData`의 identity fields에 required field를 추가해요.

```go
type PlayerData struct {
	ID                      PlayerID     `json:"Id"`
	Team                    Team         `json:"Team"`
	Slot                    int          `json:"Slot"`
	IsBot                   bool         `json:"IsBot"`
	CharacterType           CharacterType `json:"CharacterType"`
	Pos                     Vector2      `json:"Pos"`
	MoveDir                 Vector2      `json:"MoveDir"`
	AttackDir               Vector2      `json:"AttackDir"`
	Speed                   float64      `json:"Speed"`
	Radius                  float64      `json:"Radius"`
	HP                      float64      `json:"HP"`
	PressedAttack           bool         `json:"PressedAttack"`
	IsDead                  bool         `json:"IsDead"`
	LastProcessedClientTick int64        `json:"LastProcessedClientTick"`
}
```

`normalizePlayersWithConfig`는 player마다 config를 다시 선택하고 양수 fixture를 보존해요.

```go
func normalizePlayersWithConfig(players []PlayerData, config GameConfig) []PlayerData {
	cloned := clonePlayers(players)
	for i := range cloned {
		playerType, ok := config.PlayerType(cloned[i].CharacterType)
		if !ok {
			playerType = config.DefaultPlayerType()
		}
		if cloned[i].Speed <= 0 {
			cloned[i].Speed = playerType.Speed
		}
		if cloned[i].Radius <= 0 {
			cloned[i].Radius = playerType.Radius
		}
		if cloned[i].HP <= 0 {
			cloned[i].HP = playerType.HP
		}
	}
	return cloned
}
```

Public join path의 unknown 값은 Task 3에서 mutation 전에 거부해 이 compatibility fallback에 도달하지 않아요. Attack state 초기화와 recharge는 Task 1에서 세 catalog가 모두 `4/30`임을 고정했으므로 기존 `DefaultPlayerType()` 기반 코드를 유지해요.

- [ ] **Step 4: 새 production HP와 기존 death 회귀를 분리**

`internal/rooms/websocket_test.go`의 test-only helper는 production `DefaultPlayerHP`에 의존하지 않고 명시적인 작은 HP를 사용해요.

```go
const combatRegressionPlayerHP = 100.0

func fastRechargeGameConfig() simulation.GameConfig {
	config := singleModeGameConfig(simulation.DefaultGameModeConfig())
	for index := range config.Player.Types {
		config.Player.Types[index].HP = combatRegressionPlayerHP
		config.Player.Types[index].AttackRechargeTicks = 1
	}
	return config
}
```

다음 세 테스트의 초기/expected HP는 `combatRegressionPlayerHP`를 사용하고 10 damage × 10 hit 구조를 유지해요.

- `TestWebSocketBroadcastsTwoPlayerMovementHitHPAndDeathSnapshots`
- `TestWebSocketSendsGameEndWinLoseAndCleansUpRoom`
- `TestWebSocketSendsDrawToBothPlayersWhenBothDieOnSameTick`

`TestWebSocketUsesClientCompatibleMessageFieldNames`의 hard-coded `"HP":100`은 default Shelly 계약인 `"HP":4000`으로 갱신하되 CharacterType wire assertion은 Task 3에서 추가해요. Production HP나 projectile damage를 테스트 편의를 위해 낮추지 않아요.

- [ ] **Step 5: Simulation과 death 회귀를 검증하고 커밋**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/simulation -count=1
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/rooms -run 'Test(WebSocketUsesClientCompatibleMessageFieldNames|WebSocketBroadcastsTwoPlayerMovementHitHPAndDeathSnapshots|WebSocketSendsGameEndWinLoseAndCleansUpRoom|WebSocketSendsDrawToBothPlayersWhenBothDieOnSameTick)$' -count=1
rtk git diff --check
```

Expected: focused tests PASS, death/GameEnd 결과와 기존 projectile history가 유지되고 whitespace 오류가 없어요.

Commit:

```sh
rtk git add internal/simulation/simulation.go internal/simulation/simulation_test.go internal/rooms/websocket_test.go
rtk git commit -m "[SL-82] feat(simulation): 캐릭터별 기본 능력치 적용" -m "- CharacterType별 HP speed radius 초기화" -m "- 양수 fixture와 기존 death 회귀 보존"
```

---

### Task 3: Join 선택부터 REST·Ready·Snapshot까지 Canonical 전파

**Files:**

- Modify: `internal/rooms/errors.go:3-18`
- Modify: `internal/rooms/messages.go:13-18,38-57,141-147,272-285,357-374`
- Modify: `internal/rooms/handler.go:115-151,274-309`
- Modify: `internal/rooms/store.go:441-676,548-555,568-676,856-889,996-1021`
- Create: `internal/rooms/character_type_test.go`
- Modify: `internal/rooms/handler_test.go:1730-2080,2473-2505`
- Modify: `internal/rooms/messages_test.go:10-120`
- Modify: `internal/rooms/logging_test.go`
- Modify: `internal/rooms/bot_participant_test.go:13-130`
- Modify: `internal/rooms/bot_fill_test.go`
- Modify: `internal/rooms/websocket_test.go:2940-3015,3896-4075,4567-4701`

**Interfaces:**

- Consumes: Task 1의 CharacterType/config lookup과 Task 2의 `PlayerData.CharacterType`을 사용해요.
- Produces: request-aware `joinMatchmakingRequest`, canonical `playerResponse.CharacterType`, required `readyEventPlayer.CharacterType`, structured compatibility warning을 REST/WS와 Task 4 문서 검증에 제공해요.

- [ ] **Step 1: Raw presence, validation matrix, mutation atomicity 실패 테스트를 작성**

새 `internal/rooms/character_type_test.go`에 다음 contract test를 작성해요. 기존 `requestWithBody`, `decodeResponse`, `assertError` helper를 재사용해요.

```go
func TestDecodeMatchmakingJoinRequestPreservesCharacterTypePresence(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantRaw string
		missing bool
	}{
		{name: "missing", body: `{"gameMode":"duel_1v1"}`, missing: true},
		{name: "null", body: `{"characterType":null}`, wantRaw: "null"},
		{name: "zero", body: `{"characterType":0}`, wantRaw: "0"},
		{name: "without game mode", body: `{"characterType":1}`, wantRaw: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := decodeMatchmakingJoinRequest(strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if tt.missing && request.CharacterType != nil {
				t.Fatalf("missing characterType = %s, want nil", request.CharacterType)
			}
			if !tt.missing && string(request.CharacterType) != tt.wantRaw {
				t.Fatalf("raw characterType = %q, want %q", request.CharacterType, tt.wantRaw)
			}
		})
	}
}

func TestMatchmakingCharacterTypeContract(t *testing.T) {
	tests := []struct {
		name string
		body string
		want simulation.CharacterType
	}{
		{name: "no body", body: "", want: simulation.CharacterTypeShelly},
		{name: "empty object", body: `{}`, want: simulation.CharacterTypeShelly},
		{name: "mode only", body: `{"gameMode":"solo"}`, want: simulation.CharacterTypeShelly},
		{name: "shelly", body: `{"characterType":0}`, want: simulation.CharacterTypeShelly},
		{name: "colt", body: `{"characterType":1}`, want: simulation.CharacterTypeColt},
		{name: "lily", body: `{"characterType":2}`, want: simulation.CharacterTypeLily},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			recorder := requestWithBody(debugHandler(t, store), http.MethodPost, "/matchmaking/join", tt.body)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			var joined matchmakingJoinResponse
			decodeResponse(t, recorder, &joined)
			if joined.Player.CharacterType != tt.want || len(joined.Room.Players) != 1 || joined.Room.Players[0].CharacterType != tt.want {
				t.Fatalf("CharacterType was not canonical: %+v", joined)
			}
			stored := store.lookupRoom(joined.Room.ID)
			stored.mu.Lock()
			got := stored.Players[0].CharacterType
			stored.mu.Unlock()
			if got != tt.want {
				t.Fatalf("stored CharacterType = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMatchmakingInvalidCharacterTypeDoesNotMutate(t *testing.T) {
	invalid := []string{"null", `"1"`, "true", `{}`, `[]`, "1.5", "-1", "3", "9223372036854775808"}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			store := NewStore(5)
			t.Cleanup(store.Close)
			recorder := requestWithBody(debugHandler(t, store), http.MethodPost, "/matchmaking/join", `{"gameMode":"duel_1v1","characterType":`+value+`}`)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			assertError(t, recorder, "invalid_character_type")
			if len(store.listRooms().Rooms) != 0 {
				t.Fatal("invalid CharacterType created a room")
			}
			store.mu.RLock()
			playerIDs, sessions := len(store.playerIDs), len(store.activeSessions)
			store.mu.RUnlock()
			if playerIDs != 0 || sessions != 0 {
				t.Fatalf("invalid CharacterType mutated IDs/sessions: %d/%d", playerIDs, sessions)
			}
		})
	}
}
```

`TestMatchmakingCharacterTypeErrorPriority`에는 다음 exact pairs를 table로 넣어요.

```go
tests := []struct{ name, body, wantCode string }{
	{"invalid game mode before character", `{"gameMode":"ranked","characterType":3}`, "invalid_game_mode"},
	{"game mode shape before character", `{"gameMode":1,"characterType":3}`, "invalid_request"},
	{"malformed before character", `{"gameMode":"duel_1v1","characterType":`, "invalid_request"},
	{"valid mode then character", `{"gameMode":"duel_1v1","characterType":3}`, "invalid_character_type"},
}
```

기존 rate-limit과 oversized tests에 invalid CharacterType을 섞어 각각 `rate_limited`, `invalid_request`가 유지되는 case를 추가해요.

- [ ] **Step 2: Rooms contract tests가 현재 실패하는지 확인**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/rooms -run 'Test(DecodeMatchmakingJoinRequestPreservesCharacterTypePresence|MatchmakingCharacterTypeContract|MatchmakingInvalidCharacterTypeDoesNotMutate|MatchmakingCharacterTypeErrorPriority)$' -count=1
```

Expected: request/response field와 error code 미정의 또는 invalid value가 기존처럼 `201`이어서 FAIL해요.

- [ ] **Step 3: Raw request와 canonical REST participant DTO를 추가**

`internal/rooms/messages.go`의 DTO를 다음처럼 바꿔요.

```go
type matchmakingJoinRequest struct {
	GameMode      string          `json:"gameMode"`
	CharacterType json.RawMessage `json:"characterType"`
}

type playerResponse struct {
	ID            string                   `json:"id"`
	Team          string                   `json:"team"`
	Slot          int                      `json:"slot"`
	IsBot         bool                     `json:"isBot"`
	CharacterType simulation.CharacterType `json:"characterType"`
}

type readyEventPlayer struct {
	ID            string                   `json:"Id"`
	Team          string                   `json:"Team"`
	Slot          int                      `json:"Slot"`
	IsBot         bool                     `json:"IsBot"`
	CharacterType simulation.CharacterType `json:"CharacterType"`
	SpawnPosition simulation.Vector2        `json:"SpawnPosition"`
}
```

Decoder의 field struct에 `CharacterType json.RawMessage`를 추가하고 request를 먼저 구성해 gameMode missing 분기에서도 raw value가 사라지지 않게 해요.

```go
var fields struct {
	GameMode      json.RawMessage `json:"gameMode"`
	CharacterType json.RawMessage `json:"characterType"`
}
if err := json.Unmarshal(rawRequest, &fields); err != nil {
	return matchmakingJoinRequest{}, ErrInvalidRequest
}
request := matchmakingJoinRequest{CharacterType: fields.CharacterType}
if fields.GameMode == nil {
	return request, nil
}
// 기존 null/type validation 뒤 request.GameMode에 unmarshal하고 반환
```

- [ ] **Step 4: Store의 mode-first resolver와 mutation 전파를 구현**

`internal/rooms/errors.go`에 다음 error를 추가해요.

```go
ErrInvalidCharacterType = errors.New("invalid character type")
```

기존 수십 개 internal test call을 유지하기 위해 `joinMatchmaking(gameMode string)`은 explicit Shelly wrapper로 남기고 HTTP path만 raw-aware method를 사용해요.

```go
type matchmakingJoinResult struct {
	Response               matchmakingJoinResponse
	CharacterTypeDefaulted bool
}

func resolveMatchmakingCharacterType(
	gameConfig simulation.GameConfig,
	raw json.RawMessage,
) (simulation.CharacterType, bool, error) {
	if len(raw) == 0 {
		return simulation.CharacterTypeShelly, true, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, ErrInvalidCharacterType
	}
	var characterType simulation.CharacterType
	if err := json.Unmarshal(raw, &characterType); err != nil {
		return 0, false, ErrInvalidCharacterType
	}
	if _, ok := gameConfig.PlayerType(characterType); !ok {
		return 0, false, ErrInvalidCharacterType
	}
	return characterType, false, nil
}

func (s *Store) joinMatchmaking(gameMode string) (matchmakingJoinResponse, error) {
	result, err := s.joinMatchmakingRequest(gameMode, json.RawMessage("0"))
	return result.Response, err
}

func (s *Store) joinMatchmakingRequest(
	gameMode string,
	rawCharacterType json.RawMessage,
) (matchmakingJoinResult, error) {
	if !s.beginMutation() {
		return matchmakingJoinResult{}, ErrInternal
	}
	var resources roomResources
	defer func() { resources.close(defaultRoomWebSocketCloseMsg) }()
	defer s.endMutation()
	s.matchmakingMu.Lock()
	defer s.matchmakingMu.Unlock()
	return s.joinMatchmakingLocked(gameMode, rawCharacterType, &resources)
}
```

`joinMatchmakingLocked`의 signature는 `func (s *Store) joinMatchmakingLocked(gameMode string, rawCharacterType json.RawMessage, resources *roomResources) (matchmakingJoinResult, error)`로 바꿔요. 먼저 `SelectMode`, 다음 `resolveMatchmakingCharacterType`을 호출하고 그 뒤에만 waiting room 탐색과 credential 발급을 시작해요. Success path는 response와 `defaulted`를 `matchmakingJoinResult`에 담아요.

다음 signatures에 검증된 type을 추가하고 생성 지점까지 넘겨요.

```go
func (s *Store) tryJoinMatchmakingRoom(room *room, credentials playerCredentials, characterType simulation.CharacterType) (matchmakingJoinResponse, roomResources, bool)
func (s *Store) createMatchmakingRoom(credentials *playerCredentials, gameConfig simulation.GameConfig, characterType simulation.CharacterType) (matchmakingJoinResponse, roomResources, error)
func (s *Store) addPlayerLocked(room *room, credentials playerCredentials, characterType simulation.CharacterType) playerSessionResponse
func (s *Store) appendParticipantLocked(room *room, playerID string, isBot bool, characterType simulation.CharacterType) playerResponse
```

`appendParticipantLocked`의 `playerResponse` literal에 `CharacterType: characterType`을 넣어요. Debug `addPlayer`, manual/automatic bot 경로는 각각 다음 상수를 명시해요.

```go
s.addPlayerLocked(room, credentials, simulation.CharacterTypeShelly)
s.appendParticipantLocked(room, id, true, simulation.CharacterTypeShelly)
```

- [ ] **Step 5: Handler error와 성공 후 compatibility warning을 연결**

Handler는 `joinMatchmakingRequest`를 호출하고 error를 기존 순서에 끼워 넣어요.

```go
result, err := store.joinMatchmakingRequest(joinRequest.GameMode, joinRequest.CharacterType)
if err != nil {
	if errors.Is(err, ErrInvalidGameMode) {
		writeError(w, http.StatusBadRequest, "invalid_game_mode", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidCharacterType) {
		writeError(w, http.StatusBadRequest, "invalid_character_type", err.Error())
		return
	}
	// 기존 internal/conflict 처리 유지
}
if result.CharacterTypeDefaulted {
	store.logCharacterTypeDefaulted(result.Response.GameMode)
}
writeJSON(w, http.StatusCreated, result.Response)
```

Store method의 deferred locks가 모두 풀린 뒤 handler가 호출할 warning helper를 추가해요.

```go
func (s *Store) logCharacterTypeDefaulted(gameMode string) {
	s.logger.Warn(
		"character_type_defaulted",
		"event", "character_type_defaulted",
		"game_mode", gameMode,
	)
}
```

`internal/rooms/logging_test.go`의 `TestCharacterTypeDefaultWarningOnlyForSuccessfulMissingJoin`은 다음을 검증해요.

- missing successful human join: WARN 정확히 1회, `event`와 `game_mode`만 custom attrs로 존재
- explicit `0/1/2`: warning 0회
- invalid mode/character, room cap, credential failure: warning 0회
- debug player, manual bot, automatic bot fill: warning 0회
- log에 `sessionToken`, `webSocketPath`, `token`, raw body, client IP가 없음

- [ ] **Step 6: Ready와 simulation projection을 canonical participant에 연결**

`readyEventPlayers`와 `simulationPlayers` signature는 유지하고 field만 복사해요.

```go
func readyEventPlayers(players []playerResponse, gameConfig simulation.GameConfig) []readyEventPlayer {
	spawnedPlayers := simulationPlayers(players, gameConfig)
	result := make([]readyEventPlayer, 0, len(spawnedPlayers))
	for _, player := range spawnedPlayers {
		result = append(result, readyEventPlayer{
			ID:            string(player.ID),
			Team:          string(player.Team),
			Slot:          player.Slot,
			IsBot:         player.IsBot,
			CharacterType: player.CharacterType,
			SpawnPosition: player.Pos,
		})
	}
	return result
}
```

`simulationPlayers`의 player loop는 validated type의 authoritative stats를 명시적으로 채워요.

```go
playerType, ok := gameConfig.PlayerType(player.CharacterType)
if !ok {
	playerType = gameConfig.DefaultPlayerType()
}
result = append(result, simulation.PlayerData{
	ID:            simulation.PlayerID(player.ID),
	Team:          assignment.Team,
	Slot:          assignment.Slot,
	IsBot:         player.IsBot,
	CharacterType: player.CharacterType,
	Pos:           assignment.SpawnPosition,
	HP:            playerType.HP,
	Speed:         playerType.Speed,
	Radius:        playerType.Radius,
})
```

`roomSnapshotFromSimulation`은 `PlayerData`를 그대로 전달하므로 변경하지 않아요.

- [ ] **Step 7: 전체 REST/Ready/Snapshot/bot/reconnect 회귀 테스트를 추가**

`internal/rooms/character_type_test.go`와 기존 integration tests에서 다음 named tests를 구현해요.

- `TestCharacterTypeProjectsAcrossRESTResponses`: join top-level/nested, room list/detail, debug player session, start Room response의 raw lower camel field를 검사해요.
- `TestCharacterTypeProjectsToReadyAndSimulationPlayers`: Colt human과 Shelly bot이 Ready/PlayerData에서 동일 type을 갖고 HP `3100/4000`을 받는지 검사해요.
- `TestBotAndDebugParticipantsDefaultToShelly`: manual bot, automatic bot fill, debug player 모두 explicit `0`인지 검사해요.
- `TestWebSocketReadyAndFirstSnapshotPreserveMixedCharacterTypes`: human `1/2` join이 Ready와 첫 gameplay Snapshot에 `CharacterType`과 HP `3100/4100`을 보존하는지 검사해요.
- `TestLifecycleControlSnapshotsKeepPlayersNullWithCharacterContract`: 기존 starting/started payload의 `Players: null`을 유지해요.
- `TestWebSocketReconnectPreservesCanonicalCharacterType`: Colt join의 credential로 unmatched reconnect한 뒤 Ready와 gameplay Snapshot에서도 `1`인지 검사해요.

Raw casing helper는 REST에 `characterType`만, Ready/Snapshot에 `CharacterType`만 있는지 검사하고 zero 값도 key가 존재하는지 확인해요. 기존 bot, duel/solo/team, bot fill, GameEnd tests에는 algorithm 변경 없이 CharacterType assertion만 추가해요.

- [ ] **Step 8: Rooms focused, repeat, race 검증 후 커밋**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/rooms -run 'Test(DecodeMatchmakingJoinRequestPreservesCharacterTypePresence|MatchmakingCharacterTypeContract|MatchmakingInvalidCharacterTypeDoesNotMutate|MatchmakingCharacterTypeErrorPriority|CharacterTypeDefaultWarningOnlyForSuccessfulMissingJoin|CharacterTypeProjectsAcrossRESTResponses|CharacterTypeProjectsToReadyAndSimulationPlayers|BotAndDebugParticipantsDefaultToShelly|WebSocketReadyAndFirstSnapshotPreserveMixedCharacterTypes|LifecycleControlSnapshotsKeepPlayersNullWithCharacterContract|WebSocketReconnectPreservesCanonicalCharacterType)$' -count=1
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/rooms -run 'Test(CharacterTypeProjectsToReadyAndSimulationPlayers|WebSocketReadyAndFirstSnapshotPreserveMixedCharacterTypes|WebSocketReconnectPreservesCanonicalCharacterType)$' -count=20
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test -race ./internal/rooms -run 'Test(ConcurrentMatchmakingJoinsReuseSingleModeRoom|MatchmakingInvalidCharacterTypeDoesNotMutate|WebSocketReconnectPreservesCanonicalCharacterType)$' -count=1
rtk git diff --check
```

Expected: focused/repeat/race tests PASS, 기존 same-mode room reuse와 reconnect lifecycle이 유지되고 whitespace 오류가 없어요.

Commit:

```sh
rtk git add internal/rooms/errors.go internal/rooms/messages.go internal/rooms/handler.go internal/rooms/store.go internal/rooms/character_type_test.go internal/rooms/handler_test.go internal/rooms/messages_test.go internal/rooms/logging_test.go internal/rooms/bot_participant_test.go internal/rooms/bot_fill_test.go internal/rooms/websocket_test.go
rtk git commit -m "[SL-82] feat(rooms): CharacterType 선택과 상태 전파" -m "- optional join 선택과 invalid error ordering 구현" -m "- REST Ready Snapshot과 bot reconnect identity 보존"
```

---

### Task 4: OpenAPI·AsyncAPI·Docs 계약과 Drift 검증

**Files:**

- Modify: `api/openapi.yaml:32-120,422-443,499-520,603-622`
- Modify: `api/asyncapi.yaml:2-15,155-302,511-567`
- Modify: `docs-ui/scripts/validate.mjs:120-210,285-380,580-720`
- Modify: `docs-ui/scripts/build.mjs:78-103,140-285`
- Modify: `internal/docs/docs_test.go:12-110,170-235`
- Modify: `ai-docs/api-reference.md:38-115,250-270,390-430`
- Modify: `ai-docs/api-docs.md:46-90,210-260`
- Modify: `ai-docs/protocol.md:240-340,400-415`
- Modify: `ai-docs/architecture.md:205-258`
- Modify: `ai-docs/decisions.md` (새 `ADR-0035` 추가)
- Modify: `ai-docs/project-map.md:1-40,95-160,250-280`

**Interfaces:**

- Consumes: Task 3에서 확정한 runtime casing과 required/optional 규칙을 source spec에 기록해요.
- Produces: OpenAPI의 optional join 선택·required REST participant, AsyncAPI 0.6.0의 required Ready/Snapshot identity, generated docs UI, drift를 막는 Node/Go 검증을 제공해요.

- [ ] **Step 1: CharacterType 문서 계약 실패 검증을 먼저 추가**

`docs-ui/scripts/validate.mjs`에 `validateCharacterTypeContract()`을 추가하고 기존 validator의 마지막 호출 목록에 연결해요. 이 함수는 다음을 고정해요.

```js
function validateCharacterTypeContract() {
  assertSchemaContains(openAPIText, "CharacterType", [
    "type: integer",
    "enum: [0, 1, 2]",
  ]);

  const joinRequest = extractYAMLSchema(openAPIText, "MatchmakingJoinRequest");
  const characterTypeProperty = extractSchemaProperty(joinRequest, "characterType");
  assert(characterTypeProperty.includes('$ref: "#/components/schemas/CharacterType"'), "join characterType must use the shared schema");
  assert(!topLevelRequiredFields(joinRequest).includes("characterType"), "join characterType must remain optional until SL-98");
  for (const forbidden of ["deprecated: true", "default:", "nullable:"]) {
    assert(!characterTypeProperty.includes(forbidden), `join characterType must not contain ${forbidden}`);
  }

  const playerSchema = extractYAMLSchema(openAPIText, "Player");
  assert(topLevelRequiredFields(playerSchema).filter((field) => field === "characterType").length === 1, "REST Player must require characterType exactly once");

  assert(hasLine(asyncAPIText, "  version: 0.6.0"), "AsyncAPI version must be 0.6.0");
  for (const schemaName of ["ReadyPlayer", "PlayerData"]) {
    const schema = extractYAMLSchema(asyncAPIText, schemaName);
    assert(topLevelRequiredFields(schema).filter((field) => field === "CharacterType").length === 1, `${schemaName} must require CharacterType exactly once`);
    assert(extractSchemaProperty(schema, "CharacterType").includes("enum: [0, 1, 2]"), `${schemaName}.CharacterType must use stable IDs`);
  }

  const messages = extractYAMLNamedBlock(asyncAPIText, "  messages:");
  const readyPlayers = extractYAMLSequenceObjects(extractYAMLNamedBlock(messages, "    ReadyEventMessage:"), "Players");
  const gameplayPlayers = extractYAMLSequenceObjects(extractYAMLNamedBlock(messages, "    SnapshotMessage:"), "Players");
  assertEveryYAMLPlayerHasCharacterType(readyPlayers, "AsyncAPI Ready examples");
  assertEveryYAMLPlayerHasCharacterType(gameplayPlayers, "AsyncAPI gameplay examples");

  const docsReady = extractDocsJSONExample("Ready Event");
  const docsGameplay = extractDocsJSONExample("Gameplay");
  assertEveryJSONPlayerHasCharacterType(docsReady.Players, "docs UI Ready example");
  assertEveryJSONPlayerHasCharacterType(docsGameplay.Snapshot.Players, "docs UI Gameplay example");
}
```

두 helper는 각 player에 field가 정확히 한 번 있고 값이 safe integer `0..2`인지 확인해요. `IsBot: true`면 반드시 `0`인지도 함께 검사해요.

```js
function assertEveryYAMLPlayerHasCharacterType(objects, name) {
  assert(objects.length > 0, `${name} must include player objects`);
  for (const [index, object] of objects.entries()) {
    const fields = [...object.matchAll(/^\s+CharacterType:\s+(-?\d+)$/gm)];
    assert(fields.length === 1, `${name} player ${index} must contain exactly one CharacterType`);
    const characterType = Number(fields[0][1]);
    assert(Number.isSafeInteger(characterType) && characterType >= 0 && characterType <= 2, `${name} player ${index} has invalid CharacterType`);
    if (/^\s+IsBot:\s+true$/m.test(object)) {
      assert(characterType === 0, `${name} bot player ${index} must use Shelly`);
    }
  }
}

function assertEveryJSONPlayerHasCharacterType(players, name) {
  assert(Array.isArray(players) && players.length > 0, `${name} must include players`);
  for (const [index, player] of players.entries()) {
    assert(Object.hasOwn(player, "CharacterType"), `${name} player ${index} is missing CharacterType`);
    assert(Number.isSafeInteger(player.CharacterType) && player.CharacterType >= 0 && player.CharacterType <= 2, `${name} player ${index} has invalid CharacterType`);
    if (player.IsBot === true) {
      assert(player.CharacterType === 0, `${name} bot player ${index} must use Shelly`);
    }
  }
}
```

AsyncAPI Ready와 gameplay 예시는 human에 `1`, bot에 `0`을 사용하고, 전체 source에서 `CharacterType: 2`도 별도 Lily 예시로 최소 한 번 보여 세 ID가 모두 문서화됐는지 검증해요.

`internal/docs/docs_test.go`에는 `TestHandlerServesCharacterTypeContract`를 추가해 embedded source가 다음 marker를 제공하는지 검사해요.

```go
func TestHandlerServesCharacterTypeContract(t *testing.T) {
	handler := Handler()
	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	assertStatus(t, openAPI, http.StatusOK)
	for _, marker := range []string{
		"CharacterType:",
		"required: [id, team, slot, isBot, characterType]",
		"invalid_character_type",
	} {
		assertBodyContains(t, openAPI, marker)
	}

	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	for _, marker := range []string{
		"version: 0.6.0",
		"required: [Id, Team, Slot, IsBot, CharacterType, SpawnPosition]",
		"CharacterType: 0",
		"CharacterType: 1",
		"CharacterType: 2",
	} {
		assertBodyContains(t, asyncAPI, marker)
	}

	docsUI := request(handler, http.MethodGet, "/asyncapi")
	assertStatus(t, docsUI, http.StatusOK)
	assertBodyContains(t, docsUI, `"CharacterType": 0`)
	assertBodyContains(t, docsUI, `"CharacterType": 1`)
}
```

기존 `0.5.0`, `Player`/`ReadyPlayer`/`PlayerData` required marker test도 새 contract에 맞춰 갱신해요.

- [ ] **Step 2: 문서 contract 검증이 현재 실패하는지 확인**

Run:

```sh
rtk node docs-ui/scripts/validate.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -run 'TestHandlerServes(CharacterTypeContract|BotIdentityContracts|ClientTickACKContract)$' -count=1
```

Expected: shared schema, required field, AsyncAPI `0.6.0`, example marker가 아직 없어 FAIL해요.

- [ ] **Step 3: OpenAPI join과 REST participant 계약을 갱신**

`components.schemas`에 stable wire ID를 추가해요.

```yaml
    CharacterType:
      type: integer
      description: 캐릭터의 stable numeric ID입니다. 0=Shelly, 1=Colt, 2=Lily이며 재번호화하지 않습니다.
      enum: [0, 1, 2]
      example: 1
```

`MatchmakingJoinRequest.properties`에는 optional property만 추가해요. `required`, `deprecated: true`, `default`, `nullable`은 넣지 않아요.

```yaml
        characterType:
          $ref: "#/components/schemas/CharacterType"
```

Request 설명과 examples는 다음 경계를 명시해요.

- 새 client: `characterType`을 명시하며 `0/1/2`만 허용
- legacy 생략: SL-82에서는 Shelly `0`으로 보정하고 structured warning 1회
- explicit `null`, non-integer, string/bool/object/array, unsupported integer: `400 invalid_character_type`
- SL-98에서 field를 required로 전환

기존 default/solo/team examples에는 각각 `characterType: 0/1/2`를 넣고 `legacyMissingCharacterType` example `{gameMode: duel_1v1}`을 별도로 추가해요. `201` response example은 top-level `player.characterType`과 nested `room.players[].characterType`이 같은 값을 갖는 것을 보여요. `400` examples에는 다음 case를 추가해요.

```yaml
                invalidCharacterType:
                  summary: 지원하지 않거나 잘못된 character type
                  value:
                    error:
                      code: invalid_character_type
                      message: invalid character type
```

`Player.required`는 `[id, team, slot, isBot, characterType]`으로 바꾸고 property는 shared schema를 참조해요. `APIError.code.enum`에도 `invalid_character_type`을 추가해요. OpenAPI `info.version: 0.1.0`은 유지해요.

- [ ] **Step 4: AsyncAPI 0.6.0과 Ready/Snapshot identity를 갱신**

`info.version`을 `0.6.0`으로 올리고 description에 REST에서 선택된 CharacterType이 Ready와 gameplay Snapshot까지 보존된다고 기록해요.

`ReadyPlayer`와 `PlayerData`의 required/property를 다음처럼 갱신해요.

```yaml
    ReadyPlayer:
      type: object
      required: [Id, Team, Slot, IsBot, CharacterType, SpawnPosition]
      properties:
        CharacterType:
          type: integer
          description: REST에서 확정된 stable character ID입니다. 0=Shelly, 1=Colt, 2=Lily입니다.
          enum: [0, 1, 2]

    PlayerData:
      type: object
      required: [Id, Team, Slot, IsBot, CharacterType, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead, LastProcessedClientTick]
      properties:
        CharacterType:
          type: integer
          description: Ready와 동일한 stable character ID입니다.
          enum: [0, 1, 2]
```

Ready example의 human은 Colt `CharacterType: 1`, bot 네 명은 Shelly `CharacterType: 0`으로 표시하고 Lily `CharacterType: 2`인 두 번째 human도 포함해요. Gameplay example은 Colt human에 `HP: 3100`, Shelly bot에 `HP: 4000`과 대응 CharacterType을 사용해요. Description이나 추가 example에서 Lily HP `4100`도 고정해요. Starting/started control의 `Players: null`은 그대로 유지해요.

- [ ] **Step 5: Docs UI와 source validator를 새 contract에 맞춤**

`docs-ui/scripts/build.mjs`는 다음 내용을 반영해요.

- join 설명에 optional `characterType`, stable ID 표, missing Shelly compatibility와 SL-98 전환을 추가
- Ready/Snapshot 설명에 required `CharacterType` 전파를 추가
- Ready JSON example은 Colt human `1`과 Shelly bot `0`
- Gameplay JSON example은 Colt `1/HP 3100`과 Shelly bot `0/HP 4000`
- schema 목록은 source spec에서 계속 자동 추출

`validate.mjs`의 기존 `validateBotIdentitySchemas()`와 `validateClientTickACKContract()`는 version과 required list를 `0.6.0`/CharacterType 포함 값으로 교체하고 Step 1의 `validateCharacterTypeContract()`을 호출해요. Config validation은 Task 1의 v2 mapping 검증을 유지해 API/config mapping drift를 함께 막아요.

- [ ] **Step 6: 여섯 durable 문서와 ADR을 갱신**

각 문서는 중복된 구현 세부보다 다음 책임을 중심으로 고쳐요.

| 문서 | 기록할 내용 |
| --- | --- |
| `ai-docs/api-reference.md` | join request/response example, invalid matrix와 error priority, REST lower camel, Ready/Snapshot Pascal casing |
| `ai-docs/api-docs.md` | OpenAPI optional/required 경계, AsyncAPI 0.6.0, config v2 catalog, validator 범위 |
| `ai-docs/protocol.md` | `join -> canonical room participant -> Ready -> PlayerData` 전파 흐름, bot/debug Shelly, control `Players: null` 유지 |
| `ai-docs/architecture.md` | `internal/rooms`가 선택/저장, `internal/simulation`이 stat 적용, config가 mapping의 source라는 ownership |
| `ai-docs/decisions.md` | `ADR-0035: CharacterType stable numeric contract와 단계적 required 전환` 추가 |
| `ai-docs/project-map.md` | 현재 v2 catalog와 세 캐릭터 값, SL-82 완료 surface, stale v1/default-only next-work 문구 제거, SL-98 후속 경계 |

ADR-0035에는 approved decision을 그대로 남겨요: `0/1/2`, missing만 Shelly fallback+warning, invalid 400, REST/WS casing, canonical storage, config exact mapping, attack `4/30` 유지, SL-98 required 전환. SL-83의 `3/3/2`를 SL-82 현재값처럼 쓰지 않아요.

- [ ] **Step 7: 공식 문서 검증을 실행**

Run:

```sh
rtk node docs-ui/scripts/validate.mjs
REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true rtk npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
rtk npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error
rtk node docs-ui/scripts/build.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -count=1
rtk git diff --check
```

Expected: source validator, pinned official validators, docs build, embedded docs tests가 모두 exit `0`; generated output은 git status에 나타나지 않고 whitespace 오류가 없어요.

- [ ] **Step 8: 전체 회귀를 실행하고 docs task를 커밋**

Run:

```sh
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test ./... -count=1
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build-sl82" GOMODCACHE="$PWD/.cache/go-mod" go test -race ./internal/simulation ./internal/rooms ./internal/docs -count=1
rtk git diff --check
```

Expected: 전체 Go suite와 race 대상이 PASS하고 whitespace 오류가 없어요.

Commit:

```sh
rtk git add api/openapi.yaml api/asyncapi.yaml docs-ui/scripts/validate.mjs docs-ui/scripts/build.mjs internal/docs/docs_test.go ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/decisions.md ai-docs/project-map.md
rtk git commit -m "[SL-82] docs(api): CharacterType 계약 문서화" -m "- optional join과 required REST Ready Snapshot identity 고정" -m "- AsyncAPI 0.6.0과 config/API drift 검증 추가"
```

---

### Task 5: Clean CI, PR, Linear Handoff

**Files:**

- Verify only: 전체 repository
- External update: GitHub PR, Linear `SL-82`

**Interfaces:**

- Consumes: Task 1-4의 네 개 작은 commit과 모든 validation 결과를 사용해요.
- Produces: clean CI evidence, ready-for-review PR, Linear implementation summary를 남겨요. SL-98의 strict-required 전환은 건드리지 않아요.

- [ ] **Step 1: Detached clean worktree에서 repository CI를 실행**

Root `Makefile`의 `find .`가 repository 내부 nested worktree cache까지 볼 수 있으므로, committed `HEAD`를 repository 밖 임시 worktree에 checkout해 검증해요.

```sh
sl82_ci_parent="$(mktemp -d /private/tmp/server-crawlstars-sl82-ci.XXXXXX)"
sl82_ci_root="$sl82_ci_parent/worktree"
rtk git worktree add --detach "$sl82_ci_root" HEAD
rtk mise trust "$sl82_ci_root/.mise.toml"
rtk make -C "$sl82_ci_root" ci
```

Expected: `make ci`의 format, vet, source/official docs validation, build, Go tests가 모두 PASS해요. 실패하면 원래 branch에서 수정하고 관련 task commit에 새 fix commit을 더한 뒤 새 `HEAD`로 clean CI를 다시 실행해요. 사용자 cache를 삭제하거나 Go toolchain을 바꾸지 않아요.

- [ ] **Step 2: Branch 범위와 commit chain을 최종 확인**

Run:

```sh
rtk git status --short
rtk git diff --check main...HEAD
rtk git log --oneline --decorate main..HEAD
```

Expected: working tree가 clean이고 diff check 출력이 없으며, 설계/계획 commit 뒤 Task 1-4 commit만 SL-82 범위로 보여요. SL-83 공격 동작, SL-84 스킬, SL-98 strict-required 구현은 없어야 해요.

- [ ] **Step 3: Branch를 push하고 ready-for-review PR을 생성**

```sh
rtk git push -u origin sl-82-character-type-contract
rtk gh pr create --base main --head sl-82-character-type-contract --title "[SL-82] CharacterType 선택과 상태 전파" --body "요약: CharacterType 0/1/2와 config v2를 추가하고 join 선택을 REST, Ready, Snapshot까지 보존해 캐릭터별 HP를 적용했어요. Missing은 Shelly로 호환하며 invalid_character_type과 문서 검증을 추가했어요. 검증: make ci, go test -race ./internal/simulation ./internal/rooms ./internal/docs"
```

Expected: PR이 draft가 아닌 reviewable 상태로 생성되고 base/head가 `main`/`sl-82-character-type-contract`예요.

- [ ] **Step 4: Linear SL-82를 In Review로 옮기고 구현 evidence를 남김**

Linear comment는 다음 bullet을 짧게 남겨요.

```text
- 구현: CharacterType 0/1/2, config v2, join -> REST/Ready/Snapshot canonical 전파
- 호환: missing만 Shelly 0 + warning, explicit invalid는 400 invalid_character_type
- 경계: attack 4/30 유지, 3/3/2는 SL-83, strict required는 SL-98
- 검증: make ci, race tests 통과
- PR: 직전 단계의 `gh pr create`가 반환한 실제 URL
```

Issue state를 `In Review`로 바꾸고, 위 마지막 bullet은 설명 문구가 아니라 실제 URL로 기록해요. 실제 validation 결과만 기록하며, validation이 실패했거나 PR이 생성되지 않았으면 완료/리뷰 상태를 주장하지 않아요.
