# SL-92 Bush and Water Collision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 서버가 client `Map_0`의 Bush(3)와 Water(4)를 그대로 전달하고, player와 projectile에 서로 다른 타일 충돌 정책을 적용하게 해요.

**Architecture:** 기존 circle-vs-tile 기하 계산과 map boundary 판정은 하나의 `collidesWithMap` 함수에 유지해요. Player와 projectile은 각각 명명된 tile predicate를 넘겨 player는 Wall/Water, projectile은 Wall만 차단하고 Bush는 둘 다 통과하게 해요. Runtime map은 SL-79 client PR #22의 merged `Map_0.json`을 `server-config/game-config.json`에 고정하고 Go/API/docs 검증으로 drift를 막아요.

**Tech Stack:** Go 1.25, table-driven Go tests, JSON runtime config, OpenAPI 3.1, AsyncAPI 3.0, Node.js docs validator

## Global Constraints

- Linear `SL-92`만 구현하고 Bush 시야, client 렌더링, pathfinding/bot AI, 여러 맵/맵 로테이션, client/server 공유 map artifact는 추가하지 않아요.
- 권위 있는 client source는 `Second-Loop/Client-CrawlStars#22`의 merged `CrawlStars/Assets/StreamingAssets/Maps/Map_0.json`(blob SHA `1d9409bdf654a04cd0d385f3f9043795d76813a9`)이에요.
- Tile 계약은 `0=Ground`, `1=Wall`, `2=SpawnPoint`, `3=Bush`, `4=Water`예요.
- Player는 Wall, Water, map boundary에 막히고 Bush를 통과해요.
- Projectile은 Wall, map boundary에 막히고 Bush와 Water를 통과해요.
- `client-config/game-config.json`과 `internal/simulation/fixtures/default-map.json`은 변경하지 않아요.
- REST/WebSocket `MapData` 계약을 바꾸므로 `api/openapi.yaml`, `api/asyncapi.yaml`, `ai-docs/api-reference.md`를 같은 변경에서 갱신해요.
- 완료 전 focused test, docs validation, `make ci`, `git diff --check`를 실행해요.

---

### Task 1: Tile enum과 map validator 확장

**Files:**
- Modify: `internal/simulation/simulation.go:79-85`
- Modify: `internal/simulation/map_loader.go:58-63`
- Test: `internal/simulation/simulation_test.go:84-139`

**Interfaces:**
- Consumes: 기존 `TileType uint8`와 `LoadMapData(io.Reader)`
- Produces: `TileBush TileType = 3`, `TileWater TileType = 4`, 0..4를 허용하는 `ResolveMapData`

- [ ] **Step 1: 3과 4를 허용하고 5는 거부하는 실패 테스트 작성**

```go
func TestLoadMapDataAcceptsBushAndWaterTiles(t *testing.T) {
	gameMap, err := LoadMapData(strings.NewReader("{" +
		"\"width\":4,\"height\":4,\"index\":0,\"maxPlayers\":2,\"tileSize\":1.2," +
		"\"map\":[[1,1,1,1],[1,3,4,1],[1,0,2,1],[1,1,1,1]]}"))
	if err != nil {
		t.Fatalf("load bush/water map data: %v", err)
	}
	if gameMap.Map[1][1] != TileBush || gameMap.Map[1][2] != TileWater {
		t.Fatalf("expected bush/water tiles, got %+v", gameMap.Map[1])
	}
}

func TestLoadMapDataRejectsTileOutsideContract(t *testing.T) {
	_, err := LoadMapData(strings.NewReader("{" +
		"\"width\":4,\"height\":4,\"index\":0,\"maxPlayers\":2,\"tileSize\":1.2," +
		"\"map\":[[1,1,1,1],[1,0,5,1],[1,0,2,1],[1,1,1,1]]}"))
	if err == nil {
		t.Fatal("expected tile value 5 to be rejected")
	}
}
```

