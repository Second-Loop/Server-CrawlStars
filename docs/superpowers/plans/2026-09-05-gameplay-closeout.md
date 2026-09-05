# SL-123 Gameplay Closeout Implementation Plan

> Required workflow: superpowers:subagent-driven-development. Each task has a fresh implementer, task review, then a final whole-branch review.

**Spec:** docs/superpowers/specs/2026-09-05-gameplay-closeout.md
**Goal:** 기존 입력·prestart 생명주기·스킬 구현의 남은 결함을 닫아요.
**Base:** 46b2f53, PR #76 위의 별도 브랜치 sl-123-gameplay-closeout.

## Global constraints
- 범위를 SL-123으로 제한하고 기존 팀 승패·봇 정책을 유지해요.
- 테스트 RED/GREEN, 변경 범위 독립 리뷰, 최종 HEAD CI/race를 구분해 남겨요.
- 쓰기 범위 밖 편집·main push·merge·배포·서브에이전트 생성은 worker가 수행하지 않아요.
- Go toolchain `/private/tmp/crawlstars-go126/go`, GOTOOLCHAIN=local; cache/GOPATH는 /private/tmp 아래예요. Default shell이 실패하면 /bin/zsh login:false를 사용해요.

### Task 1: tick 전 액션 보존

**Files:** modify internal/rooms/websocket.go, internal/rooms/skill_input_test.go; create internal/rooms/action_preservation_test.go; update api/asyncapi.yaml and ai-docs/api-reference.md, ai-docs/protocol.md, ai-docs/decisions.md for this rule only.
- stale/duplicate 체크 뒤 새 양수 movement-only 입력에 pending 양수 액션 flags/AttackDir를 합쳐요. 최신 MoveDir/ClientTick 유지, 최신 action 전체 우선, legacy0 전체 덮어쓰기 유지.
- 기존 inputSelectionFixture를 재사용해 공격→여러 이동, skill→이동, 액션 교체, stale/duplicate, 한 번 소비, 쿨타임 거절 후 재생 없음, legacy0 호환을 회귀 검증해요. 기존 winning command 테스트는 새 규칙으로 정확히 수정해요.
- RED/GREEN 로그를 저장하고 focused tests 및 rooms 전체 suite를 실행해요. commit 후 report를 작성해요.

### Task 2: Loading Ready deadline

**Files:** create internal/rooms/ready_deadline.go + ready_deadline_test.go; modify internal/rooms/store.go, websocket.go, cleanup.go; update api/asyncapi.yaml and related ai-docs lifecycle docs.
- 기존 matched attach deadline 패턴을 따라 별도 room-owned 30초 타이머를 만들어요. Loading 진입 시 생성, all-human Ready/Starting·delete·shutdown 시 resource collector로 취소해요.
- markClientReady는 deadline 절대 경계를 검사해 worker가 늦어도 expired Ready로 시작하지 않아요. store→room lock 순서를 유지하고 expiration과 ACK 경합은 한번만 정리해요.
- fake clock RED/GREEN: 직전 성공, 정확 경계 실패, 일부 미준비, human+bot quorum, 정상 취소/삭제/shutdown, 늦은 worker·stale session, 자격/ID/ticker/worker 정리. 기존 prestart close 의미 유지.
- focused + rooms suite, 해당 concurrency race 검증 후 commit/report.

### Task 3: timed dash, balance, wire contract

**Files:** internal/simulation/{simulation.go,skill.go,skill_dash.go,game_config.go} and relevant tests; new timed-dash state/helper/test files as needed; server-config/game-config.json; api/asyncapi.yaml; docs-ui/scripts/validate.mjs; affected ai-docs.
- config v7 + dashDurationTicks9 required; canonical validation/static fixture/json/negative tests 모두 일치시켜요.
- persistent dash state는 방향/step거리/남은tick을 보유해 입력 없이도 진행해요. 승인 tick 일반 이동을 먼저 적용하지 않도록 기존 phase를 최소 변경해요. 기존 공격 origin/순서 계약을 의도 없이 바꾸지 않아요.
- swept dash collision 알고리즘을 tick segment마다 재사용하고 충돌 여부를 받아 후속 대시를 중단해요. 입력순서/다중대시 결정성 유지. 사망/강제Eliminate/마지막step cleanup 검증.
- IsDashing public snapshot + AttackReadyTick 잠금; 새 필드는 AsyncAPI0.10 additive, rest OpenAPI unchanged 여부 확인. 문서와 validator examples 갱신.
- 정확한 변경 수치/10개 offset/18 boundary(activation1이면 ready18) 반영. 기존 순간 dash 전제 tests를 multi-tick 최종위치 검증으로 갱신하되 충돌·사망·콜트 회귀를 약화하지 않아요.
- TDD: 9step 위치/총거리/normalmove와 attack차단/방향고정/충돌중단/사망중단/ACK/마지막snapshot해제/기존캐릭터불변. full simulation 및 rooms 검증 후 commit/report.
