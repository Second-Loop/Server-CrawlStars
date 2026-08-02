# SL-99 Server Client Config Contract 구현 계획

> **범위:** Server-CrawlStars PR만 다뤄요. Client-CrawlStars 코드 기여는 포함하지 않아요.

## 1. 목표

Server가 client config v3의 canonical artifact와 strict Go validator를 제공해요.

- 클라이언트 개발자가 승인한 field와 값을 `client-config/game-config.json`에 반영해요.
- 필수 field, exact version, 값 범위와 stable character type을 검증해요.
- client 입력 보조값과 server-authoritative gameplay 값을 분리해요.
- REST/WebSocket 계약과 server simulation 동작은 바꾸지 않아요.

## 2. 고정 계약

- Version: `3`
- Character type: `0=Shelly`, `1=Colt`, `2=Lily`
- `normalAttackDistance`: `5 / 1.5 / 6`
- `skillAttackDistance`: `1 / 3 / 7`
- `skillAttackCoolDown`: `10 / 10 / 10`
- `maxBullets`: `3 / 3 / 4`
- `normalAttackCoolDown=1`
- `tileSize=1.2`, `playerRadius=0.5`, `projectileRadius=0.3`

Unknown JSON field는 additive 호환성을 위해 허용해요. Version, 필수 field, exact character type set, 양수 값은 엄격하게 검증해요.

`skillAttackCoolDown=10`과 `maxBullets`는 Client cooldown UI·로컬 bot 입력 보조값이에요. 실제 attack range, charge, hit/damage와 skill 승인은 `server-config`와 simulation/snapshot이 소유해요.

## 3. Task 1: v3 artifact와 Go validator

대상 파일:

- `client-config/game-config.json`
- `client-config/game_config.go`
- `client-config/game_config_test.go`
- `internal/simulation/game_config.go`
- `internal/simulation/game_config_test.go`

구현:

1. `clientconfig.Version = 3`과 typed `Config`, `CharacterConfig`를 정의해요.
2. Pointer wire DTO로 missing/null을 구분해요.
3. 공용 값과 캐릭터별 값이 finite positive인지 확인해요.
4. 캐릭터가 정확히 세 개이고 type `0/1/2`가 중복 없이 존재하는지 확인해요.
5. 기존 simulation drift test는 client metadata가 아니라 stable numeric type set과 collision 공유값만 비교해요.

검증:

```bash
rtk go test ./client-config ./internal/simulation -count=1
rtk git diff --check
```

## 4. Task 2: 소유권 문서와 drift guard

대상 파일:

- `docs-ui/scripts/validate.mjs`
- `ai-docs/architecture.md`
- `ai-docs/protocol.md`
- `ai-docs/api-reference.md`
- `ai-docs/project-map.md`
- `ai-docs/decisions.md`

구현:

1. 문서에서 client config v3의 field, 값과 단위를 설명해요.
2. Client UI·로컬 bot 값과 server-authoritative gameplay truth를 분리해요.
3. 문서 validator가 v3 schema·값·ownership marker를 확인하게 해요.
4. Client 소비자는 exact version과 필수 field를 build/runtime에서 검증해야 한다는 계약을 남겨요.
5. Client 저장소 코드나 PR은 Server PR 범위에 포함하지 않아요.

검증:

```bash
rtk node docs-ui/scripts/validate.mjs
rtk make docs-build
rtk git diff --exit-code origin/main -- api/openapi.yaml api/asyncapi.yaml
rtk git diff --check
```

## 5. Task 3: Server 최종 검증과 전달

```bash
rtk make ci
rtk git diff --check origin/main...HEAD
rtk git diff --exit-code origin/main -- api/openapi.yaml api/asyncapi.yaml
rtk git status --short
```

완료 조건:

- v3 artifact exact schema와 값 test가 통과해요.
- old version, missing/null, invalid numeric, duplicate/unsupported type이 실패해요.
- Server simulation과 public REST/WebSocket schema는 바뀌지 않아요.
- Client 설정값이 server gameplay truth를 덮지 않는다고 문서화해요.
- Server 브랜치와 검증 결과를 SL-99와 PR에 남겨요.

Client 저장소 소유자는 필요할 때 이 artifact를 소비해요. Unity parser·build preprocessor 구현과 EditMode 검증은 Server PR 완료 조건이 아니에요.
