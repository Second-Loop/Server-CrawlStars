# SL-84 스킬 입력 계약과 서버 권위 쿨타임 설계

## 1. 목적과 경계

WebSocket input에 명시적인 `PressedSkill` trigger를 추가하고, simulation이 캐릭터별 쿨타임을 tick으로 판정해요. Client는 gameplay snapshot의 승인 결과와 절대 ready tick만 보고 스킬 사용 여부와 남은 쿨타임을 표시할 수 있어요.

이 설계는 다음 경계를 지켜요.

- SL-84는 입력, 승인 우선순위, 쿨타임, snapshot 계약까지만 구현해요.
- 캐릭터별 damage, projectile, 이동, 피격 같은 실제 스킬 효과는 보류한 SL-85에서 다뤄요.
- Client UI와 client-local 실행은 범위 밖이에요. 서버 snapshot이 gameplay 판정의 기준이에요.
- Bot controller는 계속 `PressedSkill: false`를 생성해요.

## 2. 확정한 제품 결정

### 2.1 `PressedSkill`은 command별 독립 시도

- WebSocket input의 optional `PressedSkill`이 누락되면 `false`예요.
- `PressedSkill: true`는 해당 command에서 새 스킬 activation을 한 번 시도한다는 뜻이에요.
- 쿨타임 중인 시도는 그 tick에서 끝나며 queue, rising-edge 상태, 버튼 latch를 만들지 않아요.
- Client가 새 command마다 `true`를 반복해서 보내면 준비 완료 첫 tick의 command가 승인될 수 있어요.
- 조준 방향은 새 필드 없이 기존 `AttackDir`을 재사용해요.
- 필드가 존재할 때 `null`을 포함해 JSON boolean이 아니면 기존 malformed input 경로로 거부해요. 기존 `PressedAttack` decode 의미는 바꾸지 않아요.

### 2.2 사용 가능한 스킬이 일반 공격보다 우선

같은 command에서 `PressedSkill`과 `PressedAttack`이 모두 `true`이면 다음 순서로 판정해요.

1. 기존 input payload, player, `ClientTick`, 방향 validation을 적용해요.
2. 스킬 요청이 있고 방향이 non-zero이며 쿨타임이 준비됐는지 판정해요.
3. 스킬이 eligible이면 스킬만 승인하고 일반 공격 charge는 유지해요.
4. 스킬이 ineligible이면 스킬 상태를 바꾸지 않고 기존 일반 공격 판정으로 fallback해요.
5. 스킬을 승인한 뒤 SL-85의 effect가 miss 또는 block되더라도 일반 공격으로 fallback하지 않아요.

| 스킬 상태 | 일반 공격 charge | 결과 |
| --- | --- | --- |
| ready | 있음 | 스킬만 승인, charge 유지 |
| cooldown | 있음 | 기존 일반 공격 승인 시도 |
| ready | 없음 | 스킬 승인 |
| cooldown | 없음 | 둘 다 미승인 |
| invalid input | 무관 | 둘 다 미승인, 기존 상태 보존 |

이미 시작한 Colt burst와 기존 projectile은 새 command의 일반 공격 activation이 아니므로 스킬 승인 여부와 무관하게 기존 schedule을 계속 처리해요.

### 2.3 절대 ready tick 경계

Gameplay snapshot에서 command를 처리해 반환하는 tick을 activation tick `A`, 캐릭터 cooldown을 `C`라고 해요.

- 초기 상태는 `PressedSkill: false`, `SkillReadyTick: 0`이에요.
- 현재 activation tick `A >= SkillReadyTick`이면 쿨타임이 준비된 상태예요.
- 승인 snapshot에는 `PressedSkill: true`, `SkillReadyTick: A + C`를 기록해요.
- 정확히 `A + C` tick부터 다음 activation을 승인할 수 있어요.
- `PressedSkill`은 다음 `State.Step` 시작에 다시 `false`가 돼요.
- `SkillReadyTick`은 다음 승인 때 덮어쓸 때까지 유지해요. Remaining cooldown을 별도 필드로 매 tick 보내지 않아요.

