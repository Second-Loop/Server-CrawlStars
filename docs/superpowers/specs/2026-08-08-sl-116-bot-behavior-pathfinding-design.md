# SL-116 서버 봇 상세 행동과 A* 길찾기 설계

## 1. 목적과 경계

SL-65의 Client prototype에서 검증한 `dodge`, `explore`, `retreat`, `chase`와 4방향 A*를 서버 소유 bot input 생성 경계로 이관해요. 행동 의도와 기획 수치는 유지하지만 Unity `Time.time`, `Random`, dictionary 순회 순서에는 의존하지 않아요. 같은 room seed, config, authoritative snapshot, controller state는 항상 같은 `InputCommand`를 만들어요.

이 설계는 다음 경계를 지켜요.

- Bot 판단은 `internal/rooms`가 소유하고 사람과 같은 `InputCommand -> simulation.State.Step` 경계를 사용해요.
- `internal/simulation`은 bot 전용 gameplay 경로를 만들지 않아요.
- Client 저장소, public REST/WebSocket schema, Client UI는 바꾸지 않아요.
- Bot의 `PressedSkill`은 계속 `false`예요.
- Disconnect player를 bot으로 교체하거나 난이도·학습형 AI를 추가하지 않아요.

## 2. 확정한 제품 결정

### 2.1 행동 우선순위와 공격은 분리해요

Bot은 매 gameplay tick에 이동 행동을 하나만 선택해요.

```text
hostile projectile 위협 있음 -> dodge
아니고, 탐지 범위 안 live enemy 없음 -> explore
아니고, HP / maxHP <= 0.20 -> retreat
그 외 -> chase
```

공격 판단은 이동 상태와 독립적으로 수행해요. 탐지한 target이 캐릭터의 normal attack range 안에 있고 기존 server-authoritative attack cadence가 다시 승인 가능한 tick이면 같은 target 방향으로 `PressedAttack: true`를 만들어요. Dodge 또는 retreat 중에도 이 조건을 만족하면 공격할 수 있어요.

- 적 탐지 거리는 `15` world-unit이고 거리 제곱이 `15 * 15` 이하인 target을 포함해요.
- 거리 동률은 `PlayerID` 오름차순으로 해소해요.
- Bot skill은 범위 밖이므로 skill range나 `SkillReadyTick`은 공격 판단에 사용하지 않아요.
- Client의 0.3초 attack throttle은 옮기지 않아요. 기존 normal attack charge와 Colt burst 종료를 기준으로 room이 기억한 canonical next-attack tick을 사용해요.

### 2.2 Explore는 고정 seed로 목적지를 선택해요

탐지 범위 안에 live enemy가 없으면 map의 passable tile에서 explore 목적지를 하나 선택해요.

- Passable tile은 Ground, SpawnPoint, Bush예요. Wall과 Water는 제외해요.
- 후보는 row-major `(y, x)` 순서로 정렬해요.
- 후보가 둘 이상이면 bot이 현재 서 있는 tile은 제외해요.
- 길이 prefix를 붙인 `room ID`, 길이 prefix를 붙인 `bot PlayerID`, big-endian uint64 `exploreEpoch`을 canonical byte sequence로 합쳐 SHA-256을 계산해요. Digest의 첫 8 bytes를 big-endian uint64로 읽고 후보 수로 나눈 index를 사용해요.
- 목적지를 새로 선택할 때만 `exploreEpoch`을 1 증가시켜요.
- 목적지 중심에서 `0.25` world-unit 안에 도착하면 다음 tick에 새 목적지를 선택해요.
- A*가 목적지까지 경로를 찾지 못하면 목적지를 즉시 폐기하고 다음 tick에 새 seed epoch으로 다시 선택해요.

Room ID가 다르면 explore 경로가 달라질 수 있지만 같은 room replay에서는 재현돼요. 전역 RNG와 shared mutable seed는 만들지 않아요.

### 2.3 Retreat는 최대 6 world-unit 안의 유효 위치로 줄여요

