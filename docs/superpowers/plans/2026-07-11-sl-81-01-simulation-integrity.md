# SL-81 Stack 1 Simulation Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make movement, aiming, death handling, and normal-attack capacity server authoritative without changing the `Step(inputs) -> Snapshot` transport boundary.

**Architecture:** Sanitize vectors at the simulation boundary and keep per-player attack charge state inside `simulation.State`. Resolve charge capacity and recharge ticks from the server-only game configuration so clients may predict the rule but cannot bypass it.

**Tech Stack:** Go 1.25, table-driven Go tests, JSON game config, Markdown architecture records.

## Global Constraints

- `MoveDir` magnitudes at or below 1 remain unchanged; larger finite magnitudes clamp to 1.
- Every non-zero finite `AttackDir` becomes a unit vector.
- A dead player input cannot change position, directions, or create a projectile; `PressedAttack` is false in that tick's snapshot.
- The default player starts with 4 attack charges and regains one charge every 30 ticks, up to 4.
- Attack charge fields stay server-only and do not expand `client-config/game-config.json`.
- Preserve existing projectile creation timing and the public snapshot schema.

---

### Task 1: Add validated attack charge configuration

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`
- Modify: `server-config/game-config.json`

**Interfaces:**
- Consumes: existing `PlayerTypeConfig` JSON decoding.
- Produces: `PlayerTypeConfig.MaxAttackCharges int` and `PlayerTypeConfig.AttackRechargeTicks int` with defaults `4` and `30`.

- [ ] **Step 1: Write failing config tests**

Add tests that load a player type with `maxAttackCharges: 4` and `attackRechargeTicks: 30`, then reject zero or negative values with the exact player type ID in the error.

```go
if got := config.DefaultPlayerType().MaxAttackCharges; got != 4 {
	t.Fatalf("expected 4 max attack charges, got %d", got)
}
if _, err := ResolveGameConfig(configWithAttackBudget(0, 30)); err == nil {
	t.Fatal("expected zero maxAttackCharges to fail")
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `mise exec -- go test ./internal/simulation -run 'Test(Load|Resolve|Static).*Attack' -count=1`

Expected: compilation fails because the two `PlayerTypeConfig` fields do not exist.

- [ ] **Step 3: Implement the schema and validation**

Add the two JSON fields to `PlayerTypeConfig`, require both to be positive in `ResolveGameConfig`, set them in `StaticGameConfig`, and add them to the default player object in `server-config/game-config.json`.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `mise exec -- go test ./internal/simulation -run 'Test(Load|Resolve|Static).*Attack' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the config boundary**

```text
[SL-81] feat(config): 서버 공격 charge 설정 추가

- 기본 공격 charge와 회복 tick을 server config에 추가
- 잘못된 공격 budget 설정 검증 보강
```

### Task 2: Sanitize vectors and ignore dead-player input

**Files:**
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/simulation_test.go`

**Interfaces:**
- Consumes: `Vector2`, `State.Step`, and configured player speed.
- Produces: `clampDirection(Vector2) Vector2`, `normalizeDirection(Vector2) Vector2`, and dead-input rejection before state mutation.

- [ ] **Step 1: Write failing vector/death regression tests**

Cover `(100, 0)` movement versus `(1, 0)`, diagonal movement length, `(0, 50)` attack direction versus `(0, 1)`, analog `(0.25, 0)` preservation, and a player killed by a projectile before its input is applied in the same `Step`.

```go
oversized := state.Step([]InputCommand{{PlayerID: "player-1", MoveDir: Vector2{X: 100}}})
unit := comparison.Step([]InputCommand{{PlayerID: "player-1", MoveDir: Vector2{X: 1}}})
assertVector(t, "clamped position", oversized.Players[0].Pos, unit.Players[0].Pos)
```

- [ ] **Step 2: Run focused tests and verify RED**

Run: `mise exec -- go test ./internal/simulation -run 'TestStep.*(Clamp|Normalize|Dead)' -count=1`

Expected: oversized vectors move or shoot farther and dead input mutates the terminal snapshot.

- [ ] **Step 3: Implement minimal sanitization**

Use `math.Hypot`; return zero unchanged, preserve vectors with magnitude `<= 1` for movement, and scale larger movement/non-zero attack vectors by `1/magnitude`. Reset every player's transient `PressedAttack` to false at the beginning of `Step`, then return immediately from `applyInput` when `IsDead` is true.

- [ ] **Step 4: Run focused and full simulation tests**

Run: `mise exec -- go test ./internal/simulation -count=1`

Expected: PASS with unchanged non-finite input behavior.

- [ ] **Step 5: Commit input sanitization**

```text
[SL-81] fix(simulation): 입력 방향을 서버에서 정규화

- 이동과 공격 방향 크기를 서버 경계에서 제한
- 사망한 player의 같은 tick 입력 차단
```

### Task 3: Enforce deterministic attack charges

**Files:**
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/simulation_test.go`

**Interfaces:**
- Consumes: `PlayerTypeConfig.MaxAttackCharges`, `AttackRechargeTicks`, and `State.tick`.
- Produces: private `attackState{charges int, rechargeTicks int}` keyed by `PlayerID`; accepted attacks alone set `PressedAttack=true` and create a projectile.

- [ ] **Step 1: Write failing charge tests**

Test four accepted consecutive shots, the fifth ignored shot, one restored charge after 30 recharge ticks, no accumulation above four, zero `AttackDir` consuming no charge, and separate budgets for two players.

```go
for tick := 0; tick < 4; tick++ {
	snapshot = state.Step([]InputCommand{attackInput("player-1")})
}
snapshot = state.Step([]InputCommand{attackInput("player-1")})
if got := liveProjectileCount(snapshot); got != 4 {
	t.Fatalf("expected exhausted fifth attack to be ignored, got %d projectiles", got)
}
```

- [ ] **Step 2: Run the charge tests and verify RED**

Run: `mise exec -- go test ./internal/simulation -run 'TestStep.*AttackCharge' -count=1`

Expected: the fifth consecutive attack creates a fifth projectile.

- [ ] **Step 3: Implement per-player attack state**

Initialize every player at full charges in `NewStateWithConfig`. At each `Step`, advance recharge progress only while below max, restore at most the earned whole charges, and cap at max. Consume one charge only when `PressedAttack` is true and normalized `AttackDir` is non-zero; otherwise leave `PressedAttack` false.

- [ ] **Step 4: Run simulation tests repeatedly**

Run: `mise exec -- go test ./internal/simulation -count=20`

Expected: PASS on all 20 runs.

- [ ] **Step 5: Commit attack authority**

```text
[SL-81] fix(simulation): 공격 charge를 서버에서 강제

- player별 공격 charge와 tick 기반 회복 상태 추가
- charge 소진 및 회복 회귀 테스트 추가
```

### Task 4: Document and validate stack 1

**Files:**
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`
- Add: `ai-docs/improvement-report.md`
- Add: `docs/superpowers/plans/2026-07-11-sl-81-*.md`

**Interfaces:**
- Consumes: implemented simulation behavior.
- Produces: durable server-authority documentation and committed SL-81 plans.

- [ ] **Step 1: Update current-state documentation**

Record vector rules, 4-charge/30-tick recharge semantics, dead-input behavior, and the fact that snapshot schema is unchanged. Add an ADR entry for the authoritative attack budget.

- [ ] **Step 2: Run official validation**

Run: `make ci`

Expected: docs validation, format check, vet, all tests, build, and deploy syntax checks pass.

- [ ] **Step 3: Check the diff**

Run: `git diff --check main...HEAD`

Expected: no whitespace errors and no files outside stack 1 plus plan/report files.

- [ ] **Step 4: Commit docs and plans**

```text
[SL-81] docs(simulation): 입력 무결성 기준 기록

- 공격 charge와 방향 정규화 규칙 문서화
- 개선 보고서와 stacked 구현 계획 추가
```
