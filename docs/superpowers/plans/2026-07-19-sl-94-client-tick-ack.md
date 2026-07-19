# SL-94 ClientTick ACK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Client input의 optional `ClientTick`을 실제 simulation 처리 시 player별 ACK로 snapshot에 포함하고, 양수 tick의 ACK가 감소하지 않게 합니다.

**Architecture:** Simulation state가 `LastProcessedClientTick`의 최종 소유자입니다. Room은 `room.mu` 아래 `lastPlayers`와 pending input을 비교해 stale/duplicate 양수 tick을 Step 전에 제거하고, simulation은 두 번째 guard로 stale input 전체를 거부합니다. Legacy `ClientTick=0`은 기존 last-write-wins input을 적용하되 ACK를 유지합니다.

**Tech Stack:** Go 1.24, JSON WebSocket DTO, existing authoritative `State.Step`, AsyncAPI 3.0 contract version 0.5.0, Node.js docs validator.

## Global Constraints

- Base는 ready-for-review SL-91 branch이고 PR base도 `sl-91-bot-autofill`입니다.
- `ClientTick`은 optional signed int64 wire field이고 누락은 `0`, 음수는 `invalid_input`입니다.
- 양수 stale/duplicate tick은 error/control frame 없이 input 전체를 무시합니다.
- 정상 양수 `10 -> 11 -> 12`는 pending `12`가 이기고 `12 -> 11` 또는 `12 -> 12`는 기존 `12`를 유지합니다.
- Legacy `0`은 양수 pending도 last-write-wins로 덮을 수 있고 상태에는 적용되지만 ACK를 변경하지 않습니다.
- ACK는 유효 input을 처리하면 눈에 보이는 효과가 없어도 증가하고, unknown/dead/non-finite/negative input에는 증가하지 않습니다.
- Match 안 reconnect는 snapshot ACK 다음 양수 tick을 이어가며 새 match만 `0`으로 초기화합니다.
- `Snapshot.Tick`, starting `Players:null`, started control `Tick:0/Players:null` 의미를 바꾸지 않습니다.
- Bot command와 bot ACK는 `0`입니다.
- PressedAttack pending overwrite 특성은 별도 범위이며 수정하지 않습니다.
- 각 task는 RED 확인 후 최소 구현, GREEN 확인, 독립 리뷰, commit 순서를 지킵니다.

---

### Task 1: Simulation state가 처리 ACK를 소유

**Files:**
- Modify: `internal/simulation/simulation.go`
- Test: `internal/simulation/simulation_test.go`

**Interfaces:**
- Consumes: `State.Step([]InputCommand) Snapshot`, `State.applyInput`.
- Produces: `InputCommand.ClientTick int64`, `PlayerData.LastProcessedClientTick int64`.

- [ ] **Step 1: ACK 의미를 고정하는 failing tests를 작성합니다**

다음 named tests를 추가합니다.

```go
func TestStepAcknowledgesProcessedClientTick(t *testing.T)
func TestStepPreservesLastProcessedClientTickWithoutInput(t *testing.T)
func TestStepTracksClientTickIndependentlyPerPlayer(t *testing.T)
func TestStepAcknowledgesProcessedInputWithoutVisibleEffect(t *testing.T)
func TestStepRejectsStaleAndDuplicateClientTick(t *testing.T)
func TestStepDoesNotAcknowledgeUnprocessedInput(t *testing.T)
func TestStepAppliesLegacyInputWithoutChangingACK(t *testing.T)
```

핵심 assertion 형태:

```go
state := NewState([]PlayerData{{ID: "red", Team: TeamRed}})
first := state.Step([]InputCommand{{
	PlayerID: "red", ClientTick: 12, MoveDir: Vector2{X: 1},
}})
if got := first.Players[0].LastProcessedClientTick; got != 12 {
	t.Fatalf("ACK=%d want=12", got)
}
second := state.Step(nil)
if got := second.Players[0].LastProcessedClientTick; got != 12 {
	t.Fatalf("ACK after no input=%d want=12", got)
}
```

