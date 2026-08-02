# SL-84 Skill Input and Server-Authoritative Cooldown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional WebSocket `PressedSkill` command and deterministic character-specific server cooldown approval, exposing required gameplay `PressedSkill` and absolute `SkillReadyTick` snapshot state without implementing skill effects.

**Architecture:** `simulation.PlayerData` remains the single cooldown owner. A small `skill.go` helper reads each character's server-config v4 `skill.cooldownTicks`, approves at `activationTick >= SkillReadyTick`, and updates the canonical player before the existing normal-attack path; an ineligible skill falls through to that path. Rooms only decode/copy the new command field, while AsyncAPI 0.7.0 and repository validators pin the public contract.

**Tech Stack:** Go 1.25, `encoding/json`, `nhooyr.io/websocket`, AsyncAPI 3.0, Node.js source validators, repository `make ci` workflow.

## Global Constraints

- Work only in `/private/tmp/server-crawlstars-sl84` on branch `sl-84-skill-input-contract`.
- Keep SL-85 effects, SL-93 checklist work, SL-100, client UI, and bot skill policy out of this plan.
- `PressedSkill` is optional; missing means `false`, while present `null` or a non-boolean value returns existing WebSocket `invalid_input` without replacing pending state.
- Reuse normalized non-zero `AttackDir`; never infer a skill attempt from direction alone.
- Treat each `PressedSkill: true` command as an independent attempt; blocked attempts do not queue or extend cooldown.
- At activation tick `A`, set `SkillReadyTick = A + C`; exact tick `A + C` is eligible.
- Eligible skill wins over `PressedAttack` and preserves attack charge; ineligible skill falls back to the existing normal-attack decision.
- Keep `PlayerData` canonical; do not add a private `skillStates` map or generalize `attackState`.
- Use cooldowns Shelly/Colt/Lily `360/390/330` ticks and server config exact version `4`.
- Keep `client-config/game-config.json` and `ClientGameConfigVersion` unchanged; SL-99 owns that separate artifact/parser contract.
- Publish AsyncAPI info version `0.7.0`; keep AsyncAPI dialect `3.0.0` and OpenAPI `0.1.0` unchanged.
- Starting/started control snapshots remain `Tick: 0`, `Players: null`, `Projectiles: null`.
- Run every implementation change test-first and commit only after its focused tests pass.

## File Structure

- Modify `internal/simulation/game_config.go`: define and validate server-only `SkillConfig`, then update static v4 fallback.
- Modify `server-config/game-config.json`: add exact v4 cooldown values.
- Modify `internal/simulation/game_config_test.go`: pin artifact, fallback, version, and invalid cooldown behavior.
- Modify `internal/simulation/simulation.go`: add command/snapshot fields, reset transient approval, and pass the output tick into combat selection.
- Create `internal/simulation/skill.go`: own the single `tryApproveSkill` mutation boundary.
- Create `internal/simulation/skill_test.go`: cover cooldown boundaries, no queue, priority, ACK, determinism, and existing burst continuity.
- Modify `internal/rooms/messages.go`: decode strict optional `PressedSkill` and expose it to pending commands.
- Modify `internal/rooms/messages_test.go`: pin missing/boolean/null/wrong-type decode behavior.
- Modify `internal/rooms/websocket.go`: copy `PressedSkill` into `simulation.InputCommand`.
- Create `internal/rooms/skill_input_test.go`: cover pending command, real WebSocket snapshot, malformed input preservation, and transient reset.
- Modify `internal/rooms/bot_test.go`: assert generated bot commands keep `PressedSkill == false`.
- Modify `api/asyncapi.yaml`: publish optional input and required gameplay state with examples.
- Modify `docs-ui/scripts/validate.mjs`: validate schema, examples, v4 cooldowns, OpenAPI exclusion, and client-config non-ownership.
- Modify `docs-ui/scripts/build.mjs`: update the human-facing embedded input/snapshot examples.
- Modify `internal/docs/docs_test.go`: pin the served AsyncAPI 0.7.0 skill contract.
- Modify `ai-docs/api-reference.md`, `ai-docs/api-docs.md`, `ai-docs/protocol.md`, `ai-docs/architecture.md`, `ai-docs/decisions.md`, and `ai-docs/project-map.md`: document runtime ownership and the SL-84/SL-85 boundary.
- Inspect but do not modify `api/openapi.yaml` and `client-config/game-config.json`.

---

