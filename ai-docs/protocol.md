# 프로토콜

현재 protocol surface는 E2 client-server integration을 위한 development 계약입니다. Production gameplay protocol은 아직 아닙니다.

## 현재 구현

- simple matchmaking join
- room/player WebSocket
- server-authoritative snapshot stream
- static map movement/collision
- projectile movement/destroy
- projectile hit, HP, death snapshot
- GameEnd Win/Lose/Draw event와 종료 room 정리
- matchmaking Ready event/ready ACK/countdown/start
- start 전 match cancel
- client build용 shared game config artifact

아직 구현하지 않은 것:

- start 후 disconnect policy
- respawn, score
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

Config artifact는 client 공유용과 server runtime용을 분리합니다.

`client-config/game-config.json`은 Unity client가 build 때 sparse checkout해서 runtime asset 경로로 복사하는 공유 config입니다.

- `tileSize`
- `playerRadius`
- `playerTypes`
- `projectileRadius`
- `projectileTypes`

`server-config/game-config.json`은 server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only config입니다.

- `tickRate`
- `tile.size`
- player type별 `id/radius/hp/speed`
- projectile type별 `id/radius/damage/speed`
- `map`

Client는 여전히 최종 gameplay state를 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map은 server snapshot이나 Ready event로 받거나 서버만 판단하므로 client 공유 config에 넣지 않습니다.

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
    "status": "started",
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

Matchmaking room은 두 matched player가 모두 WebSocket에 연결되면 먼저 `Ready` event를 보냅니다. 이 event는 client가 map을 렌더하고 player spawn을 배치하는 기준 데이터입니다.

Ready event:

```json
{
  "Type": "Ready",
  "Map": {
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
  "Players": [
    {
      "Id": "player-1",
      "Team": "red",
      "Slot": 0,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    }
  ]
}
```

Ready ACK:

```json
{
  "Type": "ready"
}
```

Starting signal:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "starting",
    "countdown": 5,
    "Tick": 0,
    "Players": null,
    "Projectiles": null
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

GameEnd event:

```json
{
  "Type": "GameEnd",
  "PlayerId": "player-1",
  "Result": "Win"
}
```

HP가 0인 player가 생기면 server는 같은 tick의 마지막 snapshot을 먼저 보낸 뒤 player별 `GameEnd` event를 보냅니다. 한 명만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`입니다. 같은 tick에 양쪽 player가 동시에 사망하면 양쪽 모두 `Draw`입니다. Server는 `GameEnd` 전송 후 room-local ticker와 WebSocket connection을 정리합니다.

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

두 번째 player가 같은 waiting room에 들어와도 REST response shape는 유지되고 `room.status`는 `waiting`입니다. 해당 room은 matchmaking match로 잠겨 late join 대상에서 빠집니다.

두 client가 WebSocket에 연결하면 `Type: "Ready"` event를 받습니다. Ready event에는 JSON number array 형태의 `Map.map`과 `Players[].SpawnPosition`이 들어갑니다. 두 client가 모두 `{"Type":"ready"}`를 보내면 server는 `Snapshot.status: "starting"`과 `Snapshot.countdown: 5`를 1번 broadcast합니다. Client는 이 신호를 기준으로 fake timer를 표시하고, server는 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 보낸 다음 simulation ticker를 시작합니다.

첫 번째 player만 연결된 상태에서는 room이 `waiting`이라 WebSocket input은 저장되지만 gameplay snapshot은 오지 않습니다. 1명으로 테스트하려면 debug API `POST /rooms/{roomID}/start`를 호출해야 합니다.

Room response와 Ready event의 `map`은 서버 simulation이 collision에 쓰는 tile grid입니다. `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. 기본 map source는 `server-config/game-config.json`의 `map`입니다. 서버가 이 config 로드나 검증에 실패하면 `StaticGameConfig()`와 `StaticMapFixture()`의 fallback을 사용합니다.

`SL-58`에서는 이 흐름을 `POST /matchmaking/join` response shape를 유지한 채 WebSocket state message로 추가합니다. REST polling이나 SSE를 먼저 늘리지 않습니다.

## Room cleanup

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected TTL cleanup을 막습니다.

현재 WebSocket close는 connection과 pending input을 제거합니다. matchmaking start 전 close는 match cancel로 처리해 room과 남은 connection을 정리합니다. started room에서 GameEnd가 발생하면 결과 event를 보낸 뒤 room과 연결을 정리합니다.

디버그 테스트 중 즉시 비워야 하면 REST debug API를 사용합니다.

```text
DELETE /rooms
DELETE /rooms/{roomID}
```

삭제는 in-memory room, room-local ticker, WebSocket connection을 정리합니다. Persistence는 아직 없으므로 DB 삭제는 없습니다.

## 2인 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 `webSocketPath`로 연결합니다.
3. 두 connection이 같은 `Type: "Ready"` event를 받고, 이 event의 `Map.map` row가 숫자 배열이어야 합니다.
4. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
5. 한 player가 movement input을 보내면 두 connection이 같은 `Snapshot.Tick`과 player `Pos`를 받아야 합니다.
6. Red와 blue spawn은 Ready event의 `Players[].SpawnPosition`으로 확인합니다.
7. Hit tick에서 projectile은 `IsDestroyed: true`, target은 HP 감소로 보여야 합니다.
8. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
9. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
10. `GameEnd` 이후 해당 room은 정리되어야 합니다.
11. invalid JSON 이후에도 다음 snapshot stream은 유지되어야 합니다.

자동 검증은 `go test ./internal/rooms`와 `go test ./internal/simulation`이 담당합니다.

## 문서 위치

- REST: `api/openapi.yaml`
- WebSocket: `api/asyncapi.yaml`
- 사람이 읽는 API: `ai-docs/api-reference.md`
- 문서화 기준: `ai-docs/api-docs.md`

후속 protocol message는 Linear issue에서 scope와 acceptance criteria를 먼저 정한 뒤 구현합니다.
