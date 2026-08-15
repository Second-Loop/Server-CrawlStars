# SL-118 Shelly 완전 재장전과 5.4타일 대시 구현 계획

## 목표

SL-120의 typed `reload_dash` 설정을 실제 Shelly 효과로 연결해, 승인 tick에 탄약을 완전히 복구하고 일반 이동 뒤 위치에서 정확히 `5.4 tile`을 결정적으로 대시해요.

## 고정 계약

- 승인 조건과 cooldown은 기존 `tryApproveSkill`을 그대로 사용해요.
- Reload는 charge를 `normalAttack.maxCharges`로 만들고 recharge 진행도를 `0`으로 초기화해요.
- Dash 거리는 config `5.4 * tile.size = 6.48 world`이고 정규화된 `AttackDir`을 사용해요.
- Wall, Water, boundary, live player는 막고 Ground, SpawnPoint, Bush, dead player는 통과해요.
- 일반 이동을 먼저 적용한 뒤 모든 승인된 Shelly dash를 연속 시간 `0..1` batch로 처리해요.
- 최초 접촉 위치에서 진행 방향 반대로 `1e-6 world` 보정하고, event time은 `1e-12` 허용오차로 묶어요.
- Partial/blocked dash도 reload와 cooldown을 유지해요.
- 새 wire field/event, Client 변경, bot skill, 넉백·무적·범용 ability framework는 추가하지 않아요.

## 구현 순서

1. `internal/simulation/skill_dash_test.go`에 reload/reset, 축·대각선 exact distance, map/boundary/live/dead player collision, 동시 dash와 input reversal 테스트를 RED로 추가해요.
2. `dispatchApprovedSkill`이 Shelly reload를 적용하고 dash intent를 반환하도록 좁히며, `State.Step`이 모든 intent를 한 batch로 실행하게 해요.
3. `internal/simulation/skill_dash.go`에 swept circle 대 map rounded AABB, boundary, fixed/moving player 접촉과 연속 시간 event loop를 구현해요.
4. 기존 `skill_test.go`의 effect-free Shelly 기대값을 새 canonical reload/dash 계약에 맞추고, 일반 이동·공격·GameEnd 회귀를 실행해요.
5. `ai-docs/architecture.md`, `ai-docs/project-map.md`, `ai-docs/protocol.md`, `ai-docs/decisions.md`, `ai-docs/api-reference.md`와 source validator를 실제 효과 경계로 갱신해요.
6. focused test, 공식 docs 검증, `make ci`, 독립 코드 리뷰를 통과한 정확한 HEAD만 PR로 게시해요.

## 완료 기준

- Linear SL-118 Acceptance Criteria가 직접적인 회귀 테스트와 문서로 추적돼요.
- 독립 리뷰에 Critical/Important가 없고 GitHub CI가 통과해요.
- squash merge SHA의 깨끗한 detached worktree에서 `make ci`가 통과해요.
