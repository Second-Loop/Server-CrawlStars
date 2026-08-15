# SL-121 Bot Corner-Stall Fix Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to execute this plan task-by-task. Repository scope excludes multi-agent orchestration.

**Goal:** Prevent deterministic bots from becoming permanently stuck when a four-way A* route turns beside a blocked corner.

**Architecture:** Keep the existing four-way tile search and deterministic tie-breaks. Change its reusable result from a frozen cardinal direction to the first-step tile, then derive a fresh normalized direction from the bot's current world position toward that tile's center every tick. This preserves the cached search while allowing an offset player circle to clear the corner before completing the turn.

**Tech Stack:** Go 1.25, standard `testing`, rooms bot controller/pathfinding, simulation integration fixture.

---

### Task 1: Lock the corner geometry as RED

**Files:**
- Modify: `internal/rooms/bot_pathfinding_test.go`
- Modify: `internal/rooms/bot_stall_test.go`

- [x] Add a 3x3 radius-aware blocked-corner test that requires steering toward the next tile center.
- [x] Confirm the focused test returns the old cardinal direction and fails.
- [x] Confirm production solo/team fixtures fail with requested movement rejected by static map collision.

### Task 2: Cache the first-step tile and recompute steering

**Files:**
- Modify: `internal/rooms/bot_pathfinding.go`
- Modify: `internal/rooms/bot_controller.go`
- Modify: `internal/rooms/bot_controller_test.go`

- [x] Extract the deterministic first-step tile from A* while preserving `F -> H -> y -> x` behavior.
- [x] Center the current tile's perpendicular axis before applying the cached cardinal first step.
- [x] Cache `(start tile, goal tile, first-step tile)` and recompute the world direction on each same-tile call.
- [x] Update cache tests to prove the search result is reused while steering changes with current position.
- [x] Add deterministic one-tick live-player avoidance and a two-bot head-on regression without changing simulation collision.
- [x] Incorporate all raw human/bot movement candidates so simultaneous approach distances such as `1.10` cannot bypass avoidance.

### Task 3: Document the repaired invariant

**Files:**
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`

- [x] State that four-way A* still chooses cardinal tile steps, while movement centers the perpendicular tile axis before turning.
- [x] Record both SL-121 persistent-cancellation causes and why no public contract changes.

### Task 4: Verify and deliver SL-121

**Files:**
- Verify: `internal/rooms`
- Verify: `internal/simulation`
- Verify: repository CI

- [x] Run the focused corner test and production 900-tick probe.
- [x] Run each production probe ten times for determinism.
- [x] Run adjacent bot/movement regressions.
- [x] Run `make ci`.
- [ ] Commit with SL-121 convention, push, open a focused PR, wait for CI, merge, and reconcile Linear evidence.