### Task 1: Server Config v4 Skill Cooldowns

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`
- Modify: `server-config/game-config.json`
- Test: `cmd/server/main_test.go`
- Test: `internal/rooms/store_config_test.go`

**Interfaces:**
- Consumes: existing `PlayerTypeConfig`, `ResolveGameConfig`, `StaticGameConfig`, and embedded server config fallback.
- Produces: `SkillConfig{CooldownTicks int}`, `PlayerTypeConfig.Skill`, and exact server config version `4` for Task 2.

- [ ] **Step 1: Write failing config artifact and validation tests**

Add these tests beside the normal-attack config tests in `internal/simulation/game_config_test.go`:

```go
func TestLoadServerGameConfigIncludesCharacterSkillCooldowns(t *testing.T) {
	config := loadServerGameConfig(t)
	wants := map[CharacterType]int{
		CharacterTypeShelly: 360,
		CharacterTypeColt:   390,
		CharacterTypeLily:   330,
	}
	for characterType, want := range wants {
		playerType, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing character type %d", characterType)
		}
		if got := playerType.Skill.CooldownTicks; got != want {
			t.Fatalf("character type %d cooldown=%d, want %d", characterType, got, want)
		}
	}
}

func TestResolveGameConfigRejectsInvalidSkillCooldown(t *testing.T) {
	for _, cooldown := range []int{0, -1} {
		t.Run(fmt.Sprintf("cooldown_%d", cooldown), func(t *testing.T) {
			config := StaticGameConfig()
			config.Player.Types[0].Skill.CooldownTicks = cooldown
			_, err := ResolveGameConfig(config)
			if err == nil || !strings.Contains(err.Error(), "skill.cooldownTicks must be positive") {
				t.Fatalf("ResolveGameConfig() error=%v", err)
			}
		})
	}
}
```

Update `TestResolveGameConfigRejectsUnsupportedVersion` to expect `version must be 4`. Import `fmt` in the test file.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(LoadServerGameConfigIncludesCharacterSkillCooldowns|ResolveGameConfigRejectsInvalidSkillCooldown|ResolveGameConfigRejectsUnsupportedVersion)$' -count=1
```

Expected: compilation fails because `PlayerTypeConfig.Skill` does not exist.

- [ ] **Step 3: Add the minimal config model and validator**

In `internal/simulation/game_config.go`, make the following shape exact:

```go
const (
	ClientGameConfigVersion = 2
	ServerGameConfigVersion = 4
)

type SkillConfig struct {
	CooldownTicks int `json:"cooldownTicks"`
}

type PlayerTypeConfig struct {
	CharacterType CharacterType      `json:"characterType"`
	ID            string             `json:"id"`
	Radius        float64            `json:"radius"`
	HP            float64            `json:"hp"`
	Speed         float64            `json:"speed"`
	NormalAttack  NormalAttackConfig `json:"normalAttack"`
	Skill         SkillConfig        `json:"skill"`
}
```

Add `Skill SkillConfig` to the custom `UnmarshalJSON` wire struct and copy it into the final `PlayerTypeConfig`. In `validatePlayerTypeCatalog`, after normal-attack validation, reject `playerType.Skill.CooldownTicks <= 0` with:

```go
return fmt.Errorf(
	"game config player type %q skill.cooldownTicks must be positive",
	playerType.ID,
)
```

Set the static cooldowns to `360`, `390`, and `330` in the Shelly, Colt, and Lily entries respectively.

- [ ] **Step 4: Update the canonical server artifact**

Change `server-config/game-config.json` to version `4`. Add this sibling of `normalAttack` to each player entry, using the table exactly:

```json
"skill": {
  "cooldownTicks": 360
}
```

Use `390` for Colt and `330` for Lily. Do not edit `client-config/game-config.json`.

- [ ] **Step 5: Format and verify focused config tests GREEN**

Run:

```bash
rtk mise exec -- gofmt -w internal/simulation/game_config.go internal/simulation/game_config_test.go
rtk mise exec -- go test ./internal/simulation -run 'Test(LoadServerGameConfigIncludesCharacterSkillCooldowns|ResolveGameConfigRejectsInvalidSkillCooldown|ResolveGameConfigRejectsUnsupportedVersion|ServerGameConfigArtifact|ClientAndServerConfigVersionsAreIndependent)$' -count=1
```

Expected: all selected tests pass.

- [ ] **Step 6: Verify fallback consumers and client artifact isolation**

Run:

```bash
rtk mise exec -- go test ./cmd/server ./internal/rooms ./internal/simulation -run 'GameConfig|StoreConfig|Fallback' -count=1
rtk git diff --exit-code -- client-config/game-config.json
```

