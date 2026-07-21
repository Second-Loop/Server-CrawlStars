# SL-83 캐릭터별 일반 공격 설계

## 1. 목표와 범위

`PressedAttack` 입력을 선택한 `CharacterType`의 server-authoritative 설정으로 해석해 Shelly 산탄, Colt 직선 연사, Lily 근접 공격을 결정적으로 실행해요.

포함 범위:

- 캐릭터별 피해량, 사거리, projectile 수, 퍼짐, 발사 간격, charge 수
- 기존 SL-81 attack charge 소비·회복 경계 재사용
- projectile hit, death, GameEnd와 같은 기존 simulation 경계 유지
- 설정값 변경만으로 같은 attack kind의 파라미터 변경

제외 범위:

- SL-84/85의 스킬 입력·쿨타임·효과
- client 구현과 client config parser 정합화
- 최종 밸런싱
- SL-98의 필수 `CharacterType`
- destroyed projectile snapshot 정리

## 2. 확정된 제품 계약

### Shelly

- 한 charge로 projectile 5개를 같은 tick에 생성해요.
- 방향 offset은 `-12, -6, 0, 6, 12`도예요.
- projectile 하나당 피해량은 280, 사거리는 7.2 tiles예요.
- 최대 charge는 3개예요.

### Colt

- 한 charge로 6발 전체를 승인하고 다른 연사와 중첩하지 않아요.
- activation snapshot tick을 `A`라고 할 때 emission tick은 `A + [0, 6, 12, 18, 24, 30]`이에요.
- 방향은 activation 시점 값으로 고정하고, 각 탄은 emission tick의 이동 완료 후 현재 owner 위치에서 생성해요.
- 연사 중 추가 공격은 유효 input ACK만 처리하고 charge를 소비하지 않아요.
- 마지막 탄 emission tick에도 기존 연사를 active로 판정해 재공격을 거절하고, 그 다음 tick부터 새 연사를 승인해요.
- activation snapshot만 `PressedAttack=true`이고 자동 emission은 false예요.
- owner가 죽으면 미래 emission을 취소하고 charge는 환불하지 않아요. 이미 생성된 projectile은 유지해요.
- projectile 하나당 피해량은 340, 사거리는 9 tiles, 최대 charge는 3개예요.

### Lily

- 이동 완료 후 Lily 중심에서 정규화된 `AttackDir` 방향으로 2.2 tiles 길이의 centerline을 만들어요.
- 두께를 추가하지 않고 centerline과 target의 기존 player circle이 교차하면 맞아요. 끝점 접촉도 hit예요.
- Wall과 map boundary가 선분을 먼저 자르고 Bush와 Water는 통과해요. wall과 target이 같은 지점에서 접하면 wall이 우선해요.
- owner, 이미 dead인 player, mode 규칙상 공격할 수 없는 ally를 제외한 첫 대상 한 명만 맞혀요. 대상 우선순위는 canonical player 순서예요.
- miss 또는 wall block이어도 방향·charge 검증을 통과한 공격은 승인되고 charge 1개를 소비해요.
- 피해량은 1100, 최대 charge는 2개예요.
- 같은 tick의 Lily 피해는 일괄 적용해 상호 사망 Draw를 보존해요.

### 공통

- recharge는 기존 30 ticks를 유지해요.
- projectile speed/radius는 기존 `13 / 0.3`을 유지해요.
- 사거리는 `rangeTiles * resolved map tileSize`인 projectile 중심의 최대 이동 거리예요.
- resolved tile size는 `state map.TileSize -> gameConfig.Map.TileSize -> TileSize(1.2)` 순서로 fallback해 mapless `NewState(Config{})`에서도 0이 되지 않아요.
- 마지막 이동은 남은 거리만큼 clamp하고 `Wall/boundary -> player hit -> range expiry` 순으로 판정해 끝점 hit을 허용해요.
- charge는 공격 승인 시 소비하며, 이후 miss·wall block·owner death에는 환불하지 않아요.

## 3. 접근안 비교

### A. kind별 parameterized config와 공통 실행기 — 선택

`normalAttack.kind`만 분기하고 피해량, 사거리, count, offset, interval은 config에서 읽어요. 캐릭터별 Go switch가 없고 invalid 조합을 config load 시 거절할 수 있어요.

### B. 탄환별 emission 목록

각 탄의 delay와 angle을 배열로 기록하면 가장 유연하지만 현재 3종에 비해 설정이 장황하고 중복이 많아요.

### C. CharacterType별 Go strategy

초기 구현은 짧지만 설정값만으로 공격 파라미터가 바뀌어야 한다는 Acceptance Criteria를 만족하지 못하므로 사용하지 않아요.

## 4. Config 계약

Client artifact는 v2를 byte-unchanged로 유지하고 server config만 v3로 올려 `ClientGameConfigVersion = 2`, `ServerGameConfigVersion = 3`처럼 버전 상수를 분리해요. SL-83은 `[Server]` 티켓이므로 Unity parser alias와 단위 변환을 포함하지 않아요.

각 player type은 다음 구조를 가져요.

```json
{
  "characterType": 0,
  "id": "shelly",
  "radius": 0.5,
  "hp": 4000,
  "speed": 2,
  "normalAttack": {
    "kind": "spread_projectile",
    "damagePerHit": 280,
    "rangeTiles": 7.2,
    "maxCharges": 3,
    "rechargeTicks": 30,
    "projectile": {
      "type": "default",
      "directionOffsetsDegrees": [-12, -6, 0, 6, 12],
      "intervalTicks": 0,
      "count": 5
    }
  }
}
```