현재 HP가 캐릭터 max HP의 20% 이하이고 target이 있으면 target 반대 방향으로 후퇴해요.

1. `bot.Pos - targetDirection * 6`을 raw target으로 계산해요.
2. Bot 위치에서 raw target까지 map grid를 supercover 방식으로 순회해요.
3. 먼 cell부터 가까운 cell 순서로 player radius를 포함해 Wall, Water, boundary와 겹치지 않는 첫 tile center를 선택해요.
4. 선택한 tile center를 A* goal로 사용해요.
5. 현재 tile 외에 유효 후보가 없거나 A*가 실패하면 이동은 zero예요.

Player는 dynamic obstacle이므로 retreat goal 후보에서 제외하지 않아요. 실제 같은 tick player-player 충돌은 기존 simulation의 결정적 movement resolution이 처리해요.

### 2.4 A*는 4방향과 고정 tie-break를 사용해요

A*는 room-local immutable map을 읽고 다음 한 tile만 반환해요.

- 이웃은 상, 하, 좌, 우 4방향이에요.
- Wall과 Water는 blocked이고 Ground, SpawnPoint, Bush는 passable이에요.
- 이동 비용 `G`는 tile마다 1이고 heuristic `H`는 Manhattan distance예요.
- Open-set 동률은 `F -> H -> y -> x` 오름차순으로 해소해요.
- Start와 goal이 같은 tile이면 world target을 향한 direct unit direction을 반환해요.
- Start 또는 goal이 invalid/blocked이거나 open set이 소진되면 path failure예요.
- Explore path failure는 목적지를 폐기해요. Chase와 retreat path failure는 그 tick의 `MoveDir`을 zero로 두되 `AttackDir`과 공격 판단은 유지해요.

Bot controller는 `(start tile, goal tile)`이 같을 때 next tile/direction을 cache할 수 있어요. Map은 room 동안 immutable하므로 cache invalidation은 start 또는 goal 변경으로 충분해요. Player 위치는 A* obstacle에 넣지 않아요.

### 2.5 Dodge는 hostile projectile만 결정적으로 합성해요

Projectile은 다음 조건을 모두 만족할 때 threat예요.

- `IsDestroyed: false`예요.
- Owner가 bot 자신이 아니고 selected mode에서 bot에게 damage를 줄 수 있어요.
- Projectile 진행 방향으로 bot이 앞에 있고 forward distance가 `0 < d <= 8` world-unit예요.
- Projectile ray와 bot 중심의 최근접 거리가 `bot radius + projectile radius + 0.35` 이하예요.

Threat는 `ProjectileID` 오름차순으로 정렬해 각 ray에서 멀어지는 unit vector를 합해요. 합이 non-zero면 정규화한 값을 dodge 후보로 사용해요.

합이 상쇄되거나 bot 중심이 ray와 정확히 겹치면 다음 규칙을 사용해요.

1. 예상 충돌 forward distance가 가장 작은 threat를 선택해요.
2. 동률은 `ProjectileID` 오름차순이에요.
3. 해당 projectile 방향의 `+90°`, `-90°` 순서로 한 tick 이동 후보를 만들어요.
4. Player radius를 포함한 map collision이 없는 첫 방향을 선택해요.
5. 양쪽이 모두 막히면 `MoveDir`은 zero예요.

Projectile owner의 hit eligibility는 실제 projectile collision과 drift하지 않도록 simulation의 mode rule을 재사용하는 순수 helper로 한 번만 정의해요. Dodge를 포함한 최종 movement 후보는 SL-121의 one-tick live-player avoidance를 통과해요. Human pending input과 모든 bot raw input을 먼저 모아 candidate pair의 swept collision이 예상되면 진행 방향 기준 `+90°`, `-90°` 순서로 map/player-safe 후보를 고르고, authoritative 충돌과 위치는 기존 Step resolution이 확정해요.

## 3. 책임과 상태 소유권

### 3.1 Room이 controller state를 소유해요

