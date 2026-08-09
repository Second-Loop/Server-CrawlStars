# SL-116 Bot Behavior and A* Pathfinding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the basic nearest-enemy bot controller with deterministic dodge, explore, retreat, chase, and 4-direction A* behavior while preserving the shared `InputCommand -> State.Step` boundary.

**Architecture:** `internal/rooms` owns room-local controller state and generates one command per bot from the previous authoritative snapshot. Pure pathfinding, seed, target, retreat, and dodge helpers remain transport-free; `internal/simulation` only exports the existing mode hit rule as a pure helper. Server config v5 owns bot tuning and no public wire field changes.

**Tech Stack:** Go 1.24, standard library (`container/heap`, `crypto/sha256`, `encoding/binary`, `math`, `sort`), existing room/simulation packages, JSON server config, `make ci`.

## Global Constraints

- Start from the latest merged `origin/main`; preserve SL-102 player collision, SL-107 projectile tombstones, SL-108 cadence, and SL-110 bot character selection.
- Server config exact version becomes `5`; Client config and OpenAPI/AsyncAPI schemas do not change.
- Priority is `dodge -> explore -> retreat -> chase`; attack is evaluated independently.
- Bot commands keep `ClientTick: 0` and `PressedSkill: false`.
- Detection includes distance `<= 15`; retreat includes HP ratio `<= 0.20` and is capped at `6 world-unit`.
- A* is four-directional with `F -> H -> y -> x`; Wall and Water are blocked.
- Explore seed is SHA-256 over length-prefixed room ID, length-prefixed bot ID, and big-endian uint64 epoch.
- No Client changes, bot replacement, timers, goroutines, global RNG, learning AI, or generic scheduler.

---

## File Map

- Create `internal/rooms/bot_pathfinding.go`: grid conversion, passability, deterministic A*, retreat goal backoff.
- Create `internal/rooms/bot_pathfinding_test.go`: shortest path, tie-break, failure, retreat tests.
- Create `internal/rooms/bot_controller.go`: controller state, explore seed, target selection, dodge, behavior priority, independent attack.
- Create `internal/rooms/bot_controller_test.go`: pure controller behavior and permutation tests.
- Modify `internal/rooms/bot.go`: human/bot merge only; delegate bot generation to the controller.
- Modify `internal/rooms/store.go`: room-owned controller state and last projectile observation.
- Modify `internal/rooms/websocket.go`: pass authoritative observation into controller and retain returned projectiles.
- Modify `internal/rooms/bot_test.go`: room integration, cadence, cleanup, and shared simulation regression.
- Create `internal/simulation/combat_rules.go`: pure mode hit-eligibility helper shared by simulation and room bot dodge.
- Modify `internal/simulation/simulation.go`: delegate current projectile hit eligibility to the pure helper.
- Modify `internal/simulation/simulation_test.go`: helper/simulation parity tests.
- Modify `internal/simulation/game_config.go`, `internal/simulation/game_config_test.go`, `server-config/game-config.json`: bot config v5.
- Modify `ai-docs/architecture.md`, `ai-docs/protocol.md`, `ai-docs/api-reference.md`, `ai-docs/project-map.md`, `ai-docs/decisions.md`: durable behavior and ownership.
- Modify `docs-ui/scripts/validate.mjs`: pin server config v5 and all six bot values while preserving no-public-bot-endpoint checks.
- Check `api/openapi.yaml`, `api/asyncapi.yaml`: assert no public schema change.

