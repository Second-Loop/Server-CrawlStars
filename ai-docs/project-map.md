# 프로젝트 맵

이 문서는 `Server-CrawlStars`를 다시 볼 때 가장 먼저 읽는 지도입니다. 자세한 계약은 `api/`, `ai-docs/protocol.md`, `ai-docs/api-reference.md`를 따릅니다.

## 한 줄 요약

클라이언트는 `POST /matchmaking/join`으로 `room`, `player`, `webSocketPath`를 받고, WebSocket으로 input을 보냅니다. 서버는 room마다 30Hz tick에서 `State.Step(inputs) -> Snapshot`을 실행하고 같은 snapshot을 연결된 client에게 broadcast합니다.

## 현재 상태

되는 것:

- health check와 server-hosted API docs
- 단순 matchmaking join
- room/player debug REST API
- room/player WebSocket 연결
- movement, wall collision
- projectile 생성, 이동, wall/boundary destroy
- projectile hit, HP 감소, `IsDead` snapshot
- 2-player WebSocket sync regression test

아직 안 되는 것:

- match ready/loading/countdown state event
- client ready ACK 후 simulation start
- start 전 WebSocket close cancel/removal
- start 후 disconnect 정책, ping/pong timeout, bot replacement
- respawn, score, win/loss
- production matchmaking queue, rating, auth, persistence
- client/server shared constants artifact

## 레포 구조

```text
cmd/server
  main.go                  HTTP server entrypoint

internal/health            /health

internal/docs              OpenAPI/AsyncAPI raw spec과 docs UI embed

internal/rooms
  REST room lifecycle
  simple matchmaking connector
  WebSocket connection 관리
  room-local 30Hz ticker
  pending input 수집과 snapshot broadcast
  in-memory TTL cleanup

internal/simulation
  transport를 모르는 gameplay core
  State.Step(inputs) -> Snapshot
  map, movement, projectile, hit, HP/death rule

api
  openapi.yaml
  asyncapi.yaml

docs-ui                   docs validation/build scripts
scripts/deploy            VM pull deployment scripts
ai-docs                   사람이 읽는 운영/설계 문서
```

## 요청 흐름

### 1. 서버 시작

`cmd/server/main.go`는 `SERVER_ADDR`가 없으면 `127.0.0.1:8080`에 bind합니다. 하나의 `rooms.Store`를 만들고 `/health`, docs route, `/matchmaking/join`, `/rooms`, `/rooms/`를 mount합니다. active room cap은 5개입니다.

### 2. 매칭 요청

`POST /matchmaking/join`은 production queue가 아니라 단순 connector입니다.

1. 만료된 room을 정리합니다.
2. 여유 있는 waiting room을 찾습니다.
3. 없으면 새 waiting room을 만듭니다.
4. player를 발급합니다.
5. player 수가 2명이 되면 즉시 room을 start합니다.
6. `room`, `player`, `webSocketPath`를 반환합니다.

Player ID는 `player-1`, `player-2`처럼 증가합니다. join index가 짝수면 red, 홀수면 blue이고, `slot`은 `playerIndex / 2`입니다.

현재는 두 번째 player가 들어오면 바로 simulation이 시작됩니다. client loading/ready ACK를 기다리지 않습니다.

### 3. WebSocket 연결

Client는 다음 path에 연결합니다.

```text
WS /rooms/{roomID}/players/{playerID}
```

서버는 upgrade 전에 room 존재 여부, player 소속 여부, 같은 player의 중복 연결 여부를 확인합니다. Waiting room도 연결과 input 수신은 허용하지만, started 전에는 gameplay snapshot을 보내지 않습니다.

### 4. Room start

Room은 두 경로로 시작합니다.

- debug: `POST /rooms/{roomID}/start`
- matchmaking: 두 번째 player가 `POST /matchmaking/join`

Start 시점에는 `simulation.NewStateWithConfig`로 state를 만들고, `simulation.TickRate = 30` 기준 ticker를 시작합니다. 이 loop는 room-local gameplay loop이지 범용 scheduler나 runner가 아닙니다.

### 5. Input 수집