Client는 `max(0, SkillReadyTick - Snapshot.Tick)`으로 남은 tick을 계산할 수 있어요. 초 단위 표시와 client artifact의 tick-rate 전달은 SL-99/client 범위이며, SL-84는 새 tick-rate wire field를 추가하지 않아요. 사용 가능 판정은 서버가 이미 확정한 snapshot을 따라요.

### 2.4 승인 snapshot 전달 경계

일반 non-terminal gameplay snapshot은 client별 capacity-1 latest-only slot에서 coalescing해 느린 client를 격리해요. 단, 어느 player라도 `PressedSkill: true`인 non-terminal snapshot은 기존 size-8 reliable control FIFO로 승격하는 reliable approval exception이에요. 승격 전에 older pending normal snapshot과 기존 deferred normal snapshot을 버리고, reliable approval write가 성공해 승인 pending이 모두 drain될 때까지 이후 일반 snapshot은 session별 deferred latest 하나만 교체 보관해요. multiple approval은 FIFO로 전달하며 모든 승인 write 성공 뒤 approval -> latest 순서로 최신 일반 snapshot 하나를 flush해요.

accepted approval은 terminal보다 먼저 drain하고, 그 뒤 terminal snapshot -> GameEnd -> close를 실행하며 deferred normal snapshot은 종료 시 버려요. reliable queue overflow/write failure는 silent loss가 아니라 해당 session close/release의 fail-closed이고, 무한히 느린 session을 유지한다고 보장하지 않아요. PressedAttack: true-only snapshot은 계속 latest-only예요. 새 wire field/event를 추가하지 않으며 AsyncAPI dialect 3.0.0/info 0.7.0, control snapshot의 `Players: null`과 `Projectiles: null`을 유지하고, SL-85 effect는 이번 범위에서 제외하며, SL-99 client config v3/server config v4 경계를 유지해요.

이 bounded delivery는 무한히 느린 session 유지나 application-level ACK/replay를 보장하지 않아요.

## 3. 상태와 config 소유권

### 3.1 `PlayerData`가 canonical cooldown state를 소유

`simulation.PlayerData`에 다음 두 필드를 추가해요.

```go
PressedSkill  bool `json:"PressedSkill"`
SkillReadyTick Tick `json:"SkillReadyTick"`
```

- `PressedSkill`은 해당 snapshot tick의 서버 승인 결과인 transient state예요.
- `SkillReadyTick`은 다음 사용 가능 절대 tick인 persistent state예요.
- 별도 private `skillStates` map이나 snapshot mirror를 만들지 않아요.
- 기존 `State.players -> clonePlayers -> room.lastPlayers -> WebSocket Snapshot` 흐름이 cooldown 상태를 그대로 보존해요.
- Reconnect도 같은 room의 마지막 authoritative snapshot과 이후 simulation state를 보므로 별도 복원 경로가 필요하지 않아요.
- 일반 공격 charge는 충전량과 recharge progress가 필요한 private `attackState`를 계속 사용해요. 시간 모델이 다른 두 상태를 일반화하지 않아요.

### 3.2 Server config v4

`server-config/game-config.json`을 exact version `4`로 올리고 각 player type에 cooldown만 담은 `skill`을 추가해요.

```json
{
  "characterType": 0,
  "id": "shelly",
  "skill": {
    "cooldownTicks": 360
  }
}
```

| CharacterType | 캐릭터 | `cooldownTicks` | 30Hz 기준 |
| ---: | --- | ---: | ---: |
| `0` | Shelly | `360` | 12초 |
| `1` | Colt | `390` | 13초 |
| `2` | Lily | `330` | 11초 |

- `SkillConfig`는 SL-84에서 `CooldownTicks int`만 가져요. Effect 종류나 수치는 SL-85 전까지 넣지 않아요.
- Config loader는 server version이 정확히 `4`인지와 모든 캐릭터의 `cooldownTicks > 0`을 검증해요.
- Embedded config, static fallback, config test fixture를 모두 v4와 같은 값으로 맞춰요.
- `client-config/game-config.json`과 `ClientGameConfigVersion`은 SL-84에서 바꾸지 않아요. 별도 SL-99 artifact의 로컬 값이 있더라도 network gameplay UI의 authoritative 기준은 `Snapshot.Tick`과 `SkillReadyTick`이에요.

