# 프로토콜

현재 protocol surface는 E2 client-server integration을 위한 development 계약입니다. Production gameplay protocol은 아직 아닙니다.

## 현재 구현

- simple matchmaking join
- room/player WebSocket
- server-authoritative snapshot stream
- static map movement/collision
- projectile movement/destroy
- projectile hit, HP, death snapshot

아직 구현하지 않은 것:

- matched/loading/starting/started state event
- client ready ACK와 countdown
- start 전 cancel/removal
- start 후 disconnect policy
- respawn, score, win/loss
- production matchmaking queue

## Simulation 계약

```text
internal/simulation.State.Step(inputs []InputCommand) Snapshot
```

이 계약은 transport와 분리되어 Go unit test에서 직접 검증합니다. REST, WebSocket, room lifecycle, matching queue는 simulation package 안으로 들어오지 않습니다.

`Step` 순서:

1. 기존 projectile 이동
2. projectile wall/boundary destroy와 player hit 처리
3. player input 적용
4. movement는 X축, Y축 순서로 collision 검사
5. `PressedAttack`과 `AttackDir`로 projectile 생성
6. tick 증가
7. snapshot 반환

현재 값:

- `TickRate = 30`
- `TileSize = 1.2`
- `DefaultPlayerSpeed = 2`
- `DefaultPlayerRadius = 0.5`
- `DefaultPlayerHP = 100`
- `DefaultProjectileSpeed = 13`
- `DefaultProjectileDamage = 10`
- `DefaultProjectileRadius = 0.3`
- `StaticMapFixture().MaxPlayers = 6`
- player spawn은 map의 `TileSpawnPoint(2)`를 join 순서대로 사용합니다.

공통 constants artifact는 아직 없고 `SL-30` 범위입니다.

## WebSocket 계약

```text
WS /rooms/{roomID}/players/{playerID}
```

연결 조건:

- room이 존재해야 합니다.
- player가 room에 속해야 합니다.
- 같은 room/player의 중복 연결은 거부합니다.
- waiting room도 연결과 input은 허용합니다.
- gameplay snapshot은 room이 started가 된 뒤에만 broadcast합니다.
- snapshot WebSocket write deadline은 10ms입니다.

Client input:

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

Server snapshot:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

Invalid input:

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Invalid input은 connection을 끊지 않습니다.

## Field 의미

`AttackDir`와 `PressedAttack`은 분리합니다.

- `AttackDir`: 현재 조준 방향
- `PressedAttack`: 이번 tick의 발사 trigger

`AttackDir != zero`로 공격을 추론하면 조준 유지 중 매 tick 발사될 수 있습니다. 그래서 input 계약에서는 `PressedAttack`을 유지합니다.

`IsDead`는 `HP <= 0`에서 유도할 수 있지만 snapshot에는 유지합니다. Client가 death rule을 재해석하지 않아도 되고, future state를 추가하기 쉽기 때문입니다.

Snapshot의 `PressedAttack`은 input echo/debug 성격이 강합니다. 제거는 별도 schema 변경 issue에서 다룹니다.

## Matchmaking 계약

현재:

```text
POST /matchmaking/join
```

응답:

```json
{
  "room": {
    "id": "room-1",
    "status": "waiting",
    "players": [
      {
        "id": "player-1",
        "team": "red",
        "slot": 0
      }
    ],
    "maxPlayers": 6,
    "map": {
      "width": 5,
      "height": 5,
      "index": 0,
      "maxPlayers": 6,
      "tileSize": 1.2,
      "map": [
        [1, 1, 1, 1, 1],
        [1, 0, 0, 0, 1],
        [1, 0, 1, 0, 1],
        [1, 0, 0, 0, 1],
        [1, 1, 1, 1, 1]
      ]
    },
    "latestSnapshot": {
      "tick": 0,
      "playerCount": 1,
      "projectileCount": 0
    }
  },
  "player": {
    "id": "player-1",
    "team": "red",
    "slot": 0
  },
  "webSocketPath": "/rooms/room-1/players/player-1"
}
```

두 번째 player가 같은 waiting room에 들어오면 simulation이 바로 start됩니다. Match complete event, client loading/ready ACK, countdown, start 전 cancel은 아직 없습니다.

첫 번째 player만 연결된 상태에서는 room이 `waiting`이라 WebSocket input은 저장되지만 gameplay snapshot은 오지 않습니다. 1명으로 테스트하려면 debug API `POST /rooms/{roomID}/start`를 호출해야 합니다.

Room response의 `map`은 서버 simulation이 collision에 쓰는 tile grid입니다. 기본 fixture 파일은 `internal/simulation/fixtures/default-map.json`이며, 서버 시작 시 이 JSON을 로드해 room store에 주입합니다. 로드나 검증에 실패하면 `StaticMapFixture()`의 5x5 map으로 fallback합니다. 실제 client map file 또는 shared constants artifact와 맞추는 작업은 `SL-30` 범위입니다.

`SL-58`에서는 이 흐름을 `POST /matchmaking/join` response shape를 유지한 채 WebSocket state message로 추가합니다. REST polling이나 SSE를 먼저 늘리지 않습니다.

## Room cleanup

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected TTL cleanup을 막습니다.

현재 WebSocket close는 connection과 pending input만 제거합니다. start 전 player 제거와 match cancel은 `SL-58` 범위입니다.

디버그 테스트 중 즉시 비워야 하면 REST debug API를 사용합니다.

```text
DELETE /rooms
DELETE /rooms/{roomID}
```

삭제는 in-memory room, room-local ticker, WebSocket connection을 정리합니다. Persistence는 아직 없으므로 DB 삭제는 없습니다.

## 2인 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 `webSocketPath`로 연결합니다.
3. 한 player가 movement input을 보내면 두 connection이 같은 `Snapshot.Tick`과 player `Pos`를 받아야 합니다.
4. Red는 map `(1, 1)`, blue는 `(3, 3)`에서 시작합니다. Center wall 때문에 hit 검증은 red를 blue와 같은 column으로 옮긴 뒤 아래로 공격합니다.
5. Hit tick에서 projectile은 `IsDestroyed: true`, target은 HP 감소로 보여야 합니다.
6. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
7. invalid JSON 이후에도 다음 snapshot stream은 유지되어야 합니다.

자동 검증은 `go test ./internal/rooms`와 `go test ./internal/simulation`이 담당합니다.

## 문서 위치

- REST: `api/openapi.yaml`
- WebSocket: `api/asyncapi.yaml`
- 사람이 읽는 API: `ai-docs/api-reference.md`
- 문서화 기준: `ai-docs/api-docs.md`

후속 protocol message는 Linear issue에서 scope와 acceptance criteria를 먼저 정한 뒤 구현합니다.
