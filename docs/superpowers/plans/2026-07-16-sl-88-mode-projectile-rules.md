# SL-88 모드별 Projectile 규칙 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Solo/Team/Duel의 selected mode에 맞는 projectile 피격 규칙과 같은 tick의 결정적 처리 순서를 구현해요.

**Architecture:** `State.gameConfig.SelectedMode.Rules`를 hit eligibility의 단일 기준으로 사용해요. Target tie-break는 승인된 기존 `s.players` join/배정 순서를 유지하고, room map에서 온 input slice만 PlayerID stable sort해요.

**Tech Stack:** Go 1.25, table-driven tests, AsyncAPI 3.0, Node.js docs validator, `make ci`.

## Global Constraints

Fixed base는 `3f91d8619a6e916221dceeb1289a1ac26d217686` (`sl-87-six-player-ready`, PR #45)예요.

- Solo는 owner와 dead player를 제외한 모든 live player를 적으로 판정해요.
- Team과 Duel의 `two_teams + friendlyFire=false`는 ally를 피해 없이 통과하고 enemy만 맞혀요.
- 같은 tick에 여러 eligible target이 겹치면 기존 `s.players` join/배정 순서의 첫 target 하나만 맞혀요. PlayerID로 target을 다시 정렬하지 않아요.
- room의 map 순서가 simulation input과 projectile 결과에 영향을 주지 않도록 input을 복사한 뒤 PlayerID stable sort해요.
- SL-89의 탈락, 승패, GameEnd, room cleanup은 구현하지 않아요.
- JSON schema shape, mode catalog 값, OpenAPI는 바꾸지 않아요.
- `api/openapi.yaml`, `internal/rooms/game_end.go`, `internal/rooms/game_end_test.go`는 fixed base와 같아야 해요.

---

### Task 1: Mode Eligibility와 결정적 처리

**Files:**

- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/simulation.go`
- Test: `internal/simulation/game_config_test.go`
- Test: `internal/simulation/simulation_test.go`

**Interfaces:**

- Produces: `func (s *State) canProjectileHit(projectile ProjectileData, target PlayerData) bool`
- Produces: `func (s *State) playerTeam(playerID PlayerID) (Team, bool)`
- Produces: `func orderedInputsByPlayerID(inputs []InputCommand) []InputCommand`
- Preserves: `s.players` 순서와 첫 eligible overlap 우선 규칙

- [ ] **Step 1: 관계별 collision matrix와 unknown rule test를 먼저 추가**

`TestStepProjectileCollisionMatrix`를 table-driven test로 만들고 아래 행을 실제 `State.Step`으로 검증해요.

| Mode | Overlap | Expected HP | Destroyed |
| --- | --- | --- | --- |
| Solo | owner | unchanged | false |
| Solo | dead player | unchanged | false |
| Solo | 같은 Team label의 live non-owner | damage | true |
| Team | ally only | unchanged | false |
| Team | dead enemy | unchanged | false |
| Team | enemy | damage | true |
| Duel | owner | unchanged | false |
| Duel | dead opponent | unchanged | false |
| Duel | live opponent | damage | true |

같은 위치에 `target-z`, `target-a` 순으로 넣은 별도 행은 `target-z`만 피해를 받아 기존 join/배정 순서를 고정해요. `TestValidateGameConfigRejectsUnsupportedTeamBehavior`는 `teamBehavior="unsupported"`가 validation error를 반환해야 해요.

- [ ] **Step 2: RED를 확인**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestStepProjectileCollisionMatrix|TestValidateGameConfigRejectsUnsupportedTeamBehavior' -count=1
```

Expected: Team ally 행은 ally HP 감소 또는 projectile destroy로 실패하고, unsupported behavior는 error가 없어 실패해요.

- [ ] **Step 3: supported team behavior만 허용**

`validateGameModeConfig`의 non-empty 검사 뒤에 다음 enum validation을 넣어요.

```go
switch mode.Rules.TeamBehavior {
case TeamBehaviorFreeForAll, TeamBehaviorTwoTeams:
default:
	return fmt.Errorf("game config mode.rules.teamBehavior %q is not supported", mode.Rules.TeamBehavior)
}
```

- [ ] **Step 4: mode rule 기반 hit eligibility를 구현**

기존 `applyProjectileHit`의 player slice 순회와 첫 hit `return`은 유지하고, owner/dead 조건만 아래 helper 호출로 교체해요.

```go
func (s *State) canProjectileHit(projectile ProjectileData, target PlayerData) bool {
	if target.ID == projectile.OwnerID || target.IsDead {
		return false
	}

	rules := s.gameConfig.SelectedMode.Rules
	switch rules.TeamBehavior {
	case TeamBehaviorFreeForAll:
		return true
	case TeamBehaviorTwoTeams:
		if rules.FriendlyFire {
			return true
		}
		ownerTeam, ok := s.playerTeam(projectile.OwnerID)
		return ok && ownerTeam != target.Team
	default:
		return false
	}
}

func (s *State) playerTeam(playerID PlayerID) (Team, bool) {
	for i := range s.players {
		if s.players[i].ID == playerID {
			return s.players[i].Team, true
		}
	}
	return "", false
}
```

- [ ] **Step 5: input ordering RED를 실제 collision tick까지 확인**

`TestStepNormalizesInputOrderThroughCollision`은 같은 players에서 reversed input 두 상태를 만들어요. Projectile 생성 snapshot뿐 아니라 이동·충돌 tick까지 진행한 뒤 전체 snapshot, HP, projectile ID/order/destroy가 같고 caller input slice는 바뀌지 않았음을 비교해요.

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run TestStepNormalizesInputOrderThroughCollision -count=1
```

Expected: input slice 순서가 projectile 생성 순서와 ID에 반영되어 snapshot mismatch로 실패해요.

- [ ] **Step 6: input을 복사해 PlayerID stable sort**

`sort` import를 추가하고 `State.Step`의 input loop를 다음 helper 결과로 바꿔요.

```go
func orderedInputsByPlayerID(inputs []InputCommand) []InputCommand {
	if len(inputs) < 2 {
		return inputs
	}
	ordered := append([]InputCommand(nil), inputs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].PlayerID < ordered[j].PlayerID
	})
	return ordered
}
```

- [ ] **Step 7: GREEN과 package regression을 확인하고 commit**

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestStep(ProjectileCollisionMatrix|NormalizesInputOrderThroughCollision)|TestValidateGameConfigRejectsUnsupportedTeamBehavior' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -count=1
git add internal/simulation/game_config.go internal/simulation/game_config_test.go internal/simulation/simulation.go internal/simulation/simulation_test.go
git commit -m "[SL-88] feat(simulation): 모드별 projectile 규칙 적용"
```

