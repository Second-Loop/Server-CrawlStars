# SL-115 캐릭터 스킬 효과 판정과 서버 상태 계약 설계

## 1. 목표와 기준

SL-80의 Shelly·Colt·Lily 스킬 기획을 SL-84의 `PressedSkill`·`SkillReadyTick` 승인 계약 위에서 결정적으로 실행할 수 있게 해요.

- 기준 코드는 `origin/main`의 `b441bc65e0df5ab3dd400a7810c1b7802e8bf740`예요.
- ready 상태에서 non-zero `AttackDir`로 스킬을 승인하면 cooldown을 즉시 소비해요.
- 승인 뒤 대시 차단, projectile miss, 순간이동 목적지 차단이 발생해도 cooldown을 환불하거나 일반 공격으로 fallback하지 않아요.
- Client가 gameplay 결과를 재판정하지 않도록 snapshot을 서버 권위 상태로 유지해요.
- 입력 순서, 내부 slice 순서, Go map 순서가 결과를 바꾸지 않게 해요.

Client UI·애니메이션·이펙트 구현, bot의 스킬 사용, 최종 밸런싱, 범용 스킬 scripting engine은 범위 밖이에요.

## 2. 선택한 구조

Server config의 `SkillConfig.kind`가 타입 안전한 효과 실행기를 선택해요.

- `reload_dash`: Shelly의 최대 재장전과 대시
- `burst_projectile`: Colt의 예약 연사
- `teleport_projectile`: Lily의 씨앗과 적중 후 순간이동

각 실행기는 효과별 필수 설정을 검증된 Go 타입으로 받아요. 대시 충돌, projectile 생성, burst 예약, projectile 적중 같은 낮은 수준의 helper는 공유하지만, 조건·대상·실행 phase를 조합하는 범용 DSL은 만들지 않아요.

`State`는 기존 일반 공격 상태와 별도로 진행 중인 스킬 burst를 소유해요.

```text
attackStates       캐릭터별 일반 탄약과 recharge 진행도
burstStates        기존 Colt 일반 공격 burst
skillBurstStates   Colt 스킬의 방향·activation tick·다음 ordinal
```

후속 캐릭터에서 동일한 효과 조합이 실제로 반복될 때만 별도 티켓으로 범용 엔진 승격을 검토해요.

## 3. 한 tick의 처리 순서

Snapshot tick `T = state.tick + 1`에서 다음 순서를 고정해요.

1. 모든 player의 transient `PressedAttack`, `PressedSkill`을 `false`로 초기화해요.
2. 일반 attack charge recharge를 진행해요.
3. 기존 active projectile을 생성 sequence 오름차순으로 이동하고 map 충돌, player hit, range 만료를 처리해요.
   - `lily_seed`의 피해와 순간이동도 이 단계에서 처리해요.
4. `T`에 이미 예정된 Colt 일반·스킬 projectile을 committed emission으로 수집해요.
5. input을 `PlayerID` 오름차순으로 검증해 ACK, `MoveDir`, `AttackDir`을 갱신해요.
6. 모든 live player의 일반 이동을 현재 동시 movement batch 규칙으로 처리해요.
7. ready이고 `AttackDir`이 non-zero인 스킬 시도를 승인하고 cooldown을 즉시 소비해요.
   - Shelly reload를 적용하고 모든 Shelly dash를 한 batch로 계산해요.
   - Colt의 일반 burst 미래분을 취소하고 skill burst를 시작해요.
   - Lily seed의 activation emission을 예약해요.
8. 스킬이 승인되지 않은 input만 기존 일반 공격으로 fall through해요.
   - Colt skill burst가 진행 중이면 일반 공격은 charge 소비와 queue 없이 무시해요.
9. 일반 melee intent의 same-tick batch 피해를 적용해요.
10. 이미 committed된 일반·스킬 projectile을 canonical emission 순서로 생성해요.
11. 완료된 burst를 정리하고 tick을 증가시켜 gameplay snapshot을 만들어요.
12. Room은 완성된 snapshot으로 기존 Win/Lose/Draw를 판정해요.

