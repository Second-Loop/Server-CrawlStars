# SL-103 Reconnect Grace Implementation Plan

> **For agentic workers:** This plan is executed inline in the assigned worktree because the parent agent requested one scoped implementation and no additional agents. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Started matches keep a player authoritative and reconnectable for 10 seconds after an unexpected transport disconnect, then batch-expire due players on one gameplay tick through the existing mode evaluators and lifecycle ordering.

**Architecture:** A room owns a map of pending reconnect expiries keyed by player identity and connection generation. The existing room gameplay ticker checks deadlines before each `State.Step`; due identities are removed from the map and passed as one batch to simulation, so no player-specific timer or goroutine can make ordering decisions. Session close causes classify unexpected transport faults as reconnectable and existing lifecycle causes as intentional; only the current generation may create or cancel a pending expiry.

**Tech Stack:** Go, `internal/rooms`, `internal/simulation`, fake clock/ticker tests, WebSocket lifecycle tests, AsyncAPI and Korean `ai-docs` contract/ADR documentation.

## Global Constraints

- Apply the approved policy exactly: started-match peer/read/write/ping/control-overflow disconnects receive a 10-second grace period.
- Keep the player in simulation during grace; reconnect preserves the existing identity, state, and tick and cancels expiry.
- At `now >= deadline`, batch all due players before one gameplay `Step`, set HP to zero and `IsDead`, and reuse Duel/Solo/Team evaluators for simultaneous results.
- Never schedule grace for stale sessions or intentional `shutdown`, `game_end`, `expiry`, `debug_delete`, or `prestart_cancel` causes.
- Reject reconnect after a player result is finalized; connected terminal delivery remains snapshot, `GameEnd`, then close.
- Do not add a per-player timer/goroutine, alter server REST config, commit, push, or create a PR.
- Preserve existing unrelated worktree changes and run focused tests, race tests, and exact-worktree `make ci` where feasible.

## File Map

- Modify `internal/simulation/simulation.go`: add the authoritative batch-elimination operation used immediately before a gameplay step.
- Modify `internal/simulation/simulation_test.go`: prove batch elimination sets HP/`IsDead`, ignores unknown IDs, and is applied before the next snapshot.
- Modify `internal/rooms/store.go`: store per-room pending expiry state and expose the simulation-stepper contract needed by the room tick.
- Modify `internal/rooms/websocket.go`: classify causes, schedule/cancel generation-bound grace, and expire due identities in the gameplay tick.
- Modify `internal/rooms/websocket_test.go` and/or create `internal/rooms/reconnect_grace_test.go`: fake-clock boundary, reconnection identity/tick, stale generation, intentional causes, simultaneous modes, exactly-once, and transport-fault coverage.
- Modify `api/asyncapi.yaml`: document started-match reconnect grace, expiry, and terminal message ordering in the WebSocket contract.
- Modify `ai-docs/protocol.md`, `ai-docs/api-reference.md`, `ai-docs/project-map.md`, and `ai-docs/decisions.md`: describe the externally observable lifecycle and ADR-0041 decision; leave `api/openapi.yaml` unchanged because no REST contract changes.

---

### Task 1: Add batch authoritative elimination to simulation

**Files:**
- Modify: `internal/simulation/simulation.go`
- Test: `internal/simulation/simulation_test.go`

**Interfaces:**
- Produces `func (s *State) EliminatePlayers(ids []PlayerID)`; it sets each matching live player to `HP = 0` and `IsDead = true` without advancing `State.tick` or creating a snapshot.

- [ ] **Step 1: Write the failing test**

Add a table-driven test with two configured players that calls `state.EliminatePlayers([]PlayerID{"red", "unknown", "blue"})`, then calls one `Step(nil)`. Assert both known players have `HP == 0` and `IsDead == true`, the unknown ID has no effect, and the returned tick is exactly one. Also assert an already-dead player remains dead.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `rtk go test ./internal/simulation -run TestStateEliminatePlayers -count=1`

Expected: FAIL because `State` has no `EliminatePlayers` method.

- [ ] **Step 3: Implement the minimal simulation operation**

Add `State.EliminatePlayers(ids []PlayerID)` that builds a lookup set and, in the existing `s.players` slice, marks matching players dead and zeroes HP. Do not mutate tick, projectiles, input acknowledgements, or nonmatching players.

- [ ] **Step 4: Run simulation tests**

Run: `rtk go test ./internal/simulation -run 'TestStateEliminatePlayers|TestStep' -count=1`

Expected: PASS.

