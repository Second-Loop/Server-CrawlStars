# SL-121 Bot Stall Investigation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to execute this plan task-by-task. Repository scope excludes multi-agent orchestration.

**Goal:** Reproduce the reported production-map bot stall deterministically and identify its root cause before changing bot behavior.

**Architecture:** Add a rooms-package integration fixture that runs the real server config, production `Map_0`, room-owned controller state, merged bot inputs, and `simulation.State.Step`. Record requested movement and authoritative position progress per live bot so controller failure, map collision, and dynamic player collision can be distinguished without adding public runtime fields.

**Tech Stack:** Go 1.25, standard `testing`, embedded `server-config`, existing rooms bot controller and simulation state.

## Global Constraints

- Work from `origin/main@cf805b61eba13a9a0eb9fdd1e187e20f0681edb7` or a later merged main.
- Do not modify Client code, public REST/WebSocket fields, bot skill behavior, deployment, or production matchmaking.
- Do not change production behavior until a failing automated reproduction and a single root-cause hypothesis exist.
- Preserve deterministic output for the same room/config/snapshot/controller state.
- Preserve existing player collision, mode damage, reconnect, GameEnd, and cleanup contracts.
- Use inline execution only; multi-agent orchestration is out of scope.

---

### Task 1: Add a production-map bot progress probe

**Files:**
- Create: `internal/rooms/bot_stall_test.go`
- Read: `internal/rooms/bot.go`
- Read: `internal/rooms/bot_controller.go`
- Read: `internal/rooms/websocket.go`
- Read: `internal/simulation/player_assignment.go`
- Read: `internal/simulation/simulation.go`

**Interfaces:**
- Consumes: `simulation.LoadGameConfig(serverconfig.Reader())`, `GameConfig.SelectMode`, `simulation.PlayerAssignments`, `simulation.NewStateWithConfig`, `mergedTickInputsAtTick`.
- Produces: a test-only `botProgressProbe` containing per-tick bot input, before/after positions, selected behavior evidence, and consecutive blocked-movement counts.

- [x] **Step 1: Create the real production fixture**

  Add `newProductionBotProgressFixture(t, mode)` in `internal/rooms/bot_stall_test.go`. Load the embedded server config, select `solo` and `team` in table tests, assign six canonical spawn positions through `simulation.PlayerAssignments`, mark the intended five participants as bots, and create `simulation.State` plus one `botControllerState` per bot. Keep the human stationary so repeated runs use identical observations.

- [x] **Step 2: Implement one real tick probe**

  Add `stepProductionBots(t, fixture)` that builds `botObservation` from the previous authoritative snapshot, calls `mergedTickInputsAtTick`, calls `State.Step` exactly once, and returns a record with each live bot's requested `MoveDir`, `PressedAttack`, starting position, ending position, HP, and death state. Do not duplicate controller or simulation rules in the test.

- [x] **Step 3: Verify the probe itself on the first tick**

  Add `TestProductionBotProgressProbeUsesRealControllerAndStateStep`. Assert that every live bot contributes at most one authoritative command, bot commands retain `ClientTick: 0` and `PressedSkill: false`, snapshot tick increments once, and the returned players are the next observation.

- [x] **Step 4: Run the focused probe test**

  Run: `go test ./internal/rooms -run TestProductionBotProgressProbeUsesRealControllerAndStateStep -count=1`

  Expected: PASS. A failure means the diagnostic fixture does not represent the production data flow and must be fixed before investigating the stall.

### Task 2: Reproduce persistent requested-but-unapplied movement

**Files:**
- Modify: `internal/rooms/bot_stall_test.go`
- Read: `internal/simulation/simulation.go:434`
- Read: `internal/rooms/bot_pathfinding.go`

**Interfaces:**
- Consumes: `stepProductionBots` from Task 1.
- Produces: `TestProductionMapBotsDoNotRemainMovementBlocked`, whose failure identifies bot ID, mode, tick interval, positions, requested direction, and nearby live players.

- [x] **Step 1: Add a bounded progress assertion**

  Run each `solo` and `team` fixture for 900 ticks. For every live bot, count consecutive ticks whose authoritative position remains unchanged. Reset the counter on position change or death. Fail at 90 consecutive stationary ticks and include the current input, tile neighborhood, map-collision classification, controller cache, and nearby live players.

- [x] **Step 2: Verify RED against the reported class of failure**

  Run: `go test ./internal/rooms -run TestProductionMapBotsDoNotRemainMovementBlocked -count=1 -v`

  Expected: FAIL only when a live bot requests movement for 90 consecutive ticks while `State.Step` leaves its position unchanged. Record the first failing mode, bot ID, tick, target/behavior inputs, and neighbor geometry. If it passes, continue with Task 3 instead of changing production code.

  Observed: solo `bot-e` fails through tick 245 at tile `(35,30)` while requesting `+Y`; team `bot-c` fails through tick 208 at tile `(31,20)` while requesting `-X`. Both next candidates collide with the static map, have no nearby player, and retain a valid cached path.

- [x] **Step 3: Confirm determinism**

  Run the focused test ten times: `go test ./internal/rooms -run TestProductionMapBotsDoNotRemainMovementBlocked -count=10`

  Expected: the same fixture either fails at the same bot/tick geometry or passes every run. Any variation is a separate determinism defect and becomes the root-cause investigation target.

### Task 3: Classify the failing boundary

**Files:**
- Modify: `internal/rooms/bot_stall_test.go`
- Read: `internal/rooms/bot_controller.go`
- Read: `internal/rooms/bot_pathfinding.go`
- Read: `internal/simulation/simulation.go:434`

**Interfaces:**
- Consumes: first persistent-stall trace from Task 2 or a hand-derived minimal fixture matching the preserved issue symptom.
- Produces: one failing minimal regression test and one written root-cause statement in the test comment and Linear SL-121 comment.

- [x] **Step 1: Split controller zero from simulation cancellation**

  For the first stalled bot, assert and report these boundaries separately: controller returned no command, controller returned zero `MoveDir`, A* returned no path, controller returned non-zero movement but map collision rejected the candidate, or player-player axis resolution canceled it. Use existing pure helpers and the before/after positions; do not add production logging.

- [x] **Step 2: Reduce to the smallest real geometry**

  Copy only the required production tiles and player positions into a focused test while preserving tile size, player radii, teams, and requested directions. The reduced test must fail for the same boundary as the 900-tick fixture.

- [x] **Step 3: State one hypothesis**

  Add a test comment and SL-121 comment that name exactly one classified boundary, the repeated authoritative state, the state transition that never changes, and the minimal failing test plus first failing tick as evidence. Do not propose multiple fixes.

  Root cause: four-way A* returns only the cardinal direction to its first-step tile. As soon as a bot's point enters a new tile, the path turns even if the player circle is still offset near the previous edge. At an inside Wall/Water corner the circle clips the blocked neighboring tile, `State.Step` rejects every candidate, and the `(start tile, goal tile) -> direction` cache repeats that cardinal direction forever. `TestBotAStarSteersToNextTileCenterBeforeTurningPastBlockedCorner` reduces this to a 3x3 map.

- [x] **Step 4: Run adjacent regressions**

  Run: `go test ./internal/rooms ./internal/simulation -run 'Bot|PlayerMovement|ProductionMap' -count=1`

  Expected: the new minimal reproduction fails for the intended missing behavior while existing tests remain green.