기존 projectile 단계에서 죽은 player는 같은 tick input을 실행하지 못해요. 반대로 skill 또는 공격이 이미 승인된 뒤 같은 tick의 후속 melee 피해로 owner가 죽어도 그 tick에 committed된 효과는 유지해요. 미래 Colt skill emission만 다음 tick부터 취소해요.

중간에 terminal 조건이 생겨도 tick을 조기 종료하지 않아요. 다른 live player의 행동과 이미 승인된 효과를 끝까지 정산한 최종 snapshot이 GameEnd의 유일한 입력이에요.

Lily seed가 3번에서 적중하면 teleport 뒤 6번 movement batch에 Lily와 살아남은 target을 포함한 모든 live player가 참여해요. 따라서 적중 직후에는 Lily가 target 뒤에 있지만, 같은 tick 입력 이동까지 끝난 최종 snapshot에서는 두 player가 더 이동했을 수 있어요.

## 4. 공통 결정성과 적중 순서

### 4.1 Projectile 순서

- active projectile은 내부 생성 sequence 오름차순으로 처리해요.
- input 순서나 `Projectiles` slice의 우연한 구성 순서를 계약으로 사용하지 않아요.
- 한 projectile이 같은 판정 위치에서 여러 eligible target과 동시에 접촉하면 `PlayerID` 오름차순 첫 대상 하나를 맞혀요.
- 먼저 처리된 피해로 죽은 target은 뒤 projectile의 eligible target에서 제외해요.
- map Wall·boundary 충돌은 기존처럼 player hit보다 우선하고, 미충돌 range 만료는 player hit 뒤에 처리해요.
- destroyed projectile은 기존 30 gameplay tick tombstone 계약을 유지해요.

예: 같은 projectile 위치에 `enemy-a`, `enemy-b`가 동시에 겹치면 거리가 아니라 `PlayerID`가 빠른 `enemy-a`만 피해를 받아요. 같은 tick의 다음 projectile은 갱신된 HP/IsDead를 보고 다시 target을 선택해요.

### 4.2 사망과 이미 생성된 projectile

owner 사망은 이미 생성된 projectile을 제거하지 않아요. 일반 projectile과 `colt_skill`, `lily_seed` 모두 남은 range까지 기존 규칙대로 이동하고 피해를 줄 수 있어요.

Colt의 예약 emission만 owner가 죽은 것을 확인한 다음 tick부터 취소해요. Lily seed는 owner가 죽어도 피해는 주지만 순간이동은 실행하지 않아요.

## 5. Shelly: 최대 재장전과 3타일 대시

### 5.1 Activation 효과

- 일반 이동을 먼저 적용한 post-movement 위치에서 시작해요.
- attack charge를 `normalAttack.maxCharges`까지 즉시 복구해요.
- 진행 중인 recharge tick도 `0`으로 초기화해요.
- `AttackDir` 방향으로 `3 tile = 3.6 world` 대시를 시도해요.
- 대시가 일부 이동하거나 완전히 막혀도 reload와 cooldown은 유지해요.

### 5.2 충돌 대상

Shelly의 player circle을 전체 대시 segment에 쓸어 보는 swept-circle 판정을 사용해요.

- 막음: Wall, Water, map boundary, live player
- 통과: Ground, SpawnPoint, Bush, dead player

최초 접촉 직전까지만 이동해요. 위치 보정에는 고정 `collisionEpsilon = 1e-6 world unit`을 사용하고, 정규화한 접촉 시간의 동률 비교에는 `collisionTimeEpsilon = 1e-12`를 사용해요.

### 5.3 여러 대시의 동시 처리

모든 Shelly dash는 정규화된 연속 시간 `0..1`의 한 batch로 계산해요.

1. post-movement 위치와 전체 dash vector로 active candidate를 만들어요.
2. map·boundary·고정 live player·active dash pair의 가장 이른 접촉 시간을 구해요.
3. 모든 active candidate를 그 시각까지 전진시켜요.
4. 같은 허용오차 안의 접촉을 한 event로 묶고, 관련된 active dasher의 transitive 집합을 모두 멈춰요.
5. 멈춘 player는 이후 active dasher의 고정 blocker가 돼요.
6. 남은 active dasher가 목적지에 도달하거나 모두 멈출 때까지 반복해요.

동률 event의 iteration은 `PlayerID` 오름차순으로 정규화하지만 pair 양쪽에 같은 결과를 적용하므로 input 순서와 무관해요.

