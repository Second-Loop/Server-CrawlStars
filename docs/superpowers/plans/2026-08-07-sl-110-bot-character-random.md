# SL-110 Bot Character Randomization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make server-owned bots choose Shelly, Colt, or Lily independently and uniformly at creation time while preserving the chosen character through Ready and gameplay snapshots.

**Architecture:** Keep room participants as the source of truth: bot creation records one stable `CharacterType` on each `playerResponse`, and existing Ready/simulation projection paths continue to consume that value. Inject a chooser through `StoreConfig` for deterministic tests; production uses a dedicated `crypto/rand` chooser so Store ID/session entropy is not consumed by character selection. Manual bot addition and timer bot fill choose the full batch before appending, preserving all-or-nothing rollback.

**Tech Stack:** Go, `crypto/rand.Int`, `internal/rooms` store/matchmaking tests, server-hosted AsyncAPI/docs UI, ADR markdown.

## Global Constraints

- Choose only existing `0=Shelly`, `1=Colt`, `2=Lily`; do not add characters or change human selection/default behavior.
- Each bot draw is independent and unbiased; duplicate characters in one room are allowed.
- Persist the choice for the match; Ready and gameplay Snapshot must expose the same participant value.
- Production chooser must not read `Store.random`, which remains dedicated to room/player/session IDs.
- Failed chooser calls must leave no partial bots or leaked reserved IDs and must preserve existing bot-fill failure/no-retry behavior.
- Do not modify the Client repository, commit, push, or open a PR.

---

### Task 1: Add failing bot chooser and propagation tests

**Files:**
- Modify: `internal/rooms/bot_participant_test.go`
- Modify: `internal/rooms/bot_fill_test.go`
- Modify: `internal/rooms/character_type_test.go`

**Interfaces:**
- Consumes: current `StoreConfig`, `addBots`, timer bot fill, `readyEventPlayers`, and `simulationPlayers` behavior.
- Produces: deterministic chooser fixtures and regression tests that require the production interface.

- [x] **Step 1: Write tests for injected deterministic draws**

  Add tests that inject a chooser sequence such as `[Colt, Lily, Colt, Shelly, Lily]`, fill Duel/Solo/Team rooms through both `addBots` and timer bot fill, and assert literal per-participant `CharacterType` values. Assert duplicates are preserved and human values remain the requested/default values.

- [x] **Step 2: Write propagation tests**

  For a manually filled room, derive Ready players and simulation players from the stored room participants, then assert each bot's stored, Ready, and simulation `CharacterType` match the literal chosen sequence. Exercise one real room tick/snapshot so the gameplay projection is covered without changing the bot controller.

- [x] **Step 3: Write random-stream isolation and chooser-failure tests**

  Use a finite `StoreConfig.Random` containing exactly the bytes needed for room/human/session/bot IDs and leave no bytes for a character draw; assert default bot creation still succeeds. Add injected chooser error/invalid-value cases and assert manual add returns `ErrInternal`, timer fill logs failure without partial participants, and reserved IDs are released.

- [x] **Step 4: Run the focused tests and verify the expected RED failures**

  Run:

  ```sh
  env GOCACHE=/private/tmp/server-crawlstars-sl110-019fcbbc/.cache/go-build rtk go test ./internal/rooms -run 'Test(Bot|CharacterType).*' -count=1
  ```

  Expected: compile failures because `StoreConfig` has no chooser injection and existing bot creation always hard-codes Shelly.

---

### Task 2: Implement isolated uniform chooser and batch bot creation

**Files:**
- Modify: `internal/rooms/store.go`
- Modify: `internal/rooms/bot_participant_test.go`
- Modify: `internal/rooms/bot_fill_test.go`

**Interfaces:**
- Consumes: tests from Task 1.
- Produces: `StoreConfig.BotCharacterChooser func() (simulation.CharacterType, error)`, internal Store chooser field, and batch-aware `appendReservedBotsLocked`.

- [x] **Step 1: Add the minimal injected chooser seam**

  Add the optional `StoreConfig.BotCharacterChooser` field and store it during construction. If nil, use a dedicated production function backed by `crypto/rand.Int(rand.Reader, big.NewInt(3))`, mapping the result to the existing Shelly/Colt/Lily constants. Never call `s.random` for this draw.

- [x] **Step 2: Add batch choice validation and all-or-nothing append**

  Add an internal helper that calls the chooser once per bot, validates the result is one of the existing three character types, and returns the complete slice or an error. Update manual `addBots` and timer `fillMatchmakingBots` to choose the full batch before `appendReservedBotsLocked`; on error, return/log the existing internal failure and let current reservation rollback remove every ID.

- [x] **Step 3: Store the selected type on each participant**

  Change `appendReservedBotsLocked` to accept the preselected types and pass each type to `appendParticipantLocked`. Do not alter `addPlayer` human character handling, ID/session generation, team/slot assignment, or bot AI.

- [x] **Step 4: Run the focused tests and verify GREEN**

  Run the same focused command from Task 1. Expected: deterministic sequence, duplicate, failure rollback, and random-stream tests pass.

---

### Task 3: Contract documentation and ADR

**Files:**
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`

**Interfaces:**
- Consumes: shipped server behavior from Task 2; no wire shape change.
- Produces: ADR-0046 and concise documentation of bot character selection and propagation.

- [x] **Step 1: Document the stable participant contract**

  State that each server-owned bot draws independently and uniformly from existing `0/1/2`, duplicates are allowed, the choice is fixed at creation, and Ready/Snapshot reuse the same `CharacterType`. Keep bot replacement, advanced AI, and human selection out of scope.

- [x] **Step 2: Add ADR-0046**

  Record the dedicated chooser injection, isolated ID/session entropy stream, batch rollback semantics, and unchanged wire fields/AI ownership.

- [x] **Step 3: Validate docs and full repository**

  Run focused/race tests, `make ci`, and `git diff --check`; confirm no Client files or unrelated tracked changes are touched.