## 4. Simulation 처리 흐름

`State.Step`은 기존 결정적 처리 순서를 유지하면서 combat activation 선택만 확장해요.

```text
Step 시작
  -> 모든 player의 PressedAttack / PressedSkill을 false로 reset
  -> attack recharge, 기존 projectile 이동, due burst 수집
  -> input을 PlayerID 오름차순으로 stable sort
  -> player/input/ClientTick 검증과 ACK 갱신
  -> 이동과 AttackDir 정규화
  -> skill eligibility 판정
       -> eligible: PressedSkill=true, SkillReadyTick=A+C, 일반 공격 생략
       -> ineligible: 기존 PressedAttack 판정으로 fallback
  -> 기존 melee/projectile intent 적용
  -> tick 증가와 snapshot clone
```

Activation tick `A`는 현재 Step이 반환할 `s.tick + 1`이에요. Room은 player별 pending command를 하나만 Step에 넘겨요. 이 public 경계에서 `PlayerID` 정렬, config, 이전 snapshot 상태가 같으면 서로 다른 player command의 input slice 순서와 무관하게 같은 결과가 나와요.

유효한 양수 `ClientTick`은 스킬 쿨타임으로 거절돼도 기존 규칙대로 `LastProcessedClientTick`에 반영해요. Unknown/dead player, non-finite 방향, 음수 또는 stale tick은 스킬과 일반 공격을 모두 바꾸지 않고 ACK도 올리지 않아요.

## 5. Wire 계약

### 5.1 WebSocket input

```json
{
  "ClientTick": 12,
  "MoveDir": { "x": 0, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false,
  "PressedSkill": true
}
```

- `inputMessage`와 `simulation.InputCommand`에 `PressedSkill bool`을 추가해요.
- Wire field는 optional이며 누락 시 `false`예요.
- Room pending input의 기존 last-write-wins와 positive `ClientTick` admission 규칙을 그대로 사용해요.
- Bot input merge는 human과 같은 `InputCommand`를 사용하지만 controller가 `PressedSkill: false`를 명시적으로 유지해요.

### 5.2 Gameplay snapshot

```json
{
  "Tick": 25,
  "Players": [
    {
      "Id": "player-id",
      "PressedAttack": false,
      "PressedSkill": true,
      "SkillReadyTick": 385
    }
  ]
}
```

- AsyncAPI gameplay `PlayerData`의 required 목록에 `PressedSkill`, `SkillReadyTick`을 추가해요.
- `PressedSkill`은 boolean, `SkillReadyTick`은 minimum `0`인 integer예요.
- Starting/started control snapshot의 `Players: null`과 `Tick: 0`은 바꾸지 않아요.
- Ready event와 REST `Player`에는 cooldown state를 추가하지 않아요. Simulation이 시작된 뒤 gameplay snapshot만 authoritative state를 제공해요.

### 5.3 계약 버전과 문서

- `api/asyncapi.yaml` info version을 `0.7.0`으로 올리고 input, snapshot schema, 우선순위, 예제를 갱신해요.
- `api/openapi.yaml`에는 WebSocket input과 gameplay `PlayerData`가 없으므로 변경하지 않고 회귀만 확인해요.
- `ai-docs/api-reference.md`, `api-docs.md`, `protocol.md`, `architecture.md`, `decisions.md`, `project-map.md`를 구현과 맞춰요.
- `docs-ui/scripts/validate.mjs`, `internal/docs/docs_test.go`, generated docs embed가 AsyncAPI `0.7.0`과 required field를 고정해요.

## 6. 오류와 경계 동작

| 입력/상태 | 처리 결과 |
| --- | --- |
| `PressedSkill` 누락 또는 `false` | 스킬 시도 없음 |
| `PressedSkill: true`, `AttackDir` zero | 스킬 ineligible, 일반 공격도 방향 조건 때문에 미승인 |
| `PressedSkill: true`, ready | 스킬 승인과 ready tick 갱신 |
| `PressedSkill: true`, cooldown | 스킬 state 불변, 같은 command의 일반 공격만 fallback 가능 |
| 쿨타임 command 뒤 입력 없음 | 준비돼도 자동 activation 없음 |
| 매 tick 새 `true` command | 정확히 첫 ready tick에서 새 시도 승인 가능 |
| wrong JSON type 또는 malformed frame | 기존 `invalid_input`, pending authoritative state 보존 |
| dead/non-finite/negative/stale input | 기존 validation대로 둘 다 미승인 |