예: 두 Shelly가 서로를 향해 같은 tick에 대시하면 두 원의 최초 접촉 직전에 함께 멈춰요. 한 Shelly가 먼저 벽에 멈춘 뒤 다른 Shelly의 경로를 막으면, 두 번째 Shelly도 그 멈춘 위치와의 최초 접촉 직전에 멈춰요.

## 6. Colt: 39틱 12발 연사

### 6.1 값과 schedule

- 피해: projectile당 `320`
- 사거리: `11 tile = 13.2 world`
- 속도: `13 world/s`
- 반경: `0.3 world`
- type: `colt_skill`
- emission offset: `[0, 4, 7, 11, 14, 18, 21, 25, 28, 32, 35, 39]`

Offset은 `round(i * 39 / 11)`, `i=0..11`과 같은 canonical 값이며, 첫 발부터 마지막 발까지 정확히 39 tick, 1.3초를 사용해요.

예: activation tick이 `100`이면 emission tick은 `100,104,107,111,114,118,121,125,128,132,135,139`예요.

### 6.2 방향, origin, 사망

- `AttackDir`은 activation tick에 정규화해 고정해요.
- 이후 input의 방향 변경은 진행 중인 skill burst에 영향을 주지 않아요.
- 각 projectile origin은 자신의 emission tick에 있는 Colt의 현재 위치예요.
- activation tick의 ordinal 0 projectile은 같은 tick에 committed돼요.
- Colt가 그 뒤 같은 tick melee 피해로 죽어도 ordinal 0은 생성돼요.
- owner가 이미 죽은 다음 tick부터 남은 ordinal을 취소해요.

### 6.3 일반 burst와의 관계

- skill 승인 시 기존 일반 6발 burst의 미래 emission을 취소해요.
- 그 tick의 입력 단계 전에 이미 due로 committed된 일반 탄환은 생성해요.
- skill ordinal 11이 생성될 때까지 새 일반 공격은 `PressedAttack=false`, charge 불변, queue 없음으로 처리해요.
- 일반 공격이 잠겨도 movement, aim, 유효한 양수 `ClientTick` ACK는 처리해요.
- skill cooldown `390 tick`이 burst 길이 `39 tick`보다 길어서 skill burst끼리는 겹치지 않아요.

## 7. Lily: 씨앗 피해와 적 뒤 순간이동

### 7.1 Seed projectile

- 피해: `400`
- 사거리: `8 tile = 9.6 world`
- 속도: `13 world/s`
- 반경: `0.3 world`
- type: `lily_seed`

일반 projectile과 같은 map 규칙을 사용해 Wall·boundary에는 막히고 Water는 통과해요. 적중 대상은 selected mode의 기존 projectile 적/아군 규칙을 재사용해요.

### 7.2 적중과 사망 순서

1. 적중 직전 target 위치, 반경, seed 방향을 보존해요.
2. target에게 400 피해를 적용하고 HP/IsDead를 갱신해요.
3. Lily owner가 살아 있으면 순간이동 목적지를 계산해요.
4. owner가 이미 죽었으면 피해만 유지하고 순간이동하지 않아요.

400 피해로 target이 죽어도 살아 있는 Lily는 보존한 pre-hit target 위치를 기준으로 순간이동해요. 비행 중 seed는 owner 사망만으로 소멸하지 않아요.

### 7.3 우선 목적지와 직선 backoff

`d`를 target 중심에서 seed 진행 방향으로 떨어진 중심 거리라고 해요.

```text
minimumClearance = target.Radius + lily.Radius + collisionEpsilon
desiredDistance  = max(behindDistanceTiles * tileSize, minimumClearance)
candidate(d)     = preHitTargetPos + seed.Dir * d
```

Canonical `behindDistanceTiles`는 `1`이에요. 현재 radius `0.5 + 0.5`, tile size `1.2`에서는 `desiredDistance=1.2 world`, `minimumClearance=1.000001 world`예요.