### Task 1: Server Config v5 Bot Tuning

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`
- Modify: `server-config/game-config.json`

**Interfaces:**
- Produces: `GameConfig.Bot BotConfig`
- Produces: `BotConfig{DetectionRangeWorld, ExploreArrivalDistanceWorld, RetreatHPRatio, RetreatDistanceWorld, ProjectileLookAheadWorld, DodgeMarginWorld float64}`

- [ ] **Step 1: Write failing canonical and rejection tests**

Add exact assertions for version `5` and all six values, plus table mutations for zero/NaN/Infinity distance and `retreatHpRatio <= 0` or `> 1`.

```go
want := BotConfig{15, 0.25, 0.2, 6, 8, 0.35}
if got := loadServerGameConfig(t).Bot; got != want { t.Fatalf("bot config=%+v want=%+v", got, want) }
```

- [ ] **Step 2: Run the focused tests and confirm version/config failures**

Run: `mise exec -- go test ./internal/simulation -run 'Test(ServerGameConfigArtifactMatchesServerSimulationConstants|ResolveGameConfigRejectsInvalidBotConfig)' -count=1`

Expected: FAIL because `BotConfig` and v5 do not exist.

- [ ] **Step 3: Add the typed config and validation**

```go
const ServerGameConfigVersion = 5
type BotConfig struct {
    DetectionRangeWorld       float64 `json:"detectionRangeWorld"`
    ExploreArrivalDistanceWorld float64 `json:"exploreArrivalDistanceWorld"`
    RetreatHPRatio           float64 `json:"retreatHpRatio"`
    RetreatDistanceWorld     float64 `json:"retreatDistanceWorld"`
    ProjectileLookAheadWorld float64 `json:"projectileLookAheadWorld"`
    DodgeMarginWorld         float64 `json:"dodgeMarginWorld"`
}
```

Set the exact values in both `StaticGameConfig()` and the embedded JSON. Reject every non-finite/non-positive distance and require `0 < RetreatHPRatio <= 1`.

- [ ] **Step 4: Run focused config tests**

Run: `mise exec -- go test ./internal/simulation -run 'Test.*GameConfig.*(Bot|Version|Artifact)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/simulation/game_config.go internal/simulation/game_config_test.go server-config/game-config.json
git commit -m "[SL-116] feat(config): 봇 행동 설정 v5 추가" -m "- 봇 탐지와 회피 수치 추가
- v5 exact config와 invalid 값 검증"
```

### Task 2: Shared Mode Hit Eligibility

**Files:**
- Create: `internal/simulation/combat_rules.go`
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/simulation_test.go`

**Interfaces:**
- Produces: `func CanPlayerDamage(owner PlayerData, target PlayerData, mode GameModeConfig) bool`
- Consumes later: bot dodge resolves `ProjectileData.OwnerID` to an owner and calls this helper.

- [ ] **Step 1: Write a failing parity table**

Cover self, dead target, Duel enemy/ally, Team friendly-fire false/true, and Solo players. Assert the pure helper and an actual projectile hit agree.

- [ ] **Step 2: Run the focused test and confirm the missing symbol**

Run: `mise exec -- go test ./internal/simulation -run TestCanPlayerDamageMatchesProjectileHitRules -count=1`

Expected: FAIL with `undefined: CanPlayerDamage`.

- [ ] **Step 3: Implement and delegate**

```go
func CanPlayerDamage(owner, target PlayerData, mode GameModeConfig) bool {
    if owner.ID == target.ID || target.IsDead { return false }
    if mode.Rules.TeamBehavior == TeamBehaviorFreeForAll { return true }
    return mode.Rules.TeamBehavior == TeamBehaviorTwoTeams &&
        (mode.Rules.FriendlyFire || owner.Team != target.Team)
}
```

Keep `State.canOwnerHit` as the owner lookup boundary and delegate its final rule to this function.

- [ ] **Step 4: Run simulation tests**

Run: `mise exec -- go test ./internal/simulation -run 'Test(CanPlayerDamage|StepProjectileHit)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/simulation/combat_rules.go internal/simulation/simulation.go internal/simulation/simulation_test.go
git commit -m "[SL-116] refactor(simulation): 피격 가능 규칙 공유" -m "- mode별 순수 피해 eligibility 추가
- projectile 판정과 봇 회피가 같은 규칙 사용"
```

### Task 3: Deterministic Four-Direction A*

**Files:**
- Create: `internal/rooms/bot_pathfinding.go`
- Create: `internal/rooms/bot_pathfinding_test.go`

**Interfaces:**
- Produces: `type botTile struct{ x, y int }`
- Produces: `func worldToBotTile(MapData, Vector2) (botTile, bool)`
- Produces: `func nextBotPathDirection(MapData, Vector2, Vector2) (Vector2, bool)`
- Produces: `func retreatGoal(MapData, PlayerData, Vector2, float64) (Vector2, bool)`

- [ ] **Step 1: Write failing A* and retreat table tests**

Include straight shortest path, symmetric obstacle expected by `F/H/y/x`, Wall/Water blocked, disconnected goal, invalid start/goal, start=goal direct direction, 6-world raw retreat, off-center and diagonal raw retreat endpoints whose selected tile center never exceeds the requested maximum distance, and far-to-near backoff.