SL-84에는 effect 단계가 없으므로 스킬 승인은 cooldown과 snapshot state만 바꿔요. 이 승인 계약이 SL-85의 effect 실행 전제이며, effect 결과가 일반 공격 fallback을 다시 열지는 않아요.

## 7. 테스트 설계

### 7.1 Config와 simulation

- Server config v3를 거부하고 v4를 load하는지 확인해요.
- 세 캐릭터 cooldown `360/390/330`과 positive validation, embedded/static fallback 일치를 확인해요.
- 초기 `PressedSkill=false`, `SkillReadyTick=0`과 Step 시작 transient reset을 확인해요.
- Activation `A`, 직전 tick `A+C-1`, 경계 tick `A+C`를 table test로 확인해요.
- Cooldown 중 true가 ready tick을 연장하거나 중복 소비하지 않고, 입력이 없을 때 queue된 activation이 나타나지 않는지 확인해요.
- 반복 true command가 첫 ready tick에만 새 activation을 승인하는지 확인해요.
- 같은 tick의 skill/attack 우선순위 5개 조합을 table test로 확인해요.
- 스킬 승인 시 effect가 없는 SL-84에서도 일반 공격을 실행하지 않고 attack charge를 보존하는지 확인해요. SL-85는 이 회귀 위에 miss/block case를 추가해요.
- Cooldown fallback 때 기존 캐릭터별 일반 공격만 정상 소비하는지 확인해요.
- 진행 중 Colt burst와 기존 projectile이 스킬 입력으로 취소되지 않는지 확인해요.
- Input 순서를 바꿔도 동일 config와 tick에서 snapshot 결과가 같은지 확인해요.
- Cooldown-blocked positive command는 ACK하고 invalid/dead/non-finite/negative/stale command는 기존 상태와 ACK를 보존하는지 확인해요.

### 7.2 Room, WebSocket, 문서

- `PressedSkill` 누락/false/true가 room pending input과 `State.Step`에 정확히 전달되는지 확인해요.
- Wrong JSON type과 malformed payload가 기존 `invalid_input` 경로를 사용하고 이후 snapshot stream이 유지되는지 확인해요.
- 기존 `PressedAttack`, positive/legacy `ClientTick`, reconnect, bot merge 회귀를 확인해요.
- Gameplay snapshot의 두 required field와 control snapshot의 `Players: null`을 확인해요.
- AsyncAPI schema/example, source marker, generated docs handler 검증을 실행해요.
- Focused simulation/rooms/docs tests 뒤 affected race/repeat test와 전체 `make ci`를 실행해요.

## 8. 범위 밖

- SL-85 캐릭터별 스킬 effect와 피격 판정
- Client UI, animation, audio, local prediction 또는 input latch
- `client-config/game-config.json`의 skill 수치와 SL-99 client artifact/parser
- Bot의 skill 사용 정책
- 일반 공격 charge/recharge 구조 일반화
- Player-player collision, disconnect 정책, WebSocket·matchmaking 간헐 오류, map collision 최적화 같은 SL-93 범위
- Respawn, score, persistence, production matchmaking

## 9. 완료 조건

- Linear SL-84의 acceptance criteria와 이 문서의 경계 표에 대응하는 자동화 테스트가 있어요.
- `PressedSkill=false` 또는 누락 입력은 스킬을 시도하지 않아요.
- Cooldown 중 true 입력이 새 승인, ready tick 연장, 일반 공격 charge 중복 소비를 만들지 않아요.
- 같은 이전 state, tick, config, player별 command는 input slice 순서와 무관하게 같은 결과를 만들어요.
- 기존 `PressedAttack`, `ClientTick`, bot, reconnect, control snapshot 계약이 회귀하지 않아요.
- Server config v4, AsyncAPI `0.7.0`, generated docs와 `ai-docs/`가 runtime과 일치해요.
- `make ci`가 통과하고 PR과 Linear comment에 검증 결과를 남겨요.
