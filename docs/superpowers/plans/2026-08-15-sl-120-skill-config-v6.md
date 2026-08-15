# SL-120 Skill Config v6과 탄약 Snapshot 구현 계획

**Goal:** SL-115의 승인 설계를 server config v6의 typed skill 계약과 exact Colt cadence로 옮기고, 일반 공격 탄약 상태를 gameplay snapshot과 AsyncAPI 0.8.0에 공개해요.

**Architecture:** `internal/simulation.GameConfig`가 캐릭터별 skill kind와 projectile schedule의 유일한 runtime source예요. 기존 private `attackState`는 그대로 판정 소유자로 두고, 각 `Step` 마지막에 `PlayerData.AttackCharges`와 `NextAttackChargeTick`으로 투영해 transport가 재계산하지 않게 해요. Skill 승인은 typed config를 반환하고 좁은 no-effect dispatch 경계를 통과하지만, 실제 세 캐릭터 효과는 후속 티켓까지 실행하지 않아요.

**Baseline:** `origin/main@d1d21c1c4db95ca7ee172e7bb157a0c2b4a4dae9`

## Task 1: v6 config 계약을 실패 테스트로 고정

**Files:**
- Modify: `internal/simulation/game_config_test.go`
- Modify: `server-config/game-config.json`

1. canonical artifact가 version 6, 세 skill kind, exact 수치, `colt_skill`/`lily_seed`, Colt 일반·스킬 offset 전체를 가진다는 테스트를 먼저 추가해 실패를 확인해요.
2. unknown kind, kind별 필수/금지 field, invalid offset, count 불일치, non-zero interval 혼용, unknown projectile type을 직접 반대 mutation으로 거부하는 table test를 추가해요.

## Task 2: typed SkillConfig와 exact schedule을 구현

**Files:**
- Modify: `internal/simulation/game_config.go`
- Modify: `server-config/game-config.json`
- Test: `internal/simulation/game_config_test.go`

1. `ServerGameConfigVersion=6`, `SkillKind`, kind별 typed payload와 strict flat JSON decode/validation을 추가해요.
2. `ProjectileAttackConfig.EmissionOffsetsTicks`를 추가하고 explicit offsets가 있으면 `count` 일치, 첫 값 0, strictly increasing, `intervalTicks=0`을 강제해요.
3. Static/artifact config에 Shelly 5.4, Colt normal `[0,3,6,9,12,15]`, Colt skill `[0,2,4,6,7,9,11,13,14,16,18,20]`, Lily 10.4와 새 projectile type을 넣어요.

## Task 3: Colt 일반 공격이 exact offsets를 소비

**Files:**
- Modify: `internal/simulation/normal_attack_test.go`
- Modify: `internal/simulation/normal_attack.go`

1. activation tick 기준 `0,3,6,9,12,15`에만 6발이 생성되는 RED 테스트를 추가해요.
2. burst due tick을 `emissionOffsetsTicks[nextOrdinal]`에서 읽도록 바꾸고 기존 interval 기반 테스트를 새 정본에 맞춰요.

## Task 4: 탄약 snapshot projection 구현

**Files:**
- Modify: `internal/simulation/simulation.go`
- Modify: `internal/simulation/normal_attack_test.go`
- Modify: `internal/simulation/skill_test.go`

1. 첫 snapshot의 max/0, 공격 소비 직후, 부분 recharge, max 복구, 반환 snapshot 변조 격리를 RED 테스트로 고정해요.
2. `PlayerData`에 required 두 field를 추가하고 snapshot tick `T`에서 max면 `0`, 아니면 `T + (R-r)`를 private state로부터 투영해요.

## Task 5: typed skill dispatch 경계를 제공

**Files:**
- Modify: `internal/simulation/skill.go`
- Modify: `internal/simulation/skill_test.go`
- Modify: `internal/simulation/simulation.go`

1. 승인된 skill이 정확한 character kind/config를 반환하고 cooldown/zero-direction 거절은 dispatch하지 않는 테스트를 추가해요.
2. `tryApproveSkill`이 검증된 `SkillConfig`를 반환하고 `applyPreparedInput`이 exhaustive kind switch의 no-effect dispatch를 호출하게 해요.

## Task 6: AsyncAPI 0.8.0과 사람용 문서를 동기화

**Files:**
- Modify: `api/asyncapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `docs-ui/scripts/build.mjs`
- Modify: `internal/docs/docs_test.go`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/api-docs.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/project-map.md`

1. AsyncAPI `info.version=0.8.0`, gameplay `PlayerData` required fields·schema·모든 예시를 먼저 검증하도록 source/docs 테스트를 RED로 바꿔요.
2. config v6 exact 값, 탄약 절대 tick 의미, control snapshot의 null 계약, REST/Client 무변경을 문서와 validator에 동기화해요.
3. pinned AsyncAPI CLI, docs validate/build, docs Go tests를 실행해요.

## Task 7: 통합 검증과 전달

1. 관련 package 테스트, `git diff --check`, exact branch HEAD `make ci`를 실행해요.
2. 독립 코드 리뷰의 Important 이상을 수정하고 재검증해요.
3. SL-120 전용 PR을 열고 GitHub CI 성공 뒤 squash merge해요.
4. merge SHA의 clean worktree에서 `make ci`를 다시 실행하고 Linear AC/근거를 갱신해요.
