# SL-83 Character Normal Attacks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement config-driven Shelly spread, Colt timed burst, and Lily deterministic melee attacks on top of the SL-82 CharacterType contract.

**Architecture:** Server config v3 owns a discriminated `normalAttack` block while client config remains v2. The simulation converts approved inputs into deterministic attack intents, emits projectiles through a sorted private scheduler, and applies Lily melee damage as a post-movement batch so input order cannot change Draw results.

**Tech Stack:** Go 1.25, standard library `math`/`sort`/`encoding/json`, JSON embedded config, Go table tests, AsyncAPI/OpenAPI docs validation, Make.

## Global Constraints

- Base branch is `sl-82-character-type-contract`; this branch remains stacked and must not merge PRs.
- Keep SL-85 skills, client implementation, final balancing, SL-98 required CharacterType, and projectile tombstone cleanup out of scope.
- Preserve `TickRate=30`, recharge `30` ticks, projectile speed `13`, radius `0.3`, and existing mode/friendly-fire rules.
- Use `rtk` for every shell command.
- Use `apply_patch` for every file edit.
- Follow TDD: observe each new test fail for the intended reason before implementing it.
- Update `ai-docs/` and AsyncAPI descriptions where attack semantics change; REST/OpenAPI fields must remain unchanged.
- Every commit title starts with `[SL-83]` and its body lists the included changes.

---

## File Structure

- Modify `internal/simulation/game_config.go`: v2/v3 version split, attack schema, exact lookup and validation.
- Modify `internal/simulation/game_config_test.go`: v3 catalog, invalid combinations, fallback and version split tests.
- Modify `internal/simulation/simulation.go`: keep runtime compiling after projectile damage ownership moves to normal attack.
- Modify `internal/simulation/player_assignment.go`: use the server config version guard.
- Modify `cmd/server/main.go` and `internal/rooms/store.go`: use the server config version guard at embedded/fallback boundaries.
- Modify `server-config/game-config.json`: authoritative v3 attack values.
- Keep `client-config/game-config.json` byte-identical.
- Modify `internal/simulation/simulation.go`: Step phase orchestration and per-character charge lookup.
- Create `internal/simulation/normal_attack.go`: attack intents, burst scheduler, sorted projectile emission, range runtime.
- Create `internal/simulation/melee.go`: pure segment geometry and batched Lily damage.
- Create `internal/simulation/normal_attack_test.go`: focused Shelly/Colt/Lily and determinism tests.
- Modify `internal/rooms/websocket_test.go`: actual character attack death/GameEnd regressions.
- Modify `docs-ui/scripts/validate.mjs`: v3 server config assertions while retaining v2 client assertions.
- Modify `api/asyncapi.yaml`: document existing wire fields' new attack semantics without adding fields.
- Modify `ai-docs/protocol.md`, `ai-docs/architecture.md`, `ai-docs/project-map.md`, `ai-docs/decisions.md`, `ai-docs/api-reference.md`: runtime and ownership documentation.

---

### Task 1: Server Config v3 Normal-Attack Contract

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/simulation_test.go`
- Modify: `internal/simulation/player_assignment.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/store_config_test.go`
- Modify: `server-config/game-config.json`
- Modify: `docs-ui/scripts/validate.mjs`
- Test: `internal/simulation/game_config_test.go`

**Interfaces:**
- Produces: `ClientGameConfigVersion`, `ServerGameConfigVersion`, `NormalAttackKind`, `NormalAttackConfig`, `ProjectileAttackConfig`, `GameConfig.ProjectileType(ProjectileType)`, `PlayerTypeConfig.NormalAttack`, and a minimal config-owned single-projectile adapter that Tasks 2-3 replace.
- Consumes: existing `CharacterType`, `GameConfig.PlayerType`, embedded loaders and `StaticGameConfig`.

- [ ] **Step 1: Write failing version and catalog tests**

Add exact assertions using these expected values:

```go
func TestLoadServerGameConfigIncludesCharacterNormalAttacks(t *testing.T) {
	config := loadServerGameConfig(t)
	if config.Version != ServerGameConfigVersion {
		t.Fatalf("server version = %d, want %d", config.Version, ServerGameConfigVersion)
	}
	wants := map[CharacterType]NormalAttackConfig{
		CharacterTypeShelly: {Kind: NormalAttackSpreadProjectile, DamagePerHit: 280, RangeTiles: 7.2, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 5, DirectionOffsetsDegrees: []float64{-12, -6, 0, 6, 12}}},
		CharacterTypeColt:   {Kind: NormalAttackBurstProjectile, DamagePerHit: 340, RangeTiles: 9, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 6, DirectionOffsetsDegrees: []float64{0}, IntervalTicks: 6}},
		CharacterTypeLily:   {Kind: NormalAttackMelee, DamagePerHit: 1100, RangeTiles: 2.2, MaxCharges: 2, RechargeTicks: 30},
	}
	for characterType, want := range wants {
		got, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing character type %d", characterType)
		}
		if !reflect.DeepEqual(got.NormalAttack, want) {
			t.Fatalf("character type %d normal attack = %#v, want %#v", characterType, got.NormalAttack, want)
		}
	}
}