Expected: Go tests pass and the client artifact diff command prints nothing.

- [ ] **Step 7: Commit the config unit**

```bash
rtk git add internal/simulation/game_config.go internal/simulation/game_config_test.go server-config/game-config.json
rtk git commit -m "[SL-84] feat(config): 캐릭터별 스킬 쿨타임 추가" -m "- server config를 v4로 올리고 360/390/330 tick을 정의
- skill.cooldownTicks 양수 검증과 static fallback을 추가
- client config가 바뀌지 않는 독립 버전 계약을 유지"
```

### Task 2: Canonical Simulation Approval and Priority

**Files:**
- Modify: `internal/simulation/simulation.go`
- Create: `internal/simulation/skill.go`
- Create: `internal/simulation/skill_test.go`
- Test: `internal/simulation/simulation_test.go`
- Test: `internal/simulation/normal_attack_test.go`

**Interfaces:**
- Consumes: Task 1 `PlayerTypeConfig.Skill.CooldownTicks`, existing `attackIntent`, `normalAttackConfig`, and `consumeAttackCharge` behavior.
- Produces: `InputCommand.PressedSkill`, `PlayerData.PressedSkill`, `PlayerData.SkillReadyTick`, and `tryApproveSkill(playerIndex int, activationTick Tick) bool` for room transport in Task 3.

- [ ] **Step 1: Write all skill simulation tests before production code**

Create `internal/simulation/skill_test.go` in package `simulation`. Use a helper that starts from `StaticGameConfig`, replaces one character cooldown with a small positive value, calls `ResolveGameConfig`, and builds a one-player `NewStateWithConfig`.

The inclusive boundary test must assert this exact sequence for cooldown `2`:

```go
first := state.Step([]InputCommand{{
	PlayerID: "player", ClientTick: 1,
	AttackDir: Vector2{Y: 1}, PressedSkill: true,
}})
assertSkillState(t, first, "player", true, 3, 1)

second := state.Step(nil)
assertSkillState(t, second, "player", false, 3, 1)

third := state.Step([]InputCommand{{
	PlayerID: "player", ClientTick: 2,
	AttackDir: Vector2{Y: 1}, PressedSkill: true,
}})
assertSkillState(t, third, "player", true, 5, 2)
```

Add `TestStepDoesNotQueueCooldownBlockedSkill` with cooldown `3`: approve at tick `1` (`ready=4`), send true at ticks `2` and `3`, then no input at tick `4`. Expect `PressedSkill=false` on ticks `2`, `3`, and `4`, with `SkillReadyTick=4`; only a new true command at tick `5` may approve.

Add this exact priority table, setting `state.attackStates["player"]` and initial `PlayerData.SkillReadyTick` before the command:

```go
tests := []struct {
	name        string
	readyTick   Tick
	charges     int
	wantSkill   bool
	wantAttack  bool
	wantCharges int
}{
	{"ready_with_charge", 0, 1, true, false, 1},
	{"cooldown_with_charge", 2, 1, false, true, 0},
	{"ready_without_charge", 0, 0, true, false, 0},
	{"cooldown_without_charge", 2, 0, false, false, 0},
}
```

Each case sends both flags at activation tick `1` with non-zero `AttackDir`. Assert the skill result, attack result, remaining private charge, and that a skill-winning Shelly command creates zero projectiles.

Also add these focused regressions in the same file:

- `TestStepInitialSkillStateIsFalseAndReadyZero`: a new player and `Step(nil)` produce `PressedSkill=false`, `SkillReadyTick=0`.
- `TestStepCooldownBlockedSkillAcknowledgesValidClientTick`: blocked tick `2` keeps ready tick unchanged and sets `LastProcessedClientTick=2`.
- `TestStepInvalidSkillInputPreservesStateAndACK`: NaN direction, dead player, negative tick, and stale tick leave `PressedSkill=false`, ready tick, attack charge, and ACK unchanged.
- `TestStepZeroAttackDirectionDoesNotApproveSkill`: both flags with zero direction leave both approval flags false and preserve cooldown/charge.
- `TestStepSkillResultIsIndependentOfInputOrder`: two distinct players and reversed command slices produce `reflect.DeepEqual` snapshots.
- `TestStepSkillDoesNotCancelExistingColtBurst`: start a Colt normal attack, approve a skill on the next tick, and verify the scheduled emission at activation tick `A+6` still appears.
- `TestStepMissingOrFalsePressedSkillUsesExistingAttackPath`: omitted/false skill with `PressedAttack=true` keeps current normal-attack approval behavior.
- `TestSkillSnapshotDoesNotExposeMutableState`: overwrite `SkillReadyTick` in a returned snapshot and verify the next snapshot still uses the authoritative value.