No-visible-effect table은 wall collision, zero attack direction, exhausted attack charge를 각각 유효 input으로 처리하고 ACK 증가를 확인합니다. Invalid table은 negative tick, unknown/dead player, NaN/Inf direction을 넣고 position/direction/projectile/ACK가 모두 불변인지 확인합니다.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/simulation -run 'TestStep.*(ClientTick|ACK|ProcessedInput|UnprocessedInput)' -count=1
```

Expected: `ClientTick` 또는 `LastProcessedClientTick` field가 없어 compile failure.

- [ ] **Step 3: Wire-compatible fields와 apply guard를 구현합니다**

```go
type InputCommand struct {
	PlayerID      PlayerID `json:"PlayerId"`
	ClientTick    int64    `json:"ClientTick"`
	MoveDir       Vector2  `json:"MoveDir"`
	AttackDir     Vector2  `json:"AttackDir"`
	PressedAttack bool     `json:"PressedAttack"`
}

type PlayerData struct {
	ID                      PlayerID `json:"Id"`
	Team                    Team     `json:"Team"`
	Slot                    int      `json:"Slot"`
	IsBot                   bool     `json:"IsBot"`
	Pos                     Vector2  `json:"Pos"`
	MoveDir                 Vector2  `json:"MoveDir"`
	AttackDir               Vector2  `json:"AttackDir"`
	Speed                   float64  `json:"Speed"`
	Radius                  float64  `json:"Radius"`
	HP                      float64  `json:"HP"`
	PressedAttack           bool     `json:"PressedAttack"`
	IsDead                  bool     `json:"IsDead"`
	LastProcessedClientTick int64 `json:"LastProcessedClientTick"`
}
```

`applyInput`은 player를 찾고 dead/non-finite를 거부한 뒤 아래 guard를 적용합니다.

```go
if input.ClientTick < 0 {
	return
}
if input.ClientTick > 0 && input.ClientTick <= s.players[i].LastProcessedClientTick {
	return
}
if input.ClientTick > 0 {
	s.players[i].LastProcessedClientTick = input.ClientTick
}
```

ACK assignment는 movement collision과 attack effect 판정보다 앞, player/finite/stale validation보다 뒤에 둡니다. 그래서 처리된 no-effect input은 ACK하고 invalid/stale input은 ACK하지 않습니다.

- [ ] **Step 4: GREEN과 simulation 전체 회귀를 확인합니다**

Run:

```bash
rtk go test ./internal/simulation -run 'TestStep.*(ClientTick|ACK|ProcessedInput|UnprocessedInput)' -count=20
rtk go test ./internal/simulation -count=1
```

Expected: PASS.

- [ ] **Step 5: Task 1을 commit합니다**

```bash
rtk git add internal/simulation/simulation.go internal/simulation/simulation_test.go
rtk git commit -m "[SL-94] feat(simulation): add processed input ACK" -m "- carry ClientTick through authoritative input commands
- preserve monotonic per-player ACK in simulation state
- acknowledge valid inputs even when they have no visible effect"
```

---

### Task 2: Room pending 선택과 WebSocket wire 통합

**Files:**
- Modify: `internal/rooms/messages.go`
- Modify: `internal/rooms/websocket.go`
- Test: `internal/rooms/messages_test.go`
- Test: `internal/rooms/websocket_test.go`
- Test: `internal/rooms/bot_test.go`

**Interfaces:**
- Consumes: Task 1의 `InputCommand.ClientTick`, `PlayerData.LastProcessedClientTick`; SL-90의 `mergedTickInputs`.
- Produces: `inputMessage.ClientTick`, `inputDisposition`, `lastProcessedClientTick`, disposition을 반환하는 `Store.setInput`.

- [ ] **Step 1: Pending/wire/lifecycle failing tests를 작성합니다**

다음 named tests를 추가합니다.

```go
func TestInputMessageDecodesClientTickAndDefaultsMissingToZero(t *testing.T)
func TestSetInputKeepsHighestPositiveClientTick(t *testing.T)
func TestSetInputDropsPositiveTickAtOrBelowLastProcessed(t *testing.T)
func TestSetInputLetsLegacyZeroOverwritePendingClientTick(t *testing.T)
func TestWebSocketNegativeClientTickReturnsInvalidInputAndPreservesPending(t *testing.T)
func TestWebSocketStaleAndDuplicatePositiveClientTicksAreSilent(t *testing.T)
func TestWebSocketAcknowledgesClientTickOnlyAfterGameplayStep(t *testing.T)
func TestWebSocketClientTickACKsAreIndependent(t *testing.T)
func TestMergedTickInputsPreservesHumanClientTickAndKeepsBotTickZero(t *testing.T)
func TestWebSocketClientTickLifecycleKeepsControlPlayersNull(t *testing.T)
```

Pending selection의 핵심 assertion:

```go
for _, tick := range []int64{10, 11, 12, 11, 12} {
	disposition := store.setInput(room.ID, playerID, inputMessage{ClientTick: tick}, session)
	if tick <= 11 && disposition == inputStored {
		t.Fatalf("stale tick %d was stored", tick)
	}
}
room.mu.Lock()
got := room.pendingInputs[playerID].ClientTick
room.mu.Unlock()
if got != 12 {
	t.Fatalf("pending tick=%d want=12", got)
}
```

WebSocket silence test는 stale message 전후에 control queue를 확인하고, 20ms bounded read 동안 error message가 없으며 다음 gameplay snapshot stream은 계속되는지 확인합니다. Lifecycle test는 `starting null -> started Tick:0 null -> first gameplay Tick:1 players`를 순서대로 읽고 gameplay JSON에 ACK 값이 0이어도 key가 있는지 확인합니다.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'Test(InputMessage|SetInput|WebSocket.*ClientTick|MergedTickInputs.*ClientTick)' -count=1
```

