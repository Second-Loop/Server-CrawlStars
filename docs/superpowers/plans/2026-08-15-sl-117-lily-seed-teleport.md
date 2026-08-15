# SL-117 Lily 씨앗·순간이동 구현 계획

## 목표

SL-120의 typed `teleport_projectile` 설정을 실제 Lily 스킬로 연결하고, 씨앗 피해 뒤 적의 피격 전 위치를 기준으로 결정적인 순간이동을 실행해요.

## 고정 계약

- ready 상태의 non-zero `AttackDir` 스킬 승인은 `lily_seed` projectile 하나를 현재 위치에 생성하고 cooldown `330 tick`을 즉시 소비해요.
- 씨앗은 config의 피해 `400`, 사거리 `10.4 tile`, 속도 `13 world/s`, 반경 `0.3 world`를 사용해요.
- 기존 projectile과 같은 mode eligibility를 사용하고 Wall·boundary에 막히며 Bush·Water는 통과해요.
- 적중 직전 target 위치·반경과 seed 방향을 보존한 뒤 피해·사망을 먼저 적용해요.
- target이 죽어도 Lily가 살아 있으면 보존한 위치에서 seed 방향으로 `max(1 tile, radius 합 + 1e-6)` 떨어진 목적지를 계산해요.
- 우선 목적지가 막히면 같은 ray의 허용 구간에서 blocker contact interval을 합쳐 가장 큰 유효 거리를 선택해요. 고정 간격 sampling은 사용하지 않아요.
- 순간이동 목적지는 Wall·Water·boundary·다른 live player가 막고, hit target·Lily 자신·dead player는 blocker에서 제외해요.
- 유효 목적지가 없거나 owner가 이미 죽었으면 피해만 유지해요. 이미 생성된 seed는 owner 사망으로 제거하지 않아요.
- 동시 접촉 target은 `PlayerID` 오름차순으로 하나를 선택하고, active projectile 생성 sequence에 따른 처리 순서를 유지해요.
- 새 wire field/event, Client 변경, bot skill, 범용 teleport system은 추가하지 않아요.

## 구현 순서

1. `internal/simulation/skill_teleport_test.go`에 승인 snapshot, stat/range, mode·map 규칙, 피해/사망 순서, 목적지·backoff·blocker, 결정성 테스트를 RED로 추가해요.
2. `projectileRuntime`에 Lily 전용 teleport metadata를 붙이고 `SkillTeleportProjectile` dispatch가 activation emission을 만들도록 연결해요.
3. projectile hit이 target을 `PlayerID`로 정규화해 선택하고, 피해 적용 뒤 Lily 전용 목적지 solver를 호출하게 해요.
4. 목적지 solver는 boundary, expanded Wall/Water AABB, expanded live-player circle의 금지 거리 구간을 계산·합성해 canonical backoff를 선택해요.
5. Room GameEnd가 teleport까지 반영된 최종 snapshot을 사용한다는 회귀 테스트를 추가해요.
6. `ai-docs/architecture.md`, `ai-docs/project-map.md`, `ai-docs/protocol.md`, `ai-docs/decisions.md`, `ai-docs/api-reference.md`, AsyncAPI 설명과 docs validator를 현재 효과 경계로 갱신해요.
7. focused/race/docs/전체 CI와 독립 코드 리뷰를 통과한 exact HEAD만 PR로 게시해요.

## 완료 기준

- Linear SL-117 Acceptance Criteria 7개가 직접적인 테스트와 문서로 추적돼요.
- 독립 리뷰에 Critical/Important가 없고 GitHub CI가 통과해요.
- squash merge SHA의 깨끗한 detached worktree에서 `make ci`가 통과해요.