WebSocket input:

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": true
}
```

한 tick 안에 같은 player가 여러 input을 보내면 마지막 input만 사용합니다. 잘못된 JSON은 `invalid_input` error message를 보내고 snapshot stream은 유지합니다.

### 6. Tick 처리

Started room의 tick 흐름:

1. pending input을 복사합니다.
2. pending input map을 비웁니다.
3. `room.state.Step(inputs)`를 호출합니다.
4. `{"Type":"snapshot","Snapshot":...}` 형태로 감쌉니다.
5. 연결된 client 모두에게 같은 snapshot을 보냅니다.

`internal/simulation.State.Step` 순서:

1. 기존 projectile 이동
2. projectile wall/boundary destroy와 hit 처리
3. player input 적용
4. movement는 X축, Y축 순서로 wall collision 검사
5. `PressedAttack = true`이고 `AttackDir`가 zero가 아니면 projectile 생성
6. tick 증가
7. snapshot clone 반환

새 projectile은 생성된 tick에는 owner 위치에 보이고 다음 tick부터 이동합니다.

### 7. Snapshot 필드 의미

`AttackDir`와 `PressedAttack`은 분리합니다.

- `AttackDir`: 현재 조준 방향
- `PressedAttack`: 이번 tick의 발사 trigger

`AttackDir != zero`만으로 발사를 추론하면 조준 방향을 유지하는 동안 매 tick 발사될 수 있습니다. 그래서 input의 `PressedAttack`은 유지합니다.

`IsDead`는 `HP <= 0`에서 유도할 수 있지만 snapshot에 명시합니다. Client가 death rule을 재해석하지 않아도 되고, 나중에 respawn, down, invulnerable 같은 상태로 확장하기 쉽습니다.

Snapshot의 `PressedAttack`은 input echo/debug 성격이 강합니다. 제거하려면 WebSocket schema 변경이므로 별도 issue에서 다룹니다.

### 8. Cleanup

Room store는 in-memory입니다.

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.

현재 WebSocket close는 client connection과 pending input만 제거합니다. started room에서 모든 client가 나가면 disconnected TTL을 시작합니다. start 전 waiting player 제거와 match cancel은 아직 없습니다.

## Linear 흐름

완료된 큰 흐름:

- `SL-38`: simulation `Step(inputs) -> Snapshot`
- `SL-39`: map, movement, wall collision
- `SL-40`: attack/projectile skeleton
- `SL-41`: room REST debug lifecycle
- `SL-42`: WebSocket snapshot broadcast
- `SL-43`: room TTL cleanup과 invalid input regression
- `SL-47`, `SL-51`, `SL-52`: API docs hosting/build
- `SL-49`: simple `/matchmaking/join`
- `SL-53`: projectile movement와 wall collision
- `SL-54`: hit, HP, death snapshot
- `SL-55`: 2-player WebSocket sync regression
- `SL-56`: protocol validation docs

현재 E2 흐름:

- `SL-12`: user matchmaking parent
  - `SL-49`: server simple matchmaking
  - `SL-50`: client matchmaking
  - `SL-58`: server ready/loading/countdown/cancel
- `SL-14`: client prototype logic server migration
  - server child issues `SL-53` to `SL-56`
  - `SL-57`: client logic split
- `SL-30`: shared constants/data management

각 issue의 최신 상태는 Linear를 확인합니다. 이 문서는 상태판이 아니라 흐름 복구용 지도입니다.

## 다음 추천 작업

1. `SL-58`: match start state transition
   - `POST /matchmaking/join` response shape 유지
   - WebSocket match state message 추가
   - client ready/loading-complete input 추가
   - 모두 ready면 countdown 후 simulation start
   - start 전 WebSocket close는 cancel/removal 처리

2. `SL-30`: shared constants/config v1
   - tick rate, tile size, player/projectile defaults, max players, map fixture를 한 artifact로 정리
   - Go 상수와 artifact drift 검증
   - Unity가 읽을 field/unit 문서화

3. `SL-14` closeout
   - `SL-57` client PR 상태 확인
   - server/client acceptance criteria가 모두 닫히면 parent issue 정리

## 자주 쓰는 명령

```sh
make docs-build
make ci
go test ./internal/simulation
go test ./internal/rooms
go run ./cmd/server
```

## 다음에 읽을 문서

- `ai-docs/workflow.md`: 작업 방식
- `ai-docs/architecture.md`: package와 runtime 책임
- `ai-docs/protocol.md`: protocol 경계
- `ai-docs/api-reference.md`: API shape
- `ai-docs/decisions.md`: 왜 이렇게 정했는지
