# Server TODO

## Current Phase

Server E1 core loop skeleton is merged. Current work is E2 client-server integration support.

Read `ai-docs/project-map.md` first when you need the full current-state map.

## Completed Bootstrap

- [x] Go module 초기화
- [x] 최소 server entrypoint 추가
- [x] health package와 test 추가
- [x] Makefile validation loop 추가
- [x] GitHub Actions CI 추가
- [x] GitHub Actions CD package workflow 추가
- [x] Oracle VM pull deployment script 추가
- [x] workflow 및 AI docs 추가
- [x] Linear issue control model 문서화

## Follow-Ups

- [x] 초기 baseline을 GitHub에 push
- [x] 첫 push 이후 default branch 확인
- [x] branch protection 구성
- [x] 첫 PR 이후 Linear GitHub integration 확인
- [x] 첫 CD workflow와 VM deployment smoke check 실행
- [x] PR review settings 확인
- [x] Cloudflare Tunnel로 VM runtime 노출
- [x] public `/health` 및 root domain response 확인

## Completed E1/E2 Server Work

- [x] `SL-38`: `Step(inputs) -> Snapshot` simulation contract
- [x] `SL-39`: static map movement and wall collision
- [x] `SL-40`: attack/projectile skeleton
- [x] `SL-41`: room REST debug lifecycle
- [x] `SL-42`: room WebSocket snapshot broadcast
- [x] `SL-43`: room TTL cleanup and invalid input regression
- [x] `SL-47`: server-hosted OpenAPI/AsyncAPI docs
- [x] `SL-49`: simple `/matchmaking/join`
- [x] `SL-51`: docs tooling cleanup
- [x] `SL-52`: Swagger deployed-origin fix
- [x] `SL-53`: projectile movement and wall collision
- [x] `SL-54`: hit, HP, death snapshot
- [x] `SL-55`: 2-player WebSocket sync regression
- [x] `SL-56`: protocol validation docs

## Next Ticket Candidates

### 1. Recommended: `SL-58`

Title:

```text
E2-2-3 [Server] 매칭 준비/카운트다운 상태 전이
```

Suggested scope:

- [ ] Keep `POST /matchmaking/join` response shape stable.
- [ ] Add WebSocket match state messages for matched/loading/starting/started.
- [ ] Accept client ready or loading-complete input before simulation starts.
- [ ] Start countdown only when all matched clients are ready.
- [ ] Start simulation after countdown.
- [ ] Treat WebSocket close before simulation start as match cancel/removal.

Out of scope:

- post-start disconnect policy
- bot replacement
- ping/pong timeout
- respawn, score, win/loss
- production matchmaking queue
- persistence

Validation:

- [ ] Unit tests for waiting -> matched -> ready -> countdown -> started state transition.
- [ ] WebSocket tests for both clients receiving the same match state messages.
- [ ] WebSocket test for pre-start close removing the waiting player.
- [ ] `make ci`

### 2. `SL-30`: Shared constants/data management

Suggested v1 scope:

- [ ] Define one shared game config artifact for current server behavior.
- [ ] Include tick rate, tile size, player defaults, projectile defaults, max players, and static map fixture.
- [ ] Add validation that Go constants/defaults and the artifact do not drift.
- [ ] Document field names and units for Unity.

Out of scope:

- hot reload
- editor tooling
- 10-player cap expansion
- runtime map selection

### 3. `SL-14` closeout

Use after `SL-57` client PR is merged or explicitly accepted.

- [ ] Confirm server sub-issues `SL-53` through `SL-56` are done.
- [ ] Confirm client issue `SL-57` state.
- [ ] Decide snapshot field cleanup separately if still desired.
- [ ] Move parent issue only when server/client acceptance criteria are both satisfied.

## Protocol Cleanup Candidates

- [ ] Decide whether snapshot `PressedAttack` remains as debug echo or moves to a smaller input-only contract.
- [ ] Keep input `PressedAttack` separate from `AttackDir` unless a new firing model is explicitly accepted.
- [ ] Keep snapshot `IsDead` unless a future state model replaces it with a richer status enum.