- [ ] **Step 2: Run the new test file**

Run: `mise exec -- go test ./internal/rooms -run 'Test(BotAStar|RetreatGoal)' -count=1`

Expected: FAIL because helpers do not exist.

- [ ] **Step 3: Implement heap ordering and grid conversion**

```go
func (h botOpenHeap) Less(i, j int) bool {
    if h[i].f != h[j].f { return h[i].f < h[j].f }
    if h[i].h != h[j].h { return h[i].h < h[j].h }
    if h[i].tile.y != h[j].tile.y { return h[i].tile.y < h[j].tile.y }
    return h[i].tile.x < h[j].tile.x
}
```

Use neighbors in a fixed four-direction array, unit G cost, Manhattan H, and reconstruct only the next tile. Retreat uses a supercover traversal, rejects candidate centers beyond the requested retreat distance, and validates bot-radius map collision from farthest tile center to nearest.

- [ ] **Step 4: Run pathfinding tests repeatedly**

Run: `mise exec -- go test ./internal/rooms -run 'Test(BotAStar|RetreatGoal)' -count=20`

Expected: PASS with identical results on every run.

- [ ] **Step 5: Commit**

```bash
git add internal/rooms/bot_pathfinding.go internal/rooms/bot_pathfinding_test.go
git commit -m "[SL-116] feat(rooms): 결정적 봇 A* 길찾기 추가" -m "- 4방향 F H y x tie-break 구현
- retreat 목표 backoff와 실패 경계 고정"
```

### Task 4: Pure Bot Controller

**Files:**
- Create: `internal/rooms/bot_controller.go`
- Create: `internal/rooms/bot_controller_test.go`
- Modify: `internal/rooms/bot.go`

**Interfaces:**
- Produces: `botControllerState` with explore epoch/destination and path cache.
- Produces: `botObservation{roomID, gameMap, gameConfig, players, projectiles, currentTick, nextAttackTicks}`.
- Produces: `func botInputForObservation(PlayerData, botObservation, *botControllerState) (InputCommand, bool)`.

- [ ] **Step 1: Write failing behavior tests**

Test detection at `15-epsilon`, `15`, `15+epsilon`; target `PlayerID` tie; priority dodge/explore/retreat/chase; attack during dodge/retreat; exact attack-ready tick; path-cache reuse; cache invalidation when start or goal changes; and `PressedSkill=false`.

- [ ] **Step 2: Write failing explore seed tests**

Assert same room/bot/epoch is stable across candidate permutations, current tile is excluded when possible, epoch increments only on selection, arrival `<=0.25` reselects next tick, and path failure discards the destination.

- [ ] **Step 3: Write failing dodge tests**

Cover own/ally/destroyed/behind/outside-8 exclusion, single and multiple vector sums, cancellation fallback by earliest collision then projectile ID, `+90/-90`, and both sides map-blocked.

- [ ] **Step 4: Run controller tests and confirm failures**

Run: `mise exec -- go test ./internal/rooms -run 'TestBotController' -count=1`

Expected: FAIL because controller types/functions are missing.

- [ ] **Step 5: Implement controller state and canonical explore seed**

```go
binary.Write(&buf, binary.BigEndian, uint32(len(roomID))); buf.WriteString(roomID)
binary.Write(&buf, binary.BigEndian, uint32(len(botID)));  buf.WriteString(string(botID))
binary.Write(&buf, binary.BigEndian, epoch)
sum := sha256.Sum256(buf.Bytes())
index := binary.BigEndian.Uint64(sum[:8]) % uint64(len(candidates))
```

Sort passable candidates row-major before indexing.

- [ ] **Step 6: Implement threat vectors and priority selection**

Sort threats by `ProjectileID`; use `simulation.CanPlayerDamage`; apply the approved forward/ray formulas and fallback. Select one movement behavior, then independently compute aim and attack range/cadence.

- [ ] **Step 7: Run controller tests with permutations and repeats**