- [ ] **Step 2: Run the skill tests and verify RED**

Run:

```bash
rtk mise exec -- go test ./internal/simulation -run 'TestStep.*Skill|TestStepCooldownBlockedSkillAcknowledgesValidClientTick' -count=1
```

Expected: compilation fails because the three skill fields do not exist.

- [ ] **Step 3: Add the public input and canonical player fields**

In `internal/simulation/simulation.go`, extend the structs without changing existing JSON names:

```go
type InputCommand struct {
	PlayerID      PlayerID `json:"PlayerId"`
	ClientTick    int64    `json:"ClientTick"`
	MoveDir       Vector2  `json:"MoveDir"`
	AttackDir     Vector2  `json:"AttackDir"`
	PressedAttack bool     `json:"PressedAttack"`
	PressedSkill  bool     `json:"PressedSkill"`
}

// Add beside PressedAttack and LastProcessedClientTick in PlayerData.
PressedSkill   bool `json:"PressedSkill"`
SkillReadyTick Tick `json:"SkillReadyTick"`
```

At the start of every `Step`, reset both transient flags. Keep `SkillReadyTick` untouched:

```go
for i := range s.players {
	s.players[i].PressedAttack = false
	s.players[i].PressedSkill = false
}
```

- [ ] **Step 4: Implement the single cooldown mutation helper**

Create `internal/simulation/skill.go` with only this responsibility:

```go
package simulation

func (s *State) tryApproveSkill(playerIndex int, activationTick Tick) bool {
	player := &s.players[playerIndex]
	if activationTick < player.SkillReadyTick {
		return false
	}
	playerType, ok := s.gameConfig.PlayerType(player.CharacterType)
	if !ok || playerType.Skill.CooldownTicks <= 0 {
		return false
	}
	player.PressedSkill = true
	player.SkillReadyTick = activationTick + Tick(playerType.Skill.CooldownTicks)
	return true
}
```

Do not add state maps, remaining-cooldown counters, or effect data.

- [ ] **Step 5: Integrate skill-first selection into `applyInput`**

Change `applyInput` to accept `activationTick Tick`, and call it with the already-computed `snapshotTick` from `Step`.

After movement and `AttackDir` normalization, before the existing `PressedAttack` check, add:

```go
if input.PressedSkill && attackDir != (Vector2{}) &&
	s.tryApproveSkill(i, activationTick) {
	return attackIntent{}, false
}
```

Leave the existing normal-attack block immediately after it. This makes cooldown/zero-direction skill attempts fall through while a successful skill returns before charge consumption.

- [ ] **Step 6: Format and run skill tests GREEN**

```bash
rtk mise exec -- gofmt -w internal/simulation/simulation.go internal/simulation/skill.go internal/simulation/skill_test.go
rtk mise exec -- go test ./internal/simulation -run 'TestStep.*Skill|TestStepCooldownBlockedSkillAcknowledgesValidClientTick' -count=1
```

Expected: all new skill tests pass.

- [ ] **Step 7: Run existing combat, ACK, and snapshot-isolation regressions**

```bash
rtk mise exec -- go test ./internal/simulation -run 'Test(Step|Attack|Shelly|Colt|Lily|Snapshot)' -count=1
```

Expected: existing normal attacks, charges, ACK, projectile/melee, and clone-isolation tests pass.

- [ ] **Step 8: Commit the simulation unit**

```bash
rtk git add internal/simulation/simulation.go internal/simulation/skill.go internal/simulation/skill_test.go
rtk git commit -m "[SL-84] feat(simulation): 스킬 승인과 쿨타임 판정 추가" -m "- PlayerData가 PressedSkill과 절대 SkillReadyTick을 소유
- A+C 포함 경계와 command별 비큐잉 판정을 구현
- eligible skill 우선과 일반 공격 fallback 회귀를 고정"
```

### Task 3: Room Input Decode and WebSocket Propagation