func TestClientAndServerConfigVersionsAreIndependent(t *testing.T) {
	client := loadClientSharedGameConfig(t)
	server := loadServerGameConfig(t)
	if client.Version != ClientGameConfigVersion || server.Version != ServerGameConfigVersion {
		t.Fatalf("versions = client %d server %d, want %d/%d", client.Version, server.Version, ClientGameConfigVersion, ServerGameConfigVersion)
	}
}
```

- [ ] **Step 2: Run the new config tests and verify RED**

Run:

```bash
rtk make test
```

Expected: build failure for undefined version constants and `NormalAttackConfig` types.

- [ ] **Step 3: Add the schema and exact lookup interfaces**

Add these definitions and remove `MaxAttackCharges`/`AttackRechargeTicks` from `PlayerTypeConfig`:

```go
const (
	ClientGameConfigVersion = 2
	ServerGameConfigVersion = 3
)

type NormalAttackKind string

const (
	NormalAttackSpreadProjectile NormalAttackKind = "spread_projectile"
	NormalAttackBurstProjectile  NormalAttackKind = "burst_projectile"
	NormalAttackMelee            NormalAttackKind = "melee"
)

type ProjectileAttackConfig struct {
	Type                    ProjectileType `json:"type"`
	Count                   int            `json:"count"`
	DirectionOffsetsDegrees []float64      `json:"directionOffsetsDegrees"`
	IntervalTicks           int            `json:"intervalTicks"`
}

type NormalAttackConfig struct {
	Kind          NormalAttackKind        `json:"kind"`
	DamagePerHit  float64                 `json:"damagePerHit"`
	RangeTiles    float64                 `json:"rangeTiles"`
	MaxCharges    int                     `json:"maxCharges"`
	RechargeTicks int                     `json:"rechargeTicks"`
	Projectile    *ProjectileAttackConfig `json:"projectile,omitempty"`
}