Expected: missing field/type 때문에 compile failure.

- [ ] **Step 3: DTO, disposition, pending guard를 구현합니다**

```go
type inputMessage struct {
	ClientTick    int64              `json:"ClientTick"`
	MoveDir       simulation.Vector2 `json:"MoveDir"`
	AttackDir     simulation.Vector2 `json:"AttackDir"`
	PressedAttack bool               `json:"PressedAttack"`
}

type inputDisposition uint8

const (
	inputIgnored inputDisposition = iota
	inputStored
	inputInvalid
)

func lastProcessedClientTick(players []simulation.PlayerData, playerID simulation.PlayerID) int64 {
	for _, player := range players {
		if player.ID == playerID {
			return player.LastProcessedClientTick
		}
	}
	return 0
}
```

`setInput` signature를 아래처럼 바꾸고 room/session eligibility 실패는 `inputIgnored`를 반환합니다.

```go
func (s *Store) setInput(
	roomID string,
	playerID string,
	input inputMessage,
	expectedSession *clientSession,
) inputDisposition
```

Room lock 아래 저장 순서는 정확히 다음과 같습니다.

```go
if input.ClientTick < 0 {
	return inputInvalid
}
playerTick := lastProcessedClientTick(room.lastPlayers, simulation.PlayerID(playerID))
if input.ClientTick > 0 && input.ClientTick <= playerTick {
	return inputIgnored
}
if pending, ok := room.pendingInputs[playerID]; ok &&
	input.ClientTick > 0 && pending.ClientTick > 0 && input.ClientTick <= pending.ClientTick {
	return inputIgnored
}
room.pendingInputs[playerID] = simulation.InputCommand{
	PlayerID: simulation.PlayerID(playerID), ClientTick: input.ClientTick,
	MoveDir: input.MoveDir, AttackDir: input.AttackDir,
	PressedAttack: input.PressedAttack,
}
return inputStored
```

WebSocket read loop는 `inputInvalid`에만 기존 `invalid_input`을 enqueue하고 `inputIgnored`에는 아무 frame도 보내지 않습니다. 기존 내부 `setInput` 호출부는 반환값을 무시해도 됩니다. `mergedTickInputs`의 human struct copy를 유지하고 bot generator에는 tick을 설정하지 않습니다.

- [ ] **Step 4: GREEN, repeat, race를 확인합니다**

Run:

```bash
rtk go test ./internal/rooms -run 'Test(InputMessage|SetInput|WebSocket.*ClientTick|MergedTickInputs.*ClientTick)' -count=1
rtk go test ./internal/rooms -run 'Test(WebSocket.*ClientTick|MergedTickInputs.*ClientTick)' -count=20
rtk go test -race ./internal/rooms ./internal/simulation
```

Expected: PASS, race warning 없음.

- [ ] **Step 5: Task 2를 commit합니다**

```bash
rtk git add internal/rooms/messages.go internal/rooms/websocket.go internal/rooms/messages_test.go internal/rooms/websocket_test.go internal/rooms/bot_test.go
rtk git commit -m "[SL-94] feat(rooms): enforce monotonic ClientTick pending input" -m "- keep the highest admissible positive tick per player
- preserve legacy zero-tick last-write-wins input
- expose processed ACK without changing lifecycle snapshots"
```

---

### Task 3: AsyncAPI 0.5.0과 문서·최종 검증

**Files:**
- Modify: `api/asyncapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `internal/docs/docs_test.go`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/project-map.md`
- Inspect only: `api/openapi.yaml`

