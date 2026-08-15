# SL-119 Colt 일반·스킬 연사 구현 계획

## 목표

SL-120의 typed `burst_projectile` 설정을 실제 Colt 스킬로 연결하고, 일반 공격과 스킬의 canonical cadence를 하나의 결정적 tick scheduler에서 실행해요.

## 고정 계약

- 일반 공격은 activation tick `A+[0,3,6,9,12,15]`에 `default` projectile 6발을 생성해요.
- 스킬은 approval tick `S+[0,2,4,6,7,9,11,13,14,16,18,20]`에 `colt_skill` projectile 12발을 생성해요.
- 스킬 projectile은 config의 damage `320`, range `11 tile`, speed `13 world/s`, radius `0.3`을 사용해요.
- 각 projectile은 emission tick의 post-movement owner 위치에서, approval 때 고정한 방향으로 생성돼요.
- 스킬 승인 시 아직 due가 되지 않은 일반 burst를 취소하고 skill burst로 교체해요. Step pre-phase에서 이미 수집된 same-tick due emission은 committed 결과로 유지해요.
- Skill burst가 active인 동안 일반 공격은 charge를 소비하거나 queue하지 않으며, 마지막 emission tick에도 겹치지 않고 다음 tick부터 승인해요.
- Owner가 projectile pre-phase에서 사망하면 현재·미래 due emission을 취소해요. Approval 또는 pre-phase 수집 뒤 same-tick melee로 사망한 경우 이미 committed된 emission만 유지하고 이후분은 취소해요.
- Projectile emission은 owner ID, emission phase, ordinal 순서로 정규화해 input/map iteration 순서와 무관하게 만들어요.
- 새 goroutine/worker, wire field/event, Client 변경, bot skill, 벽 파괴·관통, 범용 scripting engine은 추가하지 않아요.

## 구현 순서

1. `internal/simulation/skill_burst_test.go`에 exact 12발 schedule, config-owned stat/range, 방향·현재 위치, normal cancellation, attack lock, cooldown 재입력, 사망, 동시 owner 결정성 테스트를 RED로 추가해요.
2. 기존 `projectileEmission`과 `burstState`가 normal/skill 공통 projectile burst spec을 사용하도록 좁게 정리해요.
3. Colt skill dispatch가 active normal burst를 교체하고 ordinal 0 emission을 commit하도록 `State.Step`에 연결해요.
4. 기존 일반 6발 cadence, charge/recharge, collision/tombstone/GameEnd 회귀를 focused test와 race test로 확인해요.
5. `ai-docs/architecture.md`, `ai-docs/project-map.md`, `ai-docs/protocol.md`, `ai-docs/decisions.md`, `ai-docs/api-reference.md`, AsyncAPI 설명과 docs validator를 현재 효과 경계로 갱신해요.
6. 공식 docs 검증, `make ci`, 독립 코드 리뷰를 통과한 exact HEAD만 PR로 게시해요.

## 완료 기준

- Linear SL-119 Acceptance Criteria 7개가 직접적인 테스트와 문서로 추적돼요.
- 독립 리뷰에 Critical/Important가 없고 GitHub CI가 통과해요.
- squash merge SHA의 깨끗한 detached worktree에서 `make ci`가 통과해요.