func (config GameConfig) ProjectileType(projectileType ProjectileType) (ProjectileTypeConfig, bool) {
	for _, candidate := range config.Projectile.Types {
		if ProjectileType(candidate.ID) == projectileType {
			return candidate, true
		}
	}
	return ProjectileTypeConfig{}, false
}
```

`ProjectileTypeConfig` keeps `ID`, `Radius`, `Speed` and drops `Damage`. `PlayerTypeConfig` gains `NormalAttack NormalAttackConfig`.

- [ ] **Step 4: Add failing invalid-combination table tests**

Cover these exact mutations of `StaticGameConfig()` and require `ResolveGameConfig` to return an error:

```go
tests := []struct {
	name   string
	mutate func(*GameConfig)
}{
	{"unknown kind", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Kind = "unknown" }},
	{"zero damage", func(c *GameConfig) { c.Player.Types[0].NormalAttack.DamagePerHit = 0 }},
	{"nan range", func(c *GameConfig) { c.Player.Types[0].NormalAttack.RangeTiles = math.NaN() }},
	{"zero charges", func(c *GameConfig) { c.Player.Types[0].NormalAttack.MaxCharges = 0 }},
	{"zero recharge", func(c *GameConfig) { c.Player.Types[0].NormalAttack.RechargeTicks = 0 }},
	{"missing projectile", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile = nil }},
	{"melee projectile", func(c *GameConfig) { p := c.Player.Types[0].NormalAttack.Projectile; c.Player.Types[2].NormalAttack.Projectile = p }},
	{"unknown projectile reference", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.Type = "missing" }},
	{"duplicate projectile id", func(c *GameConfig) { c.Projectile.Types = append(c.Projectile.Types, c.Projectile.Types[0]) }},
	{"spread count mismatch", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.Count = 4 }},
	{"spread non-finite offset", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.DirectionOffsetsDegrees[0] = math.NaN() }},
	{"spread interval", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.IntervalTicks = 1 }},
	{"burst count", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.Count = 1 }},
	{"burst offset", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{1} }},
	{"burst non-finite offset", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.Inf(1)} }},
	{"burst interval", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.IntervalTicks = 0 }},
}
```

Positive validation/runtime 회귀로 spread의 `count=3`, offsets `[-10,0,10]`과 burst의 `count=4`, 단일 offset `0`, positive interval 조합이 `ResolveGameConfig`를 통과하고 각각 실제 3개/4개 emission을 만드는지도 확인합니다.

- [ ] **Step 5: Implement minimal validation and exact fallback values**

Validation must use `math.IsNaN`/`math.IsInf`, reject empty or duplicate projectile IDs, resolve every reference through `ProjectileType`, and enforce kind-specific shape rules. `StaticGameConfig()` and `server-config/game-config.json` must contain exactly the design values. Update every version guard so client loaders require 2 and server/state loaders require 3.

- [ ] **Step 6: Update source validator and run GREEN tests**

Update `validate.mjs` to assert client version 2 and byte-level legacy fields unchanged, while server version 3 has exact nested attacks and no player-level attack budget or projectile-level damage.

Before running GREEN, add durable runtime tests that require every projectile created for Shelly to use damage 280 and referenced type/radius/speed, and require Lily approval to create no projectile. Change `newProjectile` to return `(ProjectileData, bool)`: resolve the owner's player type, return false when `NormalAttack.Projectile == nil`, exact-lookup the referenced projectile type, and copy attack damage plus referenced type/radius/speed. This is only the compile-safe one-projectile adapter; Task 3 replaces its count/schedule behavior. Update every `GameConfigVersion` server guard in `player_assignment.go`, `cmd/server/main.go`, and `internal/rooms/store.go` plus their tests to `ServerGameConfigVersion`; client artifact tests use `ClientGameConfigVersion`.

Run:

```bash
rtk make test
rtk node docs-ui/scripts/validate.mjs
```

Expected: both commands pass.

- [ ] **Step 7: Commit Task 1**

```bash
rtk git add internal/simulation/game_config.go internal/simulation/game_config_test.go internal/simulation/simulation.go internal/simulation/simulation_test.go internal/simulation/player_assignment.go cmd/server/main.go cmd/server/main_test.go internal/rooms/store.go internal/rooms/store_config_test.go server-config/game-config.json docs-ui/scripts/validate.mjs
rtk git commit -m "[SL-83] feat(config): 캐릭터별 일반 공격 설정 추가" -m "- server config v3 normalAttack 계약 추가
- client v2와 server v3 버전 경계 분리
- invalid attack 조합과 projectile lookup 검증"
```

---

### Task 2: Per-Character Charge and Projectile Range Runtime

**Files:**
- Modify: `internal/simulation/simulation.go`
- Create: `internal/simulation/normal_attack.go`
- Create: `internal/simulation/normal_attack_test.go`
- Test: `internal/simulation/normal_attack_test.go`

**Interfaces:**
- Consumes: `PlayerTypeConfig.NormalAttack`, `GameConfig.ProjectileType` from Task 1.
- Produces: `projectileRuntime`, `projectileEmission`, `State.emitProjectiles`, `State.resolvedTileSize`, per-character charge/recharge.

- [ ] **Step 1: Write failing charge and mapless range tests**

Add table tests that attack until exhaustion through `Step` and assert configured charge counts `Shelly=3`, `Colt=3`, and `Lily=2`. Colt non-overlap requires the burst state introduced by Task 3 and is asserted there. Also test raw initial capacity through the package-private state:

```go
func TestAttackStateUsesCharacterChargeCapacity(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{{ID: "s", CharacterType: CharacterTypeShelly}, {ID: "c", CharacterType: CharacterTypeColt}, {ID: "l", CharacterType: CharacterTypeLily}}, Config{})
	wants := map[PlayerID]int{"s": 3, "c": 3, "l": 2}
	for id, want := range wants {
		if got := state.attackStates[id].charges; got != want {
			t.Fatalf("%s charges = %d, want %d", id, got, want)
		}
	}
}