- [ ] **Step 2: 실패 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run 'TestLoadMapData(AcceptsBushAndWaterTiles|RejectsTileOutsideContract)'`

Expected: `TestLoadMapDataAcceptsBushAndWaterTiles`가 `invalid value 3`으로 FAIL하고 value 5 rejection은 PASS해요.

- [ ] **Step 3: enum과 validator 최소 구현**

```go
const (
	TileGround     TileType = 0
	TileWall       TileType = 1
	TileSpawnPoint TileType = 2
	TileBush       TileType = 3
	TileWater      TileType = 4
)
```

```go
switch tile {
case TileGround, TileWall, TileSpawnPoint, TileBush, TileWater:
default:
	return fmt.Errorf("map tile at (%d,%d) has invalid value %d", x, y, tile)
}
```

- [ ] **Step 4: focused test 통과 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run 'TestLoadMapData(AcceptsBushAndWaterTiles|RejectsTileOutsideContract)'`

Expected: PASS

- [ ] **Step 5: Task 1 commit**

```bash
git add internal/simulation/simulation.go internal/simulation/map_loader.go internal/simulation/simulation_test.go
git commit -m "[SL-92] feat(simulation): 부쉬와 물 타일 계약 추가

- TileBush 3과 TileWater 4를 정의해요
- map validator가 0부터 4까지만 허용하게 해요
- 허용·거부 경계 테스트를 추가해요"
```

### Task 2: Entity별 collision policy 분리

**Files:**
- Modify: `internal/simulation/simulation.go:229-405`
- Test: `internal/simulation/simulation_test.go:275-895`

**Interfaces:**
- Consumes: `MapData.circleIntersectsTile`와 기존 boundary 계산
- Produces: `collidesWithMap(position Vector2, radius float64, blocksTile func(TileType) bool) bool`, `tileBlocksPlayer(TileType) bool`, `tileBlocksProjectile(TileType) bool`

- [ ] **Step 1: player Wall/Bush/Water 규칙표 실패 테스트 작성**

```go
func TestStepAppliesPlayerTileCollisionPolicy(t *testing.T) {
	tests := []struct {
		name    string
		tile    TileType
		blocked bool
	}{
		{name: "wall blocks", tile: TileWall, blocked: true},
		{name: "bush passes", tile: TileBush, blocked: false},
		{name: "water blocks", tile: TileWater, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameMap := collisionPolicyMap(tt.tile)
			center := gameMap.WorldPos(2, 2)
			step := DefaultPlayerSpeed * TickDuration
			start := Vector2{X: center.X - TileSize/2 - DefaultPlayerRadius - step, Y: center.Y}
			state := NewStateWithConfig([]PlayerData{{
				ID: PlayerID("red-1"), Team: TeamRed, Slot: 0, Pos: start,
			}}, Config{Map: gameMap})

			snapshot := state.Step([]InputCommand{{
				PlayerID: PlayerID("red-1"), MoveDir: Vector2{X: 1},
			}})
			want := Vector2{X: start.X + step, Y: start.Y}
			if tt.blocked {
				want = start
			}
			assertPlayer(t, snapshot, PlayerID("red-1"), TeamRed, 0, want)
		})
	}
}
```

- [ ] **Step 2: projectile Wall/Bush/Water 규칙표 실패 테스트 작성**

```go
func TestStepAppliesProjectileTileCollisionPolicy(t *testing.T) {
	tests := []struct {
		name      string
		tile      TileType
		destroyed bool
	}{
		{name: "wall destroys", tile: TileWall, destroyed: true},
		{name: "bush passes", tile: TileBush, destroyed: false},
		{name: "water passes", tile: TileWater, destroyed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameMap := collisionPolicyMap(tt.tile)
			center := gameMap.WorldPos(2, 2)
			step := DefaultProjectileSpeed * TickDuration
			start := Vector2{X: center.X - TileSize/2 - DefaultProjectileRadius - step + 0.001, Y: center.Y}
			state := NewStateWithConfig([]PlayerData{{
				ID: PlayerID("red-1"), Team: TeamRed, Slot: 0, Pos: start,
			}}, Config{Map: gameMap})

			state.Step([]InputCommand{{
				PlayerID: PlayerID("red-1"), AttackDir: Vector2{X: 1}, PressedAttack: true,
			}})
			snapshot := state.Step(nil)
			if len(snapshot.Projectiles) != 1 {
				t.Fatalf("expected one projectile, got %d", len(snapshot.Projectiles))
			}
			if snapshot.Projectiles[0].IsDestroyed != tt.destroyed {
				t.Fatalf("expected destroyed=%t on tile %d, got %+v", tt.destroyed, tt.tile, snapshot.Projectiles[0])
			}
		})
	}
}

func collisionPolicyMap(center TileType) MapData {
	gameMap := StaticMapFixture()
	gameMap.Map[2][2] = center
	return gameMap
}
```