- 먼저 `candidate(desiredDistance)`를 검사해요.
- 막히면 같은 ray에서 `d`를 줄여 `[minimumClearance, desiredDistance]` 안의 가장 큰 유효 `d`를 선택해요.
- 고정 간격 sampling으로 근사하지 않고, blocker별 contact interval을 합쳐 가장 가까운 유효 경계를 결정적으로 계산해요.
- target은 minimum clearance로 분리하고 blocker 집합에서는 제외해요.
- Lily 자신과 dead player도 blocker에서 제외해요.
- Wall, Water, boundary, 다른 live player는 blocker예요.
- 유효 `d`가 없으면 피해만 적용하고 순간이동을 취소해요.

예: pre-hit target이 `(0,0)`, seed 방향이 `(+1,0)`이면 우선 목적지는 `(1.2,0)`이에요. 이 위치가 다른 live player와 겹치면 `d`를 줄여 `1.000001 <= d < 1.2`에서 가장 큰 유효 중심을 사용하고, 전체 구간이 막히면 Lily 위치를 바꾸지 않아요.

Teleport는 경로 이동이 아니므로 target과 후보 사이의 타일을 통과 여부로 검사하지 않고 최종 player circle만 검사해요. 목적지에 도착한 Lily는 같은 tick의 일반 movement batch에서 자신의 입력 이동을 이어서 적용해요.

## 8. Server config v6

SL-116 구현이 server config v5를 먼저 병합한 뒤 SL-115 구현 계열이 v6을 사용해요. 구현 branch는 v5에 rebase하고 v6 schema를 한 번만 도입해야 해요. Client config v3는 변경하지 않아요.

```json
{
  "skill": {
    "kind": "reload_dash",
    "cooldownTicks": 360,
    "dashDistanceTiles": 3
  }
}
```

```json
{
  "skill": {
    "kind": "burst_projectile",
    "cooldownTicks": 390,
    "damagePerHit": 320,
    "rangeTiles": 11,
    "projectile": {
      "type": "colt_skill",
      "emissionOffsetsTicks": [0, 4, 7, 11, 14, 18, 21, 25, 28, 32, 35, 39]
    }
  }
}
```

```json
{
  "skill": {
    "kind": "teleport_projectile",
    "cooldownTicks": 330,
    "damagePerHit": 400,
    "rangeTiles": 8,
    "behindDistanceTiles": 1,
    "projectile": {
      "type": "lily_seed"
    }
  }
}
```

`projectile.types`에는 다음 canonical entry를 추가해요.

```json
[
  { "id": "colt_skill", "radius": 0.3, "speed": 13 },
  { "id": "lily_seed", "radius": 0.3, "speed": 13 }
]
```

Config validation은 다음을 fail-fast로 거부해요.

- 알 수 없는 skill kind
- kind별 필수 field 누락 또는 다른 kind 전용 field 혼합
- 0 이하, NaN, Infinity인 거리·피해·cooldown
- 빈 emission offset, 첫 값이 0이 아님, 음수, 중복 또는 감소
- 정의되지 않은 projectile type 참조
- stable `CharacterType`과 skill kind의 canonical mapping drift

## 9. Snapshot과 API 계약

Gameplay `PlayerData`에 두 required field를 추가해요.

```text
AttackCharges         현재 server-authoritative 일반 attack charge
NextAttackChargeTick  다음 charge 1개가 복구되는 absolute tick, 최대면 0
```

Snapshot tick을 `T`, 현재 누적 recharge 진행도를 `r`, 설정값을 `R=normalAttack.rechargeTicks`라고 하면 다음과 같이 투영해요.

```text
AttackCharges == max  -> NextAttackChargeTick = 0
AttackCharges < max   -> NextAttackChargeTick = T + (R - r)
```

`r`은 `0 <= r < R`을 유지해요. Max 상태에서 charge를 소비한 tick에는 `r=0`이므로 다음 복구 tick은 `T+R`이에요. 이미 일부 recharge가 진행된 상태에서 charge를 더 소비해도 기존 `r`을 유지해 가장 가까운 복구 tick을 바꾸지 않아요. Shelly reload는 charge를 max로 만들고 `r=0`으로 초기화하므로 승인 snapshot에서 `AttackCharges=max`, `NextAttackChargeTick=0`이에요.

Snapshot 생성 시 private `attackStates`에서 두 field를 매번 투영해요. `NewStateWithConfig`에 전달된 외부 `PlayerData`의 두 값은 신뢰하지 않으며, 첫 gameplay snapshot부터 캐릭터별 canonical max charge와 `0`을 반환해요.