Run: `mise exec -- go test ./internal/rooms -run 'TestBotController' -count=20`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/rooms/bot.go internal/rooms/bot_controller.go internal/rooms/bot_controller_test.go
git commit -m "[SL-116] feat(rooms): 봇 상세 행동 controller 추가" -m "- dodge explore retreat chase 우선순위 구현
- 결정적 explore seed와 독립 공격 판단 추가"
```

### Task 5: Room-Owned Observation and Controller State

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/websocket.go`
- Modify: `internal/rooms/bot.go`
- Modify: `internal/rooms/bot_test.go`

**Interfaces:**
- Room owns `botControllerStates map[PlayerID]*botControllerState` and `lastProjectiles []ProjectileData`.
- `mergedTickInputsAtTick` consumes one immutable previous-snapshot observation for every bot.

- [ ] **Step 1: Write failing room integration tests**

Assert two bots see the same previous players/projectiles regardless of slice/input order, state persists across ticks, absent bot state is removed, room cleanup releases maps, and human pending commands remain unique and authoritative.

- [ ] **Step 2: Run focused room tests**

Run: `mise exec -- go test ./internal/rooms -run 'Test(RoomBot|MergedTickInputs)' -count=1`

Expected: FAIL because room state/projectile observation is not wired.

- [ ] **Step 3: Add room fields and lifecycle initialization**

```go
botControllerStates map[simulation.PlayerID]*botControllerState
lastProjectiles     []simulation.ProjectileData
```

Initialize the map in `newRoomLocked`. Before generation, remove controller/cadence entries for bot IDs no longer present. Room removal naturally drops all remaining state.

- [ ] **Step 4: Wire the tick without changing simulation authority**

Build one observation from cloned `room.lastPlayers` and `room.lastProjectiles`, generate all bot commands, merge humans, call `State.Step` once, then clone both returned players and projectiles back to the room. Update cadence only from `snapshot.Players[].PressedAttack`.

- [ ] **Step 5: Run room and race-focused regressions**

Run: `mise exec -- go test ./internal/rooms -run 'Test(RoomBot|MergedTickInputs|RoomTickAppliesPlayerCollision)' -count=20`

Run: `mise exec -- go test -race ./internal/rooms -run 'TestRoomBot' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/rooms/store.go internal/rooms/websocket.go internal/rooms/bot.go internal/rooms/bot_test.go
git commit -m "[SL-116] feat(rooms): 봇 controller 상태를 room에 연결" -m "- 이전 authoritative projectile 관측 저장
- 모든 봇 입력을 한 snapshot에서 생성"
```

### Task 6: Documentation and Exact-HEAD Verification

**Files:**
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`
- Check: `api/openapi.yaml`
- Check: `api/asyncapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`

**Interfaces:**
- Produces: durable bot behavior/config v5 documentation with no public schema additions.

- [ ] **Step 1: Update durable docs with exact values and ownership**

Record priority, thresholds, A* tie-break, explore seed, failure behavior, room state, shared damage rule, config v5, `PressedSkill=false`, and Client/public API exclusions. Update validator assertions from server config v4 to v5 and assert the exact six `bot` fields; retain the existing assertion that OpenAPI has no bot endpoint.

- [ ] **Step 2: Run docs validation before generated build**

Run: `mise exec -- node docs-ui/scripts/validate.mjs`

Run: `REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true mise exec -- npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml`

Run: `mise exec -- npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error`

Expected: PASS; AsyncAPI info version remains `0.7.0` because no wire field changes.

- [ ] **Step 3: Build docs and run all validation**

Run: `mise exec -- node docs-ui/scripts/build.mjs`

Run: `make ci`

Expected: PASS.

- [ ] **Step 4: Commit docs**

```bash
git add ai-docs docs-ui/scripts/validate.mjs
git commit -m "[SL-116] docs(bot): 상세 행동과 A* 계약 반영" -m "- room-owned controller와 실패 규칙 문서화
- server config v5와 public API 비변경 기록"
```

- [ ] **Step 5: Verify the exact committed HEAD is clean**

Run: `git status --short`

Expected: no output.

Run: `make ci`

Expected: PASS on the exact commit intended for push/PR.

## Execution Order After SL-116

1. Merge and verify SL-116 so server config v5 is canonical.
2. Implement SL-120 from the merged v5 HEAD to introduce config v6 and the ammo snapshot contract.
3. After SL-120 merges, SL-118, SL-119, and SL-117 may run in parallel in separate worktrees.
4. Do not create a shared config version independently in the three character branches.