Room은 bot별 private controller state를 보관해요.

```text
botControllerState
  exploreEpoch
  hasExploreDestination
  exploreDestination
  cachedPathStart
  cachedPathGoal
  cachedPathNext
```

- Bot fill 또는 debug bot 생성 후 첫 gameplay tick에 lazy initialize해요.
- Bot 사망 시 새 입력은 만들지 않지만 room 종료 전까지 state 보존 여부는 gameplay 결과에 영향을 주지 않아요.
- Bot이 participant에서 제거되거나 room이 cleanup되면 controller state와 attack cadence state를 함께 제거해요.
- Goroutine, timer, global controller singleton은 추가하지 않아요.
- `room.lastPlayers`와 projectile snapshot이 authoritative observation이에요. Client listener나 이전 Client projectile position cache를 사용하지 않아요.
- A* cache는 first-step tile을 보존하고, 현재 tile 안의 authoritative world position으로 매 tick steering을 다시 계산해요. 경로가 꺾일 때는 현재 tile의 수직축 중앙을 먼저 맞춰 player radius가 blocked corner를 자르지 않아요.

### 3.2 Simulation은 공통 입력 경계를 유지해요

Room은 human pending input과 bot-generated input을 PlayerID별 하나로 합치고 PlayerID 오름차순으로 `State.Step`에 전달해요. Bot command는 계속 `ClientTick: 0`, `PressedSkill: false`예요.

Simulation이 소유하는 기존 규칙은 바꾸지 않아요.

- movement clamp와 Wall/Water/boundary/player collision
- normal attack charge/recharge와 burst schedule
- projectile collision, HP, death, GameEnd
- Solo/Team/Duel hit eligibility

Bot controller는 입력을 제안할 뿐 이동·공격 성공을 확정하지 않아요.

## 4. Server config v5

`server-config/game-config.json` exact version을 `5`로 올리고 server-only `bot` section을 추가해요.

```json
{
  "version": 5,
  "bot": {
    "detectionRangeWorld": 15,
    "exploreArrivalDistanceWorld": 0.25,
    "retreatHpRatio": 0.2,
    "retreatDistanceWorld": 6,
    "projectileLookAheadWorld": 8,
    "dodgeMarginWorld": 0.35
  }
}
```

- 모든 거리 값은 world-unit이고 finite positive여야 해요.
- `retreatHpRatio`는 `0 < value <= 1`이어야 해요.
- Explore seed와 A* tie-break는 gameplay 규칙이므로 runtime 환경 변수로 바꾸지 않아요.
- Client config와 public API에는 bot tuning 값을 노출하지 않아요.
- Embedded config, static test config, loader validation, docs marker를 v5에 맞춰요.

SL-115가 이후 skill effect config를 추가하면 그 구현은 SL-116 병합 뒤 최신 version에서 순차적으로 올려요. 두 ticket을 같은 config version으로 병렬 구현하지 않아요.

## 5. Tick 데이터 흐름

```text
room gameplay tick N
  -> authoritative lastPlayers / projectiles / selected mode / map 읽기
  -> bot별 controller state 준비
  -> target 탐지와 hostile projectile threat 계산
  -> dodge / explore / retreat / chase 중 MoveDir 하나 선택
  -> normal attack range와 canonical next-attack tick으로 AttackDir/PressedAttack 결정
  -> human pending input과 bot input을 PlayerID 오름차순으로 merge
  -> simulation.State.Step
  -> snapshot을 room.lastPlayers와 delivery 경로에 반영
  -> 승인된 bot attack이면 기존 next-attack tick 갱신
```

한 tick에서 여러 bot은 같은 이전 authoritative snapshot을 보고 각 입력을 만들어요. 앞선 bot의 controller 결정이나 slice 순서가 뒤 bot의 관측을 바꾸지 않아요. 실제 동시 movement와 collision은 한 번의 `State.Step`에서 결정해요.

## 6. 오류와 경계 동작