### Task 2: AsyncAPI와 사람용 문서 동기화

**Files:**

- Modify: `api/asyncapi.yaml`
- Modify: `docs-ui/scripts/validate.mjs`
- Modify or explicitly review: `ai-docs/api-docs.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md` (`ADR-0030`)
- Verify unchanged: `api/openapi.yaml`

- [ ] **Step 1: AsyncAPI semantic marker RED를 추가**

`ProjectileData` description에 `Solo`, `Team`, `friendlyFire=false`, `join/배정 순서`, `PlayerID 오름차순 input`이 모두 있어야 한다는 assertion을 `docs-ui/scripts/validate.mjs`에 먼저 넣고 실행해요.

```bash
node docs-ui/scripts/validate.mjs
```

Expected: 기존 `ProjectileData` description에 marker가 없어 실패해요.

- [ ] **Step 2: source spec과 사람용 문서를 갱신**

- Solo는 owner/dead 제외, Team/Duel은 ally pass-through와 enemy hit을 설명해요.
- eligible multi-contact는 join/배정 순서의 첫 target, input 적용은 PlayerID stable sort라고 구분해요.
- death snapshot 이후 elimination/GameEnd는 SL-89 범위라고 명시해요.
- `ADR-0030`에 사용자 승인 선택 `1-A`와 기존 동작 보존 이유를 기록해요.
- `ai-docs/api-docs.md`는 관련 설명이 있으면 갱신하고, 없으면 report에 unchanged review 근거를 남겨요.

- [ ] **Step 3: docs GREEN과 contract 검증 후 commit**

```bash
node docs-ui/scripts/validate.mjs
make docs-build
/Users/hyunjun/.npm/_npx/0929aae77d023606/node_modules/.bin/asyncapi validate api/asyncapi.yaml
cmp api/asyncapi.yaml internal/docs/api/asyncapi.yaml
git diff --exit-code 3f91d8619a6e916221dceeb1289a1ac26d217686..HEAD -- api/openapi.yaml
git add api/asyncapi.yaml docs-ui/scripts/validate.mjs ai-docs/api-docs.md ai-docs/api-reference.md ai-docs/protocol.md ai-docs/architecture.md ai-docs/project-map.md ai-docs/decisions.md
git commit -m "[SL-88] docs(protocol): 모드별 projectile 판정 문서화"
```

Generated `internal/docs/api/asyncapi.yaml`은 stage하지 않아요.

## 최종 검증

```bash
node docs-ui/scripts/validate.mjs
make docs-build
/Users/hyunjun/.npm/_npx/0929aae77d023606/node_modules/.bin/asyncapi validate api/asyncapi.yaml
cmp api/asyncapi.yaml internal/docs/api/asyncapi.yaml
make ci
git diff --check 3f91d8619a6e916221dceeb1289a1ac26d217686..HEAD
git diff --exit-code 3f91d8619a6e916221dceeb1289a1ac26d217686..HEAD -- api/openapi.yaml
git diff --name-only 3f91d8619a6e916221dceeb1289a1ac26d217686..HEAD
```

최종 diff는 SL-88 projectile eligibility, 결정성, validation, 관련 문서에만 제한해요.