- [ ] **Step 3: 실패 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run 'TestStepApplies(Player|Projectile)TileCollisionPolicy'`

Expected: player Water case가 현재 wall-only 판정 때문에 FAIL해요. Projectile Water case는 기존 동작을 고정해요.

- [ ] **Step 4: 공용 기하 계산과 entity policy 구현**

```go
func tileBlocksPlayer(tile TileType) bool {
	return tile == TileWall || tile == TileWater
}

func tileBlocksProjectile(tile TileType) bool {
	return tile == TileWall
}

func (s *State) collidesWithMap(position Vector2, radius float64, blocksTile func(TileType) bool) bool {
	if s.gameMap.Width == 0 || s.gameMap.Height == 0 {
		return false
	}
	if radius < 0 {
		radius = 0
	}
	tileSize := s.gameMap.TileSize
	halfTileSize := tileSize * 0.5
	minX := s.gameMap.WorldPos(0, 0).X - halfTileSize
	maxX := s.gameMap.WorldPos(s.gameMap.Width-1, 0).X + halfTileSize
	minY := s.gameMap.WorldPos(0, s.gameMap.Height-1).Y - halfTileSize
	maxY := s.gameMap.WorldPos(0, 0).Y + halfTileSize
	if position.X-radius < minX || position.X+radius > maxX || position.Y-radius < minY || position.Y+radius > maxY {
		return true
	}
	for y, row := range s.gameMap.Map {
		for x, tile := range row {
			if !blocksTile(tile) {
				continue
			}
			if s.gameMap.circleIntersectsTile(position, radius, x, y) {
				return true
			}
		}
	}
	return false
}
```

`applyInput`의 두 `collidesWithWall` 호출은 `collidesWithMap(..., tileBlocksPlayer)`로, `moveProjectiles`의 호출은 `collidesWithMap(..., tileBlocksProjectile)`로 바꿔요. 기존 `collidesWithWall`은 삭제해요.

- [ ] **Step 5: collision과 기존 회귀 test 통과 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run 'TestStep(Applies(Player|Projectile)TileCollisionPolicy|KeepsPlayerPositionWhenMovementHitsWall|DestroysProjectileWhenIt(HitsWall|LeavesMapBounds))'`

Expected: PASS

- [ ] **Step 6: Task 2 commit**

```bash
git add internal/simulation/simulation.go internal/simulation/simulation_test.go
git commit -m "[SL-92] feat(simulation): 엔티티별 타일 충돌 정책 분리

- player가 Wall과 Water에 막히게 해요
- projectile이 Wall만 충돌하고 Bush와 Water를 통과하게 해요
- boundary와 circle geometry 회귀를 유지해요"
```

### Task 3: Server runtime map을 client Map_0과 동기화

**Files:**
- Modify: `server-config/game-config.json:45-70`
- Modify: `internal/simulation/game_config_test.go:3-8,178-198`
- Modify: `docs-ui/scripts/validate.mjs:160-180`

**Interfaces:**
- Consumes: SL-79 merged client `Map_0.json` 20x20 row-major grid
- Produces: client와 정확히 같은 `server-config.map.map`, exact-grid Go regression, config contract docs validation

- [ ] **Step 1: exact Map_0 실패 테스트 작성**

`game_config_test.go`에 `reflect` import를 추가하고 다음 test/helper를 작성해요.

```go
func TestServerGameConfigArtifactMatchesClientMap0(t *testing.T) {
	config := loadServerGameConfig(t)
	want := expectedClientMap0()
	if !reflect.DeepEqual(config.Map.Map, want) {
		t.Fatalf("server runtime map drifted from SL-79 client Map_0:\n got: %+v\nwant: %+v", config.Map.Map, want)
	}
}

func expectedClientMap0() [][]TileType {
	return [][]TileType{
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		{1, 2, 0, 0, 3, 3, 3, 0, 0, 0, 0, 0, 0, 3, 3, 3, 0, 0, 2, 1},
		{1, 0, 0, 1, 1, 3, 3, 0, 4, 4, 0, 0, 3, 3, 1, 1, 0, 0, 0, 1},
		{1, 0, 0, 1, 0, 3, 0, 0, 4, 4, 0, 0, 0, 3, 0, 1, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 3, 3, 3, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1, 3, 3, 3, 1},
		{1, 3, 3, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 3, 3, 1},
		{1, 0, 3, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 3, 0, 1},
		{1, 0, 0, 0, 4, 4, 4, 0, 0, 0, 0, 0, 0, 4, 4, 4, 0, 0, 0, 1},
		{1, 2, 0, 0, 0, 4, 4, 0, 0, 0, 0, 0, 0, 4, 4, 0, 0, 0, 2, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 4, 4, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 1, 1, 0, 0, 0, 0, 0, 4, 4, 0, 0, 1, 1, 0, 0, 0, 1},
		{1, 2, 0, 0, 3, 3, 3, 0, 0, 0, 4, 4, 0, 3, 3, 3, 0, 0, 2, 1},
		{1, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 1},
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	}
}
```