지원 kind:

- `spread_projectile`: offset 하나당 같은 tick에 하나 생성하고 interval은 0이어야 해요.
- `burst_projectile`: 단일 방향 offset과 양수 count/interval을 사용해 순차 emission해요.
- `melee`: projectile block을 가지면 안 돼요.

기존 player-level `maxAttackCharges`와 `attackRechargeTicks`는 v3에서 제거해 source of truth를 하나로 만들어요. Projectile type은 `id/radius/speed`만 소유하고 damage는 normal attack이 소유해요. Projectile catalog의 ID는 non-empty·unique여야 하고 normal attack reference는 exact ID lookup으로 해석해요. 생성 시 referenced projectile type의 `id/radius/speed`를 public `ProjectileData.Type/Radius/Speed`로 복사하고 attack damage를 `ProjectileData.Damage`로 복사해요.

Config load는 다음을 거절해요.

- 지원하지 않는 kind
- finite positive가 아닌 damage/range
- positive가 아닌 charge/recharge
- projectile kind의 누락된 projectile block 또는 melee의 불필요한 block
- 존재하지 않는 projectile type reference
- 비어 있거나 중복된 projectile type ID
- spread의 count/offset 불일치 또는 non-zero interval
- burst의 count 1 이하, offset 0이 아닌 값, non-positive interval
- finite하지 않은 direction offset

Static fallback도 exact server v3 값을 사용해 embedded config 실패 시 동작이 달라지지 않게 해요.

## 5. Runtime 상태와 tick 순서

Private runtime 상태만 추가하고 wire DTO는 늘리지 않아요.

- player별 `attackState`: charge와 recharge progress
- player별 optional `burstState`: 고정 방향, 다음 ordinal, 다음 emission tick
- projectile ID별 `projectileRuntime`: 최대 사거리와 이미 이동한 거리
- Step-local `attackIntent`와 `meleeIntent`

추천 Step 순서:

1. transient `PressedAttack`을 false로 초기화해요.
2. 각 player의 CharacterType 설정으로 charge를 recharge해요.
3. 기존 projectile을 남은 사거리까지 이동하고 wall/hit/range expiry를 판정해요.
4. `PlayerID`순 input을 검증하고 ACK·movement를 적용한 뒤 attack intent를 모아요.
5. charge와 active burst를 확인해 신규 공격을 승인해요.
6. 신규/예약 projectile emission을 `OwnerID -> ordinal` 순으로 정렬해 생성해요.
7. post-movement player snapshot에서 Lily target을 정하고 피해를 player index별로 누적해 한 번에 적용해요.
8. tick을 증가시키고 snapshot을 반환해요.

기존 projectile 때문에 3단계에서 죽은 player는 input과 예약탄을 실행하지 않아요. 같은 attack phase에서 Lily에게 죽는 Colt는 이미 due인 탄까지 생성하고 미래 emission만 취소해 행동 우선순위 편향을 만들지 않아요.

Colt의 charge recharge는 active burst와 독립적으로 계속 진행해요. 마지막 emission tick `A+30`에도 단계 5에서 burst를 active로 유지해 그 tick의 재공격을 거절하고, 마지막 탄 생성 뒤 burst를 완료해 `A+31`부터 새 activation을 허용해요.

Go map 순회 순서는 projectile ID나 snapshot 순서에 사용하지 않아요. Same-tick emission은 owner ID와 ordinal로 정렬하고 melee damage는 `State` 생성/assignment 순서인 canonical player index slice에 누적해 input slice 순서가 바뀌어도 결과가 같아야 해요.

## 6. 기존 계약과 문서 영향

- REST/OpenAPI 필드는 바뀌지 않아요.
- WebSocket/AsyncAPI 필드는 바뀌지 않지만 `PressedAttack`, projectile 생성·사거리, damage 의미를 확인하고 설명과 예제를 갱신해요.
- `ai-docs/protocol.md`, `architecture.md`, `project-map.md`, `decisions.md`, `api-reference.md`를 현재 동작과 맞춰요.
- `docs-ui/scripts/validate.mjs`의 v2 server attack budget assertion을 v3 nested contract로 바꿔요.
- 기존 destroyed projectile tombstone 동작은 유지해요.

## 7. 검증 전략

TDD로 config validation부터 실패 테스트를 추가하고 runtime을 kind별로 확장해요.

Focused tests:

- config v3 exact catalog, projectile ID unique/exact lookup, invalid 조합, static fallback, client/server version 분리
- Shelly exact 5 angles, damage/range/charge, config-only parameter mutation
- Colt emission tick `1/7/13/19/25/31`, 고정 방향, 이동 spawn, non-overlap, ACK, death cancellation
- Lily range/endpoint/tangent, wall/boundary, Bush/Water, target order, miss charge, reciprocal lethal Draw
- 캐릭터별 charge `3/3/2`와 independent recharge
- mapless tile-size fallback, range endpoint hit, wall priority, expiry, friendly-fire/mode matrix
- 기존 projectile hit/death와 room GameEnd 회귀
- input 순서 역전과 반복 실행의 exact snapshot 결정성

최종 증거:

- focused Go tests
- 반복 결정성 test
- `go test -race` 대상 package
- exact HEAD의 clean detached worktree에서 `make ci`
- 구현자와 분리한 독립 최종 코드 리뷰