func TestResolvedTileSizeFallsBackForMaplessState(t *testing.T) {
	state := NewStateWithConfig([]PlayerData{{ID: "s", CharacterType: CharacterTypeShelly}}, Config{})
	if got := state.resolvedTileSize(); got != TileSize {
		t.Fatalf("tile size = %v, want %v", got, TileSize)
	}
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(AttackStateUsesCharacterChargeCapacity|ResolvedTileSizeFallsBackForMaplessState)' -count=1
```

Expected: charge assertion fails with 4 or compilation fails because `resolvedTileSize` is missing.

- [ ] **Step 3: Add private runtime types and per-character recharge**

Create `normal_attack.go` with these exact private types:

```go
type projectileRuntime struct {
	maxDistance float64
	moved       float64
}

type projectileEmission struct {
	ownerID       PlayerID
	direction     Vector2
	attack        NormalAttackConfig
	projectile    ProjectileTypeConfig
	projectileType ProjectileType
	ordinal       int
	snapshotTick  Tick
}

type burstState struct {
	direction      Vector2
	attack         NormalAttackConfig
	activationTick Tick
	nextOrdinal    int
}
```

Extend `State` with `burstStates map[PlayerID]burstState` and `projectileRuntime map[ProjectileID]projectileRuntime`. Initialize each attack state from that player's resolved `NormalAttack.MaxCharges`. Recharge must resolve each player by ID and CharacterType rather than use `DefaultPlayerType()`.

Implement the fallback exactly:

```go
func (s *State) resolvedTileSize() float64 {
	if s.gameMap.TileSize > 0 {
		return s.gameMap.TileSize
	}
	if s.gameConfig.Tile.Size > 0 {
		return s.gameConfig.Tile.Size
	}
	return TileSize
}
```

- [ ] **Step 4: Write failing projectile range tests**

Create one-projectile test config with speed 1, radius 0.1, damage 10, range 1 tile and tile size 1. Assert:

- snapshot 1 spawns at owner position;
- snapshot 2 moves one tick duration;
- the final movement clamps exactly to distance 1;
- a target tangent at the endpoint is hit before expiry;
- a miss at the endpoint produces `IsDestroyed=true`;
- mapless config uses 1.2 rather than 0.

- [ ] **Step 5: Implement range-aware movement and sorted emission**

`emitProjectiles` sorts by `ownerID`, then `ordinal`, creates IDs with the existing `projectile-<snapshot tick>-<owner>-<global seq>` form, copies referenced `Type/Radius/Speed` plus attack damage, and stores `rangeTiles * resolvedTileSize()`.

In `moveProjectiles`, compute `stepDistance`, clamp it to `maxDistance-moved`, update position, run wall/boundary then player hit, and mark a surviving projectile destroyed when no range remains. Delete private runtime for destroyed projectiles while retaining public tombstones.

- [ ] **Step 6: Run focused and existing projectile tests**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(AttackState|ResolvedTileSize|ProjectileRange|Step.*Projectile)' -count=1
```

Expected: pass.

- [ ] **Step 7: Commit Task 2**

```bash
rtk git add internal/simulation/simulation.go internal/simulation/normal_attack.go internal/simulation/normal_attack_test.go
rtk git commit -m "[SL-83] feat(simulation): 공격 charge와 사거리 runtime 분리" -m "- CharacterType별 charge와 recharge 적용
- projectile emission과 최대 이동 거리 추적
- mapless tile size fallback 및 endpoint hit 검증"
```

---

### Task 3: Shelly Spread and Colt Burst Scheduler

**Files:**
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/normal_attack.go`
- Modify: `internal/simulation/normal_attack_test.go`
- Test: `internal/simulation/normal_attack_test.go`

**Interfaces:**
- Consumes: Task 2 emission and charge helpers.
- Produces: `attackIntent`, `State.approveProjectileAttack`, `State.collectDueBurstEmissions`, deterministic Step attack phase.

- [ ] **Step 1: Write failing Shelly exact-angle test**

Attack once at direction `(1,0)` and assert five projectiles in ID order, each at the post-movement owner position, each damage 280/range runtime 8.64, and directions equal `cos/sin` of `-12,-6,0,6,12` degrees within `1e-12`.

- [ ] **Step 2: Write failing Colt scheduler table test**

For activation snapshot 1, step without further attack input and assert total projectile counts only change at snapshots `1,7,13,19,25,31`. Change movement and `AttackDir` between steps; assert each emission uses current post-movement position but original activation direction.

Add exact cases:

```go
tests := []struct {
	name             string
	inputTick        Tick
	wantPressed      bool
	wantChargeChange bool
}{
	{"activation", 1, true, true},
	{"mid burst", 7, false, false},
	{"last emission", 31, false, false},
	{"next tick", 32, true, true},
}
```

Also assert valid `ClientTick` inputs during the burst update `LastProcessedClientTick`.

- [ ] **Step 3: Run focused tests and verify RED**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(Shelly|Colt)' -count=1
```

Expected: Shelly creates one projectile and Colt has no scheduled emissions.

- [ ] **Step 4: Refactor input processing to return an attack intent**

Use these exact interfaces:

```go
type attackIntent struct {
	playerIndex int
	owner       PlayerData
	direction   Vector2
	attack      NormalAttackConfig
}

func (s *State) applyInput(input InputCommand) (attackIntent, bool)
func (s *State) approveProjectileAttack(intent attackIntent, snapshotTick Tick) []projectileEmission
func (s *State) collectDueBurstEmissions(snapshotTick Tick) []projectileEmission
```

`applyInput` preserves validation, ACK and X/Y movement, but only returns an intent for `PressedAttack=true` and non-zero normalized direction. Approval checks active burst before consuming charge.

- [ ] **Step 5: Implement pattern execution and deterministic Step phase**

- Spread: rotate the fixed direction once per configured offset and return all emissions with ordinals matching config order.
- Burst: create a `burstState` only after one charge is consumed. `collectDueBurstEmissions` emits when `snapshotTick == activationTick + Tick(nextOrdinal*intervalTicks)`.
- Keep a burst active through ordinal 5 input processing, emit ordinal 5 at `A+30`, then delete it. New activation is possible at `A+31`.
- Cancel future burst state after existing projectile death, but retain due emission created before same-phase melee damage.
- Sort the combined immediate/due slice before assigning IDs.

- [ ] **Step 6: Add death cancellation and two-player determinism tests**

Assert an existing projectile killing Colt before input prevents the due shot and deletes the burst. Assert already-fired bullets remain. Run two Colt states with reversed input slice order and compare complete snapshots including projectile IDs.

- [ ] **Step 7: Run repeat tests**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(Shelly|Colt|NormalAttackInputOrder)' -count=100
```

Expected: pass on all 100 repetitions.

- [ ] **Step 8: Commit Task 3**

```bash
rtk git add internal/simulation/simulation.go internal/simulation/normal_attack.go internal/simulation/normal_attack_test.go
rtk git commit -m "[SL-83] feat(simulation): 산탄과 non-overlap 연사 적용" -m "- Shelly offset 기반 5발 산탄 생성
- Colt 6발 tick scheduler와 사망 취소 구현
- input 순서와 projectile ID 결정성 검증"
```

---

### Task 4: Lily Centerline Melee and Batched Damage

**Files:**
- Create: `internal/simulation/melee.go`
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/normal_attack.go`
- Modify: `internal/simulation/normal_attack_test.go`
- Test: `internal/simulation/normal_attack_test.go`

**Interfaces:**
- Consumes: Task 3 attack intents and mode hit eligibility.
- Produces: `meleeIntent`, `segmentCircleHit`, `segmentAABBHit`, `State.applyMeleeIntents`.

- [ ] **Step 1: Write failing pure geometry table tests**

Use exact unit geometry cases:

```go
func TestSegmentCircleHit(t *testing.T) {
	tests := []struct {
		name string
		end Vector2
		center Vector2
		radius float64
		want bool
	}{
		{"inside", Vector2{X: 2}, Vector2{X: 1}, 0.25, true},
		{"endpoint tangent", Vector2{X: 1}, Vector2{X: 1.5}, 0.5, true},
		{"lateral tangent", Vector2{X: 2}, Vector2{X: 1, Y: 0.5}, 0.5, true},
		{"epsilon outside", Vector2{X: 2}, Vector2{X: 1, Y: 0.500001}, 0.5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := segmentCircleHit(Vector2{}, tt.end, tt.center, tt.radius)
			if got != tt.want {
				t.Fatalf("hit = %t, want %t", got, tt.want)
			}
		})
	}
}
```

Add slab-based segment/AABB cases for hit, miss, start inside, and corner tangent.

- [ ] **Step 2: Run geometry tests and verify RED**

```bash
rtk mise exec -- go test ./internal/simulation -run 'TestSegment' -count=1
```

Expected: undefined geometry functions.

- [ ] **Step 3: Implement pure segment geometry**

Implement `segmentCircleHit(start,end,center,radius) (float64,bool)` with a quadratic returning first normalized `t` in `[0,1]`. Implement `segmentAABBHit(start,end,min,max) (float64,bool)` with the slab method and no allocation. Exact contact returns a hit.

- [ ] **Step 4: Write failing Lily behavior tests**

Cover exact cases:

- target inside 2.2 tiles, just outside, endpoint tangent, lateral tangent;
- Wall before target blocks, Wall behind target does not, equal contact lets Wall win;
- Bush and Water do not block;
- boundary truncates the centerline;
- owner/dead/ally exclusions and friendly-fire modes;
- first canonical `State.players` target wins even if a later target is geometrically nearer;
- miss and wall block still set `PressedAttack=true` and consume one charge;
- movement and all inputs finish before target selection;
- two 1100-HP Lily players kill each other with reversed input order and identical snapshot.

- [ ] **Step 5: Implement batched melee damage**

Add exact interfaces:

```go
type meleeIntent struct {
	ownerIndex int
	origin     Vector2
	direction  Vector2
	attack     NormalAttackConfig
}

func (s *State) applyMeleeIntents(intents []meleeIntent)
func (s *State) firstMeleeTarget(intent meleeIntent, players []PlayerData) int
func (s *State) firstBlockingSegmentT(start, end Vector2) float64
```

Clone post-movement players once. For each intent, compute the wall/boundary cutoff, iterate that clone in index order, require target intersection `t < blockingT`, and add damage to a `[]float64` indexed by canonical player order. Apply accumulated damage only after all targets are selected. Refactor projectile eligibility to owner-based `canOwnerHit(ownerID,target)` and reuse it.

- [ ] **Step 6: Run Lily and existing death tests**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(Lily|Segment|Step.*(Hit|Death))' -count=1
rtk mise exec -- go test ./internal/simulation -run 'TestLily.*Determin' -count=100
```

Expected: pass.

- [ ] **Step 7: Commit Task 4**

```bash
rtk git add internal/simulation/melee.go internal/simulation/simulation.go internal/simulation/normal_attack.go internal/simulation/normal_attack_test.go
rtk git commit -m "[SL-83] feat(simulation): Lily 근접 공격 일괄 판정" -m "- centerline과 wall 우선 geometry 구현
- canonical 첫 대상에 melee damage 적용
- 같은 tick 상호 사망 Draw 결정성 보존"
```

---

### Task 5: Room GameEnd Regression and Contract Documentation

**Files:**
- Modify: `internal/rooms/websocket_test.go`
- Modify: `api/asyncapi.yaml`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `docs-ui/scripts/validate.mjs`
- Test: `internal/rooms/websocket_test.go`

**Interfaces:**
- Consumes: complete Task 1-4 simulation behavior.
- Produces: verified room death/GameEnd connection and durable human/wire documentation.

- [ ] **Step 1: Add failing room regressions using real character attacks**

Add a duel test that starts two Lily players at mutual range with HP 1100, submits reciprocal attacks in one room tick, and asserts terminal snapshot has both dead followed by `GameEnd Draw`. Add a Shelly/Colt hit-death case that proves character damage reaches the existing GameEnd calculator.

- [ ] **Step 2: Run room-focused tests and verify expected state**

```bash
rtk mise exec -- go test ./internal/rooms -run 'TestWebSocket.*CharacterAttack.*GameEnd' -count=1
```

Expected before fixture support: test fails because the room helper does not place/configure the selected attack scenario; after using the existing test-only room config and spawn helpers it must exercise production simulation code without test-only damage branches.

- [ ] **Step 3: Make only fixture-level adjustments needed for production behavior**

Keep production room code unchanged unless the test exposes a real propagation defect. Use room-local server v3 config, explicit CharacterType, existing Ready/countdown helpers, and production input messages.

- [ ] **Step 4: Update docs and validators**

Record ADR decisions for config v3 ownership, Colt non-overlap schedule, Lily centerline/batched damage, range order and out-of-scope client parser work. Update AsyncAPI field descriptions for `PressedAttack`, projectile damage/type, and snapshots without adding fields. Update the five `ai-docs` current-state sections and pin decisive phrases in `validate.mjs`.

- [ ] **Step 5: Run the official docs sequence**

```bash
rtk node docs-ui/scripts/validate.mjs
rtk npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
rtk npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error
rtk node docs-ui/scripts/build.mjs
rtk mise exec -- go test ./internal/docs -count=1
```

Expected: source validator, official schema validators, build and docs handler tests pass; existing documented Redocly warnings may remain but no errors are allowed.

- [ ] **Step 6: Commit Task 5**

```bash
rtk git add internal/rooms/websocket_test.go api/asyncapi.yaml ai-docs/protocol.md ai-docs/architecture.md ai-docs/project-map.md ai-docs/decisions.md ai-docs/api-reference.md docs-ui/scripts/validate.mjs
rtk git commit -m "[SL-83] docs(combat): 일반 공격과 GameEnd 계약 검증" -m "- 실제 캐릭터 공격 death와 Draw room 회귀 추가
- AsyncAPI와 protocol 문서에 공격 tick 의미 기록
- config ownership 및 client 제외 경계 고정"
```

---

### Task 6: Full Verification, Independent Review, and Handoff

**Files:**
- Modify only files required by evidence-backed review fixes.
- Test all packages affected by Tasks 1-5.

**Interfaces:**
- Consumes: complete SL-83 branch.
- Produces: focused/race/clean-CI evidence, review-ready stacked PR and Linear In Review record.

- [ ] **Step 1: Run focused and repeat suites**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(LoadServerGameConfigIncludesCharacterNormalAttacks|AttackState|ResolvedTileSize|Shelly|Colt|Lily|Segment|ProjectileRange|NormalAttackInputOrder)' -count=1
rtk mise exec -- go test ./internal/simulation -run 'Test(Colt|Lily|NormalAttackInputOrder)' -count=100
rtk mise exec -- go test ./internal/rooms -run 'Test(WebSocket.*CharacterAttack.*GameEnd|BotBasicAttack|WebSocketSendsDraw)' -count=1
```

Expected: all pass.

- [ ] **Step 2: Run race tests outside the restricted network sandbox**

```bash
rtk mise exec -- go test -race ./internal/simulation ./internal/rooms ./internal/docs ./cmd/server -count=1
```

Expected: pass with no race reports.

- [ ] **Step 3: Create an exact-HEAD clean detached worktree and run official CI**

Resolve the committed HEAD, create a temporary detached worktree at that exact SHA, verify `git status --short` is empty, then run:

```bash
rtk make ci
```

Expected: all docs, fmt, vet, test, build and deploy checks pass. Record the exact SHA and worktree path.

- [ ] **Step 4: Dispatch independent final code review**

Give the reviewer the SL-83 Linear scope, approved design, base SHA, head SHA, complete diff, and test evidence. Require Critical/Important/Minor findings with exact files/lines. Fix only evidence-backed SL-83 defects, rerun the smallest failed check, then repeat affected focused/race/clean CI evidence.

- [ ] **Step 5: Push and create the stacked PR**

Push `sl-83-character-normal-attacks` and open a ready-for-review PR with base `sl-82-character-type-contract`, concise Korean body, SL-83 title, exact tests and explicit exclusions.

- [ ] **Step 6: Record Linear evidence and move SL-83 to In Review**

Add the approved Colt/Lily decisions, PR link, exact HEAD, focused/repeat/race/clean `make ci` results and independent review outcome to SL-83. Set status to `In Review` only after the PR is open and evidence is current.