**Interfaces:**
- Consumes: Tasks 1–2의 최종 runtime/wire semantics.
- Produces: AsyncAPI `0.5.0`, optional input tick, required player ACK, human/bot examples, served docs validator.

- [ ] **Step 1: Contract tests와 source validator를 RED로 바꿉니다**

`internal/docs/docs_test.go`에 `TestHandlerServesClientTickACKContract`를 추가합니다.

```go
for _, marker := range []string{
	"version: 0.5.0",
	"ClientTick:",
	"format: int64",
	"minimum: 0",
	"LastProcessedClientTick:",
	"required: [Id, Team, Slot, IsBot, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead, LastProcessedClientTick]",
} {
	assertBodyContains(t, asyncAPI, marker)
}
```

Validator는 다음을 구조적으로 확인합니다.

- `InputMessage` property에 `ClientTick`이 있지만 required 목록에는 없음.
- `PlayerData` required에 `LastProcessedClientTick`이 있음.
- Gameplay example의 모든 player가 ACK를 정확히 한 번 포함함.
- Human example은 양수 또는 0, bot example은 0임.
- Starting/started control은 계속 `Players: null`임.
- OpenAPI에 gameplay `PlayerData` 또는 `ClientTick` schema가 생기지 않음.

- [ ] **Step 2: RED를 확인합니다**

Run:

```bash
rtk go test ./internal/docs -run TestHandlerServesClientTickACKContract -count=1
rtk node docs-ui/scripts/validate.mjs
```

Expected: 기존 version `0.4.0`과 누락된 fields/examples 때문에 FAIL.

- [ ] **Step 3: AsyncAPI와 ai-docs를 실제 계약에 맞춥니다**

- `api/asyncapi.yaml` info version을 `0.5.0`으로 올립니다.
- `InputMessage.ClientTick`은 `type: integer`, `format: int64`, `minimum: 0`이며 optional로 둡니다.
- `PlayerData.LastProcessedClientTick`은 동일 integer contract이고 required입니다.
- Input ordering, stale/duplicate silence, legacy 0, ACK 처리 시점, match/reconnect reset을 설명합니다.
- Starting과 started control `Players:null`, first gameplay ACK snapshot을 예제로 고정합니다.
- Human/bot 예제의 ACK와 `IsBot`을 일관되게 갱신합니다.
- `ai-docs/`에 room pending guard와 simulation-owned ACK data flow를 기록하고 SL-94를 다음 작업 목록에서 제거합니다.
- `api/openapi.yaml`은 변경하지 않습니다.

- [ ] **Step 4: Focused, docs, full CI를 fresh run으로 검증합니다**

Run:

```bash
rtk go test ./internal/simulation -run 'TestStep.*(ClientTick|ACK|ProcessedInput|UnprocessedInput)' -count=20
rtk go test ./internal/rooms -run 'Test(InputMessage|SetInput|WebSocket.*ClientTick|MergedTickInputs.*ClientTick)' -count=20
rtk go test -race ./internal/rooms ./internal/simulation
rtk go test ./internal/docs -count=1
rtk node docs-ui/scripts/validate.mjs
rtk make docs-build
rtk make ci
rtk git diff --check
rtk git diff -- api/openapi.yaml
```

Expected: 모두 PASS, 마지막 두 diff command 출력 없음.

- [ ] **Step 5: Task 3을 commit합니다**

```bash
rtk git add api/asyncapi.yaml docs-ui/scripts/validate.mjs internal/docs/docs_test.go ai-docs/api-reference.md ai-docs/api-docs.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/decisions.md ai-docs/project-map.md
rtk git commit -m "[SL-94] docs(api): publish ClientTick ACK contract" -m "- bump AsyncAPI to 0.5.0 with optional input ticks
- require per-player processed ACK in gameplay snapshots
- document monotonic, legacy, lifecycle, and reconnect behavior"
```

---

## Final SL-94 Review Gate

- [ ] `git diff sl-91-bot-autofill...HEAD`가 SL-94 runtime/docs만 포함하는지 확인합니다.
- [ ] Spec compliance reviewer와 code quality reviewer의 Critical/Important 지적을 모두 해결합니다.
- [ ] Targeted `-count=20`, relevant `-race`, docs validation, `make ci`를 fresh run으로 다시 실행합니다.
- [ ] Linear SL-94에 monotonic policy, validation, PR link를 기록하고 상태를 `In Review`로 옮깁니다.
- [ ] Branch를 push하고 base `sl-91-bot-autofill`인 ready-for-review PR을 생성합니다.