별도 `SkillEffect` 배열이나 효과별 event는 만들지 않아요.

- 승인: `PressedSkill`, `SkillReadyTick`
- 위치 효과: `Pos`
- 피해·사망: `HP`, `IsDead`
- projectile 효과: `ProjectileData.Type`의 `default`, `colt_skill`, `lily_seed`
- 탄약 효과: `AttackCharges`, `NextAttackChargeTick`

AsyncAPI dialect `3.0.0`은 유지하고 `info.version`을 `0.7.0`에서 `0.8.0`으로 올려요. Gameplay PlayerData required 목록, schema, 예시와 사람이 읽는 API 문서를 함께 갱신해요. Control snapshot의 `Players: null`, `Projectiles: null`, REST OpenAPI, Client 코드는 바꾸지 않아요.

`PressedSkill: true` snapshot의 기존 reliable approval 전달 규칙도 유지해요. 탄약과 위치 효과는 이 승인 snapshot에 함께 들어가므로 Client가 별도 effect event를 기다릴 필요가 없어요.

## 10. GameEnd 예시

- 기존 projectile이 tick 시작에 player를 죽이면 그 player의 input은 실행하지 않지만 다른 live player의 행동은 계속 정산해요.
- Colt skill ordinal 0이 승인된 뒤 같은 tick Lily melee로 Colt가 죽으면 ordinal 0은 생성되고 미래 ordinal만 취소해요.
- 서로의 이미 생성된 projectile이 같은 tick에 양쪽을 죽이면 최종 snapshot은 기존 mode evaluator에 의해 Draw가 될 수 있어요.
- Lily seed가 마지막 enemy를 죽이면 살아 있는 Lily의 teleport까지 반영한 snapshot을 만든 뒤 GameEnd를 판정해요.

## 11. 검증

### Config

- v6 canonical JSON의 세 skill 전체 clause와 projectile type map을 exact 비교해요.
- kind, required field, positive value, offset order, projectile reference의 직접 반대 mutation을 각각 거부해요.
- SL-116 v5 field를 잃지 않는 integration fixture를 둬요.

### Simulation

- Shelly 최대 reload, recharge reset, 3.6 world unobstructed dash
- Wall·Water·boundary·stationary player 접촉과 dead player 통과
- 두 개 이상 동시 dash, same-time contact, wall에 먼저 멈춘 dasher와 후속 접촉, input reversal 동일성
- Colt exact 12 offset, locked direction, emission-tick current origin, 일반 burst 선점, 일반 attack lock, owner death 취소
- Lily seed Wall/boundary 충돌, Water 통과, mode hit eligibility, lethal hit teleport, dead owner damage-only
- Lily desired destination, 직선 backoff, 최소 clearance 실패, live/dead player blocker 차이
- projectile 생성 sequence와 target `PlayerID` tie-break
- same-tick full settlement와 Win/Lose/Draw
- snapshot `AttackCharges`, `NextAttackChargeTick`의 초기·소비·recharge·Shelly reload 값

### 계약과 전체 검증

- `api/asyncapi.yaml`, `ai-docs/api-reference.md`, `ai-docs/protocol.md`, `ai-docs/decisions.md`를 함께 갱신해요.
- `docs-ui/scripts/validate.mjs`와 AsyncAPI CLI 검증을 실행해요.
- 최종 구현 branch의 정확한 HEAD에서 `make ci`를 통과해요.

## 12. 하위 티켓 전달

- SL-118은 Shelly의 reload·동시 swept dash 규칙을 직접 참조해요.
- SL-119는 Colt의 v6 config, exact schedule, burst 선점·잠금·사망 규칙을 직접 참조해요.
- SL-117은 Lily의 seed, 피해 후 teleport, 1타일 중심 간격과 직선 backoff 규칙을 직접 참조해요.

공통 v6 config·snapshot·skill runtime 기반의 구현 소유권과 하위 티켓 dependency 순서는 별도 구현 계획에서 정해요. SL-115 자체는 승인된 설계와 계약을 닫는 문서 티켓이며 실제 효과 구현을 포함하지 않아요.