### Task 2: Add room-owned generation-bound grace scheduling

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/websocket.go`
- Test: `internal/rooms/reconnect_grace_test.go`

**Interfaces:**
- Consumes `(*simulation.State).EliminatePlayers` through the room simulation stepper.
- Produces room helpers equivalent to `scheduleReconnectGraceLocked`, `cancelReconnectGraceLocked`, and `expiredReconnectPlayersLocked(now) []simulation.PlayerID`; the expiry record contains the connection generation and deadline.

- [ ] **Step 1: Write failing room tests**

Cover these exact cases with `newFakeClock` and an attached started room: an unexpected peer/read/write/ping/control cause creates a 10-second pending record; advancing to one nanosecond before the deadline leaves the player alive and the room reconnectable; advancing to the exact deadline and ticking the gameplay ticker produces one dead snapshot; reconnect before the deadline removes the record and preserves the prior snapshot tick/state; and a second expiry attempt does not produce a second result or another step-side mutation.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `rtk go test ./internal/rooms -run 'TestReconnectGrace|TestReconnectExpiry' -count=1`

Expected: FAIL because the room has no pending-grace state and the close callback does not classify/schedule unexpected causes.

- [ ] **Step 3: Implement generation-safe scheduling and cancellation**

Add `defaultReconnectGrace = 10 * time.Second` and a room map such as `map[string]reconnectGrace{generation uint64; expiresAt time.Time}`. In `releaseClient`, after confirming `room.clients[playerID] == expectedSession`, read the session close cause. For a started room and reconnectable cause, record the current session generation and `s.clock.Now().Add(defaultReconnectGrace)`; for intentional causes, leave no pending record. In `attachClientSession`, delete only the matching player’s pending record after all room/session validity checks and clear `disconnectedAt` as today. Because release validates the current session pointer, an older generation closing after a reconnect cannot schedule a new grace.

- [ ] **Step 4: Process due records before the single gameplay step**

Under `room.mu`, call `expiredReconnectPlayersLocked(s.clock.Now())` before merging inputs. Pass the returned IDs to `room.state` through `EliminatePlayers` in one call, then call `Step` exactly once. Treat `now.Before(expiresAt)` as not due and `now >= expiresAt` as due. Do not launch a timer or goroutine per record.

- [ ] **Step 5: Run focused room tests**

Run: `rtk go test ./internal/rooms -run 'TestReconnectGrace|TestReconnectExpiry' -count=1`

Expected: PASS, including before-deadline and exact-deadline assertions.

### Task 3: Verify lifecycle policy, evaluator reuse, and terminal ordering

**Files:**
- Modify: `internal/rooms/websocket.go` only if the lifecycle tests expose a gap.
- Test: `internal/rooms/reconnect_grace_test.go`, `internal/rooms/close_observation_test.go` if a narrow assertion belongs with close causes.

**Interfaces:**
- Consumes existing `calculateGameEndResults`, `claimFinalizedGameEndResults`, `gameEndDeliveries`, and terminal writer handoff.
- Produces no new public message type; finalized result state rejects later reservation through the existing `hasFinalizedGameEndResult` check.

- [ ] **Step 1: Write failing policy tests**

Test all intentional causes (`shutdown`, `game_end`, `expiry`, `debug_delete`, `prestart_cancel`) do not create a forfeit record; test a stale old session close after generation-two attach does not create a record; test two due players in Duel, Solo, and Team yield the existing simultaneous evaluator result (Draw where both sides are eliminated) from one gameplay tick; test a connected terminal player receives one snapshot, one `GameEnd`, then close; and test reconnect after finalization is rejected.

- [ ] **Step 2: Run the policy tests and observe failure**

Run: `rtk go test ./internal/rooms -run 'TestReconnectGrace|TestReconnectExpiry|TestReconnectIntentional|TestReconnectStale|TestReconnectTerminal' -count=1`

Expected: FAIL until the new lifecycle state is wired through all cases.

- [ ] **Step 3: Implement only the minimal lifecycle corrections**

Use the existing close-cause enum and first-cause claim. Keep `gameEnd`, `shutdown`, room `expiry`, debug deletion, and prestart cancellation outside the reconnectable set. Do not reopen a room or reset a finalized result. Keep existing message delivery code so terminal order remains snapshot → `GameEnd` → close.

- [ ] **Step 4: Run focused and race tests**

Run: `rtk go test ./internal/rooms -run 'TestReconnectGrace|TestReconnectExpiry|TestReconnectIntentional|TestReconnectStale|TestReconnectTerminal' -count=1` and `rtk go test -race ./internal/rooms ./internal/simulation`

Expected: PASS with no race reports.

### Task 4: Update the approved WebSocket contract and decision record

**Files:**
- Modify: `api/asyncapi.yaml`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`

**Interfaces:**
- Documents existing message names/fields only; no REST schema or gameplay evaluator changes.

- [ ] **Step 1: Write failing docs assertions if the repository has a contract/doc test for the new clauses**

Extend the existing AsyncAPI/protocol validation test only if needed to assert the exact reconnect-grace clauses and intentional close-cause list; otherwise use the repository’s existing markdown/AsyncAPI validation commands as the check.

- [ ] **Step 2: Update the contract text**

State the exact 10-second started-match grace, no simulation pause, same identity/state/tick on reconnect, expiry at or after deadline in the next gameplay tick, batch same-tick elimination, finalized reconnect rejection, and terminal ordering. Explicitly separate reconnectable transport causes from intentional lifecycle causes. Keep `api/openapi.yaml` unchanged.

- [ ] **Step 3: Run docs and API checks**

Run: `rtk go test ./internal/rooms -run 'Test.*(Protocol|AsyncAPI|API)' -count=1` if matching tests exist, then the repository’s `make ci` validation after all code/docs changes.

Expected: PASS and no stale “reconnect is not implemented” statement remains for the shipped scope.

### Task 5: Final verification and handoff

**Files:**
- Read-only review of the final diff and test output.

- [ ] **Step 1: Inspect scope and generated changes**

Run: `rtk git status --short`, `rtk git diff --stat`, and `rtk git diff --check`; confirm only SL-103 files plus the implementation plan are changed.

- [ ] **Step 2: Run final checks**

Run: `rtk go test ./internal/simulation`, `rtk go test ./internal/rooms`, `rtk go test -race ./internal/rooms ./internal/simulation`, and exact-worktree `rtk make ci` if dependencies and the worktree permit it.

- [ ] **Step 3: Report without commit/push/PR**

Return changed files, focused/race/CI evidence, exact policy behavior, and residual risks. Do not commit or publish; parent agent will integrate the worktree result.
