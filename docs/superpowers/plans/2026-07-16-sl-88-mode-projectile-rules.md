# SL-88 모드별 projectile 규칙 구현 계획

> Fixed base: `3f91d8619a6e916221dceeb1289a1ac26d217686` (`sl-87-six-player-ready`, PR #45)

## 목표와 경계

- Solo는 owner와 dead player를 제외한 모든 live player를 적으로 판정해요.
- Team과 Duel의 `two_teams + friendlyFire=false`는 ally를 피해 없이 통과하고 enemy만 맞혀요.
- 같은 tick에 여러 target이 겹칠 때의 tie-break는 구현 전에 사용자 결정을 받아요. 후보는 기존 join/player 순서 유지, PlayerID 오름차순, 이동 경로상 최초 접촉이에요.
- room의 map 순서가 simulation input과 projectile 결과에 영향을 주지 않도록 input을 복사한 뒤 PlayerID stable sort해요.
- SL-89의 탈락, 승패, GameEnd, room cleanup은 구현하지 않아요.
- JSON schema shape, mode catalog, OpenAPI는 바꾸지 않아요.

## Task 1: 관계별 collision matrix를 RED로 고정

수정 파일:

- `internal/simulation/simulation_test.go`
- `internal/simulation/game_config_test.go`

검증 행:

- Solo: owner, live opponent, dead player, 같은 Team label의 live opponent
- Team: owner, ally-only overlap, enemy, dead enemy
- Duel: owner, opponent, dead opponent
- 각 행은 HP 변화와 projectile `IsDestroyed`를 함께 검증해요.
- unknown `teamBehavior`는 현재 validation에서 수용되는 문제를 RED로 고정하고 supported enum 외 값을 거부하게 해요.

RED:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestStepProjectileCollisionMatrix|TestValidateGameConfigRejectsUnsupportedTeamBehavior' -count=1
```

## Task 2: mode eligibility와 결정적 처리 구현

수정 파일:

- `internal/simulation/game_config.go`
- `internal/simulation/simulation.go`
- `internal/simulation/simulation_test.go`

구현:

- `validateGameModeConfig`가 `free_for_all`, `two_teams`만 허용해요.
- `canProjectileHit`은 owner/dead를 공통 제외하고 selected mode rule로 ally/enemy를 판정해요.
- ally/dead처럼 제외된 overlap은 projectile을 destroy하지 않아요.
- `projectileTargetIndex`의 tie-break는 사용자 결정값을 적용해요. 결정 전에는 production code를 수정하지 않아요.
- `orderedInputsByPlayerID`는 caller slice를 복사하고 stable sort해요.
- reversed input 두 상태를 실제 projectile 접촉 tick까지 진행해 HP, projectile ID/order/destroy가 같은지 검증해요.

GREEN:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -run 'TestStep(ProjectileCollisionMatrix|ProjectileChoosesLowestPlayerIDWhenTargetsOverlap|NormalizesInputOrderThroughCollision)|TestValidateGameConfigRejectsUnsupportedTeamBehavior' -count=1
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod mise exec -- go test ./internal/simulation -count=1
```

## Task 3: AsyncAPI와 사람용 문서 동기화

수정 또는 검토 파일:

- `api/asyncapi.yaml`
- `docs-ui/scripts/validate.mjs`
- `ai-docs/api-docs.md`
- `ai-docs/api-reference.md`
- `ai-docs/protocol.md`
- `ai-docs/architecture.md`
- `ai-docs/project-map.md`
- `ai-docs/decisions.md`의 `ADR-0030`
- unchanged 확인: `api/openapi.yaml`

문서 계약:

- Solo/Team/Duel별 eligibility와 ally pass-through를 설명해요.
- multi-contact target은 사용자에게 승인받은 tie-break를, input은 PlayerID 오름차순을 사용한다고 기록해요.
- death snapshot 이후의 elimination/GameEnd는 SL-89 범위임을 명시해요.
- generated `internal/docs/api/asyncapi.yaml`은 stage하지 않고 source와 `cmp`만 해요.

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