- [ ] **Step 2: 기존 runtime map과 달라 실패하는지 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run TestServerGameConfigArtifactMatchesClientMap0`

Expected: FAIL and both grid values are printed.

- [ ] **Step 3: server config map 배열을 exact client grid로 교체**

`server-config/game-config.json`의 20개 row를 `expectedClientMap0()`과 같은 순서/값으로 교체해요. `width=20`, `height=20`, `index=0`, `maxPlayers=6`, `tileSize=1.2`는 유지해요.

- [ ] **Step 4: docs validator guard 추가**

```js
const serverMapTiles = serverGameConfig.map?.map?.flat() ?? [];
assert(serverMapTiles.includes(3), "server-config/game-config.json must include TileBush value 3");
assert(serverMapTiles.includes(4), "server-config/game-config.json must include TileWater value 4");
```

- [ ] **Step 5: config tests와 docs validator 통과 확인**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation -run 'TestServerGameConfigArtifact(IncludesRuntimeMap|MatchesClientMap0)'`

Run: `node docs-ui/scripts/validate.mjs`

Expected: both PASS

- [ ] **Step 6: Task 3 commit**

```bash
git add server-config/game-config.json internal/simulation/game_config_test.go docs-ui/scripts/validate.mjs
git commit -m "[SL-92] feat(config): 서버 Map_0를 클라이언트와 동기화

- SL-79의 20x20 Bush·Water 배열을 반영해요
- 전체 grid 동등성 회귀 테스트를 추가해요
- docs validator가 타일 3과 4를 확인하게 해요"
```

### Task 4: REST/WebSocket 계약 문서화와 전체 검증

**Files:**
- Modify: `api/openapi.yaml:460-498`
- Modify: `api/asyncapi.yaml:401-430`
- Modify: `ai-docs/api-reference.md:150-153`
- Modify: `ai-docs/api-docs.md:52-122`
- Modify: `ai-docs/project-map.md:15-24,170-205`
- Modify: `ai-docs/protocol.md:3-75`
- Modify: `ai-docs/architecture.md:104-139`
- Modify: `ai-docs/decisions.md`
- Modify: `README.md:10-18`
- Modify: `ai-docs/workflow.md:15-24`
- Modify: `docs-ui/scripts/validate.mjs`

**Interfaces:**
- Consumes: runtime TileType 0..4와 entity collision policy
- Produces: REST/WebSocket `MapData` enum 0..4, 사람이 읽는 충돌표, ADR, 문서 drift guard

- [ ] **Step 1: API schema marker 실패 검증 추가**

```js
assertSchemaContains(openAPIText, "MapData", ["enum: [0, 1, 2, 3, 4]"]);
assertSchemaContains(asyncAPIText, "MapData", ["enum: [0, 1, 2, 3, 4]"]);
```

- [ ] **Step 2: validator 실패 확인**

Run: `node docs-ui/scripts/validate.mjs`

Expected: OpenAPI `MapData must include enum: [0, 1, 2, 3, 4]`에서 FAIL해요.

- [ ] **Step 3: OpenAPI와 AsyncAPI MapData 갱신**

두 schema의 description을 다음 의미로 맞추고 tile item enum을 갱신해요.

```yaml
description: 서버 simulation이 사용하는 tile map입니다. tile 값은 0=ground, 1=wall, 2=spawnPoint, 3=bush, 4=water이며 map row는 JSON number array입니다. Player는 wall/water, projectile은 wall에 충돌하고 map boundary는 둘 다 막습니다.
```

```yaml
enum: [0, 1, 2, 3, 4]
```