| 상태 | 결과 |
| --- | --- |
| dead/non-bot player | bot input 없음 |
| 탐지 범위 안 live enemy 없음 | deterministic explore |
| passable explore 후보 없음 | zero movement, 다음 tick 재시도 |
| A* path failure | explore 재선택 또는 chase/retreat zero movement |
| dodge 양방향 map-blocked | zero movement, 독립적인 공격 판단 유지 |
| retreat 유효 후보 없음 | zero movement |
| target이 attack range 밖 | aim은 유지할 수 있으나 `PressedAttack: false` |
| attack cadence blocked | movement/aim 유지, `PressedAttack: false` |
| invalid bot config | listener 시작 전 startup failure |
| room cleanup | controller/cache/cadence state 함께 제거 |

Invalid runtime observation을 복구하기 위한 random fallback이나 panic은 만들지 않아요. Public error event도 추가하지 않아요.

## 7. 문서와 wire 계약

- REST와 WebSocket field는 바뀌지 않으므로 OpenAPI/AsyncAPI schema version은 올리지 않아요.
- `api/openapi.yaml`, `api/asyncapi.yaml`은 새 field가 없음을 확인하고 필요한 설명만 갱신해요.
- `ai-docs/architecture.md`에 room-owned bot controller와 simulation input 경계를 기록해요.
- `ai-docs/protocol.md`, `api-reference.md`, `project-map.md`에 결정적 bot 행동과 server config v5를 반영해요.
- Client source link는 migration evidence로만 유지하고 Client repo에는 commit하지 않아요.

## 8. 테스트 설계

### 8.1 Pure controller와 pathfinding

- Player 순열과 거리 동률에서 같은 target과 input을 만드는지 확인해요.
- 15 world-unit 직전/경계/직후 target 탐지를 table test로 확인해요.
- 같은 room/bot/epoch가 같은 explore tile을, epoch 또는 bot ID 변화가 deterministic한 새 tile을 만드는지 확인해요.
- 현재 tile 제외, 도착 0.25 경계, path failure 뒤 epoch 증가를 확인해요.
- A*의 shortest path, `F/H/y/x` 동률, blocked goal, disconnected map, start=goal을 확인해요.
- HP ratio 20% 직전/경계와 6 world-unit retreat backoff를 확인해요.
- 단일·다중·상쇄 projectile, ID 순열, 양쪽 map-blocked dodge를 확인해요.
- Own/ally/destroyed/behind/8 world-unit 밖 projectile을 threat에서 제외해요.

### 8.2 Room과 simulation 회귀

- Human pending input과 bot input이 authoritative PlayerID로 unique merge되는지 확인해요.
- 여러 bot이 player/projectile slice 순서와 무관하게 같은 input batch를 만드는지 확인해요.
- Attack-ready exact tick, Colt burst 완료 뒤 재시도, movement/aim 유지 회귀를 확인해요.
- Solo/Team/Duel target과 hostile projectile eligibility가 실제 damage rule과 일치하는지 확인해요.
- Bot fill, human-only Ready, player collision, reconnect, death, GameEnd, room cleanup을 확인해요.
- Focused tests 뒤 affected race/repeat tests와 전체 `make ci`를 실행해요.

## 9. 완료 조건

- SL-116 acceptance criteria와 이 문서의 경계 표에 대응하는 자동화 테스트가 있어요.
- 같은 room seed/config/snapshot/controller state가 항상 같은 bot input을 만들어요.
- Dodge, explore, retreat, chase 우선순위와 독립적인 공격 판단이 확정된 값으로 동작해요.
- A*는 Wall/Water를 통과하지 않고 path failure에서 정해진 zero/reselect 동작을 사용해요.
- Bot은 기존 공통 `InputCommand -> State.Step` 경계를 유지하고 skill을 요청하지 않아요.
- Client repo와 public API schema를 바꾸지 않아요.
- Server config v5, runtime, tests, `ai-docs/`가 일치해요.
- `make ci`가 통과하고 PR과 Linear comment에 검증 결과를 남겨요.