**Files:**
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/messages_test.go`
- Modify: `internal/rooms/websocket.go`
- Create: `internal/rooms/skill_input_test.go`
- Modify: `internal/rooms/bot_test.go`

**Interfaces:**
- Consumes: Task 2 `simulation.InputCommand.PressedSkill` and canonical snapshot fields.
- Produces: strict optional wire decode, whole-command pending propagation, gameplay snapshot evidence, and bot-false regression for Task 4 documentation.

- [ ] **Step 1: Write failing DTO decode tests**

Add to `internal/rooms/messages_test.go`:

```go
func TestInputMessageDecodesPressedSkillAndDefaultsMissingToFalse(t *testing.T) {
	for name, payload := range map[string]string{
		"missing": `{"PressedAttack":true}`,
		"false":   `{"PressedSkill":false}`,
		"true":    `{"PressedSkill":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input inputMessage
			if err := json.Unmarshal([]byte(payload), &input); err != nil {
				t.Fatal(err)
			}
			if input.PressedSkill != (name == "true") {
				t.Fatalf("PressedSkill=%t", input.PressedSkill)
			}
		})
	}
}

func TestInputMessageRejectsNullOrNonBooleanPressedSkill(t *testing.T) {
	for name, value := range map[string]string{
		"null": "null", "number": "1", "string": `"true"`, "object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input inputMessage
			if err := json.Unmarshal([]byte(`{"PressedSkill":`+value+`}`), &input); err == nil {
				t.Fatal("expected invalid PressedSkill")
			}
		})
	}
}
```

- [ ] **Step 2: Write failing real-WebSocket and pending-state tests**

Create `internal/rooms/skill_input_test.go` using the existing `newFakeClock`, debug-room, dial, pending, and snapshot helpers.

`TestWebSocketPressedSkillAppearsInAuthoritativeSnapshot` must:

1. Start a one-player debug room.
2. Send raw JSON with `ClientTick:1`, non-zero `AttackDir`, `PressedAttack:true`, and `PressedSkill:true`.
3. Tick once and assert the player has `PressedSkill=true`, `PressedAttack=false`, `SkillReadyTick=361`, `LastProcessedClientTick=1`, and zero projectiles.
4. Tick again without input and assert `PressedSkill=false`, `SkillReadyTick=361`.
5. Assert the payload contains PascalCase `"PressedSkill"`/`"SkillReadyTick"` and not lowercase variants.

`TestWebSocketReconnectPreservesSkillReadyTick` must approve a skill, close the started-room connection, wait for detach, reconnect with the same issued path, tick once, and assert the new session sees `PressedSkill=false` with the previous absolute `SkillReadyTick=361`. This proves reconnect reads canonical simulation state rather than a transport-local mirror.

`TestWebSocketInvalidPressedSkillPreservesPendingAndSnapshotStream` must first store a valid pending `ClientTick:12`, then send `ClientTick:13` with each invalid value from Step 1. Expect one `invalid_input` error, the tick-12 pending command unchanged, and a valid snapshot after the gameplay ticker advances.

`TestSetInputCopiesPressedSkillAsPartOfWinningCommand` must call `inputSelectionFixture`, store tick `12` with `PressedSkill:true`, overwrite it with tick `13` and `PressedSkill:false`, and assert the pending command is exactly tick `13` with false. This proves whole-command replacement rather than field merging.

In `internal/rooms/bot_test.go`, extend the existing bot-command assertions with:

```go
if bot.PressedSkill {
	t.Fatalf("server-owned bot must not request skill: %+v", bot)
}
```

- [ ] **Step 3: Run room skill tests and verify RED**

```bash
rtk mise exec -- go test ./internal/rooms -run 'Test(InputMessage.*PressedSkill|WebSocket.*PressedSkill|SetInputCopiesPressedSkill|MergedTickInputs)' -count=1
```

Expected: compilation or assertions fail because room DTOs do not carry the field.

- [ ] **Step 4: Implement strict optional decode without changing `PressedAttack`**

In `inputMessage`, add:

```go
PressedSkill bool `json:"PressedSkill"`
```

In the custom wire struct use `PressedSkill json.RawMessage`. Preserve the existing `PressedAttack bool` field. Replace the current early return for missing `ClientTick` with a conditional decode so missing legacy ticks still continue to the skill field:

```go
if len(wire.ClientTick) > 0 {
	if bytes.Equal(bytes.TrimSpace(wire.ClientTick), []byte("null")) {
		return errors.New("ClientTick must be an integer")
	}
	if err := json.Unmarshal(wire.ClientTick, &m.ClientTick); err != nil {
		return err
	}
}
```

Then decode `PressedSkill` exactly as follows:

```go
if len(wire.PressedSkill) == 0 {
	return nil
}
if bytes.Equal(bytes.TrimSpace(wire.PressedSkill), []byte("null")) {
	return errors.New("PressedSkill must be a boolean")
}
if err := json.Unmarshal(wire.PressedSkill, &m.PressedSkill); err != nil {
	return fmt.Errorf("decode PressedSkill: %w", err)
}
return nil
```

Add `fmt` to imports. Keep the existing missing/null/non-integer `ClientTick` precedence unchanged.

- [ ] **Step 5: Copy the field into the authoritative pending command**

In `Store.setInput`, add this exact field to the `simulation.InputCommand` literal:

```go
PressedSkill: input.PressedSkill,
```

No changes are needed in `bot.go`; the zero-value bool is the approved false policy.

- [ ] **Step 6: Format and run room tests GREEN**

```bash
rtk mise exec -- gofmt -w internal/rooms/messages.go internal/rooms/messages_test.go internal/rooms/skill_input_test.go internal/rooms/bot_test.go
rtk mise exec -- go test ./internal/rooms -run 'Test(InputMessage.*PressedSkill|WebSocket.*PressedSkill|SetInputCopiesPressedSkill|MergedTickInputs)' -count=1
```

Expected: all selected tests pass.

- [ ] **Step 7: Run broader WebSocket regressions**

```bash
rtk mise exec -- go test ./internal/rooms -run 'Test(WebSocket|SetInput|MergedTickInputs|BotBasicAttack)' -count=1
```

Expected: existing invalid-input, ClientTick, bot merge, snapshot, and normal-attack tests pass.

- [ ] **Step 8: Commit the room transport unit**

```bash
rtk git add internal/rooms/messages.go internal/rooms/messages_test.go internal/rooms/websocket.go internal/rooms/skill_input_test.go internal/rooms/bot_test.go
rtk git commit -m "[SL-84] feat(rooms): 스킬 입력을 simulation으로 전달" -m "- optional PressedSkill을 엄격한 boolean으로 decode
- invalid payload가 pending command와 snapshot stream을 보존
- gameplay snapshot과 bot false 정책을 WebSocket 회귀로 검증"
```

### Task 4: AsyncAPI 0.7.0 and Human Documentation

**Files:**
- Modify: `api/asyncapi.yaml`
- Inspect only: `api/openapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `docs-ui/scripts/build.mjs`
- Modify: `internal/docs/docs_test.go`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/project-map.md`

**Interfaces:**
- Consumes: Task 1 v4 values, Task 2 gameplay semantics, and Task 3 runtime wire casing/error behavior.
- Produces: AsyncAPI `0.7.0`, generated UI examples, served docs tests, ADR-0037, and source validation that prevents OpenAPI/client-config scope drift.

- [ ] **Step 1: Add failing source-validator assertions first**

In `docs-ui/scripts/validate.mjs`:

1. Add `PressedSkill` and `SkillReadyTick` to `requiredWebSocketFields`.
2. Change all current exact AsyncAPI version assertions from `0.6.0` to `0.7.0`.
3. Update the exact `PlayerData.required` marker to:

```text
required: [Id, Team, Slot, IsBot, CharacterType, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, PressedSkill, SkillReadyTick, IsDead, LastProcessedClientTick]
```

4. Call a new `validateCharacterSkillCooldownContract()` after the existing character normal-attack validator.

Implement the new validator with these exact checks:

```js
function validateCharacterSkillCooldownContract() {
  const inputSchema = extractYAMLSchema(asyncAPIText, "InputMessage");
  const inputSkill = extractSchemaProperty(inputSchema, "PressedSkill");
  assert(inputSkill.includes("type: boolean"), "InputMessage.PressedSkill must be boolean");
  assert(!topLevelRequiredFields(inputSchema).includes("PressedSkill"), "InputMessage.PressedSkill must be optional");

  const playerSchema = extractYAMLSchema(asyncAPIText, "PlayerData");
  for (const field of ["PressedSkill", "SkillReadyTick"]) {
    assert(topLevelRequiredFields(playerSchema).filter((candidate) => candidate === field).length === 1,
      `PlayerData must require ${field} exactly once`);
  }
  assert(extractSchemaProperty(playerSchema, "PressedSkill").includes("type: boolean"),
    "PlayerData.PressedSkill must be boolean");
  const readyTick = extractSchemaProperty(playerSchema, "SkillReadyTick");
  for (const marker of ["type: integer", "minimum: 0", "A + C"]) {
    assert(readyTick.includes(marker), `PlayerData.SkillReadyTick must document ${marker}`);
  }

  assert(serverGameConfig.version === 4, "server config must be version 4");
  const cooldowns = new Map([[0, 360], [1, 390], [2, 330]]);
  for (const playerType of serverGameConfig.player.types) {
    assert(playerType.skill?.cooldownTicks === cooldowns.get(playerType.characterType),
      `character ${playerType.characterType} skill cooldown drift`);
  }
  assert(!openAPIText.includes("PressedSkill") && !openAPIText.includes("SkillReadyTick"),
    "OpenAPI must not expose gameplay skill fields");
}
```

Extend the existing YAML/JSON gameplay-player example loops so every object contains each new field exactly once. Require at least one `PressedSkill: true` approval example and one `PressedSkill: false`/`SkillReadyTick: 0` initial example.

- [ ] **Step 2: Add a failing served-docs contract test**

Add `TestHandlerServesSkillCooldownContract` to `internal/docs/docs_test.go`:

```go
func TestHandlerServesSkillCooldownContract(t *testing.T) {
	handler := Handler()
	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	for _, marker := range []string{
		"version: 0.7.0",
		"PressedSkill:",
		"SkillReadyTick:",
		"minimum: 0",
		"A + C",
		"360/390/330",
	} {
		assertBodyContains(t, asyncAPI, marker)
	}
	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	if strings.Contains(openAPI.Body.String(), "PressedSkill") ||
		strings.Contains(openAPI.Body.String(), "SkillReadyTick") {
		t.Fatal("OpenAPI must not expose gameplay skill fields")
	}
}
```

Update existing version and exact required-field assertions in the same file to `0.7.0` and the new list.

- [ ] **Step 3: Run docs tests and verify RED**

```bash
rtk node docs-ui/scripts/validate.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -run 'TestHandlerServesSkillCooldownContract' -count=1
```

Expected: validator and served-docs test fail because AsyncAPI/source examples are still 0.6.0 without the fields.

- [ ] **Step 4: Update AsyncAPI schema, descriptions, and examples**

In `api/asyncapi.yaml`:

- Set `info.version: 0.7.0`; keep `asyncapi: 3.0.0`.
- Add optional `InputMessage.PressedSkill` boolean. Its description must say command-level independent attempt, reused `AttackDir`, no queue, and server snapshot approval.
- Change `PressedAttack` text from server config v3 to v4 without changing its behavior.
- Add required `PlayerData.PressedSkill` boolean describing transient server approval.
- Add required `PlayerData.SkillReadyTick` integer, `format: int64`, `minimum: 0`, initial `0`, and `A + C` inclusive-ready semantics.
- Include the exact cooldown marker `360/390/330` in the `SkillReadyTick` or top-level gameplay description so served-docs tests expose the authoritative character mapping.
- Add both fields exactly once to every gameplay player example. Use `PressedSkill: true` and `SkillReadyTick: 361` for a tick-1 Shelly approval example; use `false`/`0` for an initial bot example.
- Keep Ready examples and starting/started control snapshots unchanged.
- State that a cooldown-blocked valid positive command still advances `LastProcessedClientTick`.

- [ ] **Step 5: Update generated UI source and human docs**

In `docs-ui/scripts/build.mjs`, add `PressedSkill` to the Input article/example and both output fields to every gameplay player example. Explain `Snapshot.Tick >= SkillReadyTick` as ready and keep control snapshots null.

Update the six `ai-docs` files with these exact facts:

- Input `PressedSkill` is optional/missing false; present null/wrong type is `invalid_input`.
- `AttackDir` is reused but does not itself trigger a skill.
- `PlayerData.PressedSkill` is the approval pulse and `SkillReadyTick` is persistent canonical state.
- Ready predicate is `A >= SkillReadyTick`; approval writes `A+C`; exact `A+C` is allowed.
- Skill-ready wins over attack and preserves charge; cooldown/zero-direction falls through to existing attack.
- Valid cooldown-blocked positive commands ACK; blocked attempts never queue.
- Cooldowns are server config v4 `360/390/330`; client config stays outside SL-84.
- Actual effects remain SL-85 and bot skill use remains out of scope.
- AsyncAPI is `0.7.0`; OpenAPI remains unchanged.

Append `ADR-0037: SL-84 Skill cooldown은 canonical PlayerData와 Server Config v4가 소유` to `ai-docs/decisions.md`. Update `project-map.md` so SL-84 moves into the implemented flow and SL-85 remains not implemented.

- [ ] **Step 6: Run the official docs validation chain GREEN**

Run in the repository-required order:

```bash
rtk node docs-ui/scripts/validate.mjs
REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true rtk npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
rtk npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error
rtk node docs-ui/scripts/build.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -count=1
```

Expected: all five commands succeed.

- [ ] **Step 7: Verify excluded artifacts did not change**

```bash
rtk git diff --exit-code -- api/openapi.yaml client-config/game-config.json
```

Expected: no output and exit status `0`.

- [ ] **Step 8: Commit the public contract and docs unit**

```bash
rtk git add api/asyncapi.yaml docs-ui/scripts/validate.mjs docs-ui/scripts/build.mjs internal/docs/docs_test.go ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/decisions.md ai-docs/project-map.md
rtk git commit -m "[SL-84] docs(api): 스킬 쿨타임 계약 공개" -m "- AsyncAPI 0.7.0에 optional input과 required snapshot state를 추가
- server config v4와 A+C 포함 경계를 source validator로 고정
- OpenAPI와 client config 제외 경계 및 SL-85 후속 범위를 문서화"
```

### Task 5: Cross-Layer Verification, PR, and Linear Closeout

**Files:**
- Verify only: all Task 1-4 files
- Do not modify: `api/openapi.yaml`, `client-config/game-config.json`, SL-85/SL-93/SL-99 worktrees

**Interfaces:**
- Consumes: all implementation and contract commits from Tasks 1-4.
- Produces: repeat/race/full-CI evidence, a reviewable SL-84 PR, merged-main validation, and Linear Done evidence.

- [ ] **Step 1: Run focused repeat and race checks**

```bash
rtk mise exec -- go test ./internal/simulation -run 'TestStep.*Skill|TestStepCooldownBlockedSkillAcknowledgesValidClientTick' -count=20
rtk mise exec -- go test -race ./internal/rooms -run 'Test(InputMessage.*PressedSkill|WebSocket.*PressedSkill|SetInputCopiesPressedSkill|MergedTickInputs)' -count=20
```

Expected: both commands pass with zero flakes and no race report.

- [ ] **Step 2: Run full repository validation**

```bash
rtk make ci
```

Expected: docs validation/build, `go vet`, all Go tests, server build, deploy regression tests `1..14`, and shell syntax checks pass.

- [ ] **Step 3: Audit scope and repository cleanliness**

```bash
rtk git diff --check origin/main...HEAD
rtk git diff --exit-code origin/main...HEAD -- api/openapi.yaml client-config/game-config.json
rtk git status --short --branch
rtk git log --oneline origin/main..HEAD
```

Expected: no whitespace errors, no excluded-artifact diff, clean worktree, and only SL-84 commits.

- [ ] **Step 4: Reconcile latest main without overwriting concurrent SL-99 work**

Fetch `origin/main`. If it advanced, inspect the new commits first, then rebase this branch while preserving any independently merged `ClientGameConfigVersion` or client artifact changes. Re-run Steps 1-3 after any rebase. Do not edit the separate SL-99 worktree.

- [ ] **Step 5: Push and open a ready-for-review PR**

Use title:

```text
[SL-84] 스킬 입력과 서버 권위 쿨타임 추가
```

Use this body:

```markdown
## 왜 해당 PR을 올렸나요?

- Client가 명시적으로 스킬 사용을 요청하고 서버 승인 결과를 확인해야 해요.
- 캐릭터별 쿨타임과 일반 공격 우선순위를 같은 tick 기준으로 결정해야 해요.

## 무엇을 어떻게 수정했나요?

- `PressedSkill`을 command별 독립 시도로 받아요.
- `PlayerData`가 승인 결과와 절대 `SkillReadyTick`을 소유해요.
- Server config v4에 `360/390/330` tick을 추가했어요.
- AsyncAPI 0.7.0과 회귀 테스트·문서를 함께 갱신했어요.
- 실제 캐릭터별 스킬 효과는 SL-85로 남겼어요.
```

Move SL-84 to `In Review` and add one Linear comment containing the PR URL plus the exact focused/race/`make ci` results.

- [ ] **Step 6: Complete review, CI, and merge**

Wait for GitHub checks and unresolved review threads. Fix findings with the same test-first rule, rerun affected tests and `make ci`, then squash-merge only when checks are green and no review is pending.

- [ ] **Step 7: Validate merged main and resolve Linear**

Fetch the merge commit into a fresh clean temporary worktree, trust its `.mise.toml`, and run `rtk make ci` at exact merged HEAD. Then comment on SL-84 with merge PR/SHA and validation evidence, move SL-84 to `Done`, and confirm SL-85 remains held for revisit rather than starting it.

- [ ] **Step 8: Return to SL-93 as a separate design/plan cycle**

After SL-84 is merged and Done, resume SL-93 from its five proposed child boundaries. Do not combine SL-93 code or Linear child creation into the SL-84 PR.