- [ ] **Step 4: human docs와 ADR 갱신**

문서 전체에서 현재형 `wall collision` 요약을 entity별 정책으로 바꾸고 다음 표를 `ai-docs/api-reference.md`와 `ai-docs/architecture.md`에 넣어요.

| Tile | 값 | Player | Projectile |
| --- | ---: | --- | --- |
| Ground | 0 | 통과 | 통과 |
| Wall | 1 | 충돌 | 충돌 |
| SpawnPoint | 2 | 통과 | 통과 |
| Bush | 3 | 통과 | 통과 |
| Water | 4 | 충돌 | 통과 |
| Map boundary | - | 충돌 | 충돌 |

`ai-docs/decisions.md`에는 다음 결정의 새 ADR을 추가해요.

- Client SL-79 merged `Map_0`을 server runtime map의 값 기준으로 사용해요.
- Map artifact 공유/자동 동기화는 이 issue 범위 밖이고 exact-grid regression으로 현재 drift를 막아요.
- 기하 계산은 공유하고 entity별 blocking predicate만 분리해요.
- Bush visibility와 Water pathfinding은 client/bot 후속 범위예요.

- [ ] **Step 5: focused package, docs, schema 검증**

Run: `GOCACHE=.cache/go-build GOMODCACHE=.cache/go-mod go test ./internal/simulation`

Run: `make docs-build`

Run: `npx --yes --package @asyncapi/cli asyncapi validate api/asyncapi.yaml`

Expected: all PASS. AsyncAPI CLI는 schema error 없이 성공해요.

- [ ] **Step 6: 전체 회귀 검증**

Run: `make ci`

Run: `git diff --check`

Run: `git status --short`

Expected: `make ci`와 `git diff --check` PASS. Status에는 의도한 SL-92 파일만 남아요.

- [ ] **Step 7: Task 4 commit**

```bash
git add api/openapi.yaml api/asyncapi.yaml ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/project-map.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/decisions.md README.md ai-docs/workflow.md docs-ui/scripts/validate.mjs docs/superpowers/plans/2026-07-15-sl-92-bush-water-collision.md
git commit -m "[SL-92] docs(api): 부쉬와 물 충돌 계약 문서화

- OpenAPI와 AsyncAPI tile enum을 0부터 4까지 확장해요
- entity별 타일 충돌표와 Map_0 기준을 기록해요
- 전체 docs와 CI 검증을 완료해요"
```

### Task 5: Review handoff와 Linear/GitHub 연결

**Files:**
- No source changes expected

**Interfaces:**
- Consumes: clean validated `sl-92-bush-water-collision` branch
- Produces: ready-for-review PR, Linear implementation note, `SL-92` In Review

- [ ] **Step 1: clean branch와 commit 범위 확인**

Run: `git status --short`

Run: `git log --oneline main..HEAD`

Run: `git diff --stat main...HEAD`

Expected: working tree clean, commits and files all belong to SL-92.

- [ ] **Step 2: code review 요청 후 지적 반영**

`superpowers:requesting-code-review`로 scope, AC, regression, docs 계약을 검토하고 발견된 P1/P2를 같은 branch에서 수정해요.

- [ ] **Step 3: 최종 fresh validation**

Run: `make ci`

Run: `git diff --check main...HEAD`

Expected: PASS

- [ ] **Step 4: push와 ready-for-review PR 생성**

PR title: `[SL-92] 부쉬·물 타일 계약 및 충돌 정책 추가`

PR body:

```markdown
## 왜 해당 PR을 올렸나요?

- 서버가 SL-79의 최종 Map_0에 포함된 Bush와 Water를 읽어야 해요.
- Player와 projectile의 Water 충돌 규칙이 서로 달라 공용 wall-only 판정을 분리해야 해요.

## 무엇을 어떻게 수정했나요?

- TileBush(3), TileWater(4)와 exact Map_0 runtime config를 추가했어요.
- Player는 Wall/Water, projectile은 Wall/boundary에 충돌하도록 정책을 분리했어요.
- OpenAPI, AsyncAPI, 사람이 읽는 문서와 회귀 테스트를 갱신했어요.
```

- [ ] **Step 5: Linear 상태와 증거 반영**

`SL-92`에 PR URL, focused tests, `make ci` 결과를 comment하고 상태를 `In Review`로 바꿔요. Merge 전에는 `Done`으로 바꾸지 않아요.
