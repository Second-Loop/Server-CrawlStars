# SL-91 / SL-94 stacked PR 설계

## 1. 목적과 PR 구조

열린 PR `#48`의 SL-90 기본 bot participant 위에 다음 두 변경을 순서대로 쌓습니다.

```text
#48 sl-90-basic-bot
  -> SL-91 sl-91-bot-autofill
    -> SL-94 sl-94-client-tick-ack
```

- SL-91은 첫 human matchmaking join부터 10초 뒤 빈 participant slot을 bot으로 채웁니다.
- SL-94는 실제 simulation step에 반영된 client input tick을 player별 snapshot ACK로 돌려줍니다.
- 두 PR의 runtime·contract 변경은 각각 Linear issue 하나의 범위만 담고, 독립적으로 검토할 수 있게 유지합니다. 이 문서만 두 PR의 공통 planning artifact로 SL-91 기반 branch에 둡니다.
- SL-94는 기능상 SL-91과 독립적이지만 WebSocket, simulation, protocol 문서 변경이 겹치므로 SL-91 위에 쌓아 충돌을 한 번만 해결합니다.

## 2. 확정한 제품 결정

### SL-91의 10초 경계

Human join과 bot fill이 동시에 진행되면 서버의 matchmaking 직렬화 잠금을 먼저 얻은 transition이 우선합니다.

- Human join이 먼저면 해당 human을 기존 room에 넣은 뒤 남은 slot만 bot으로 채웁니다.
- Bot fill이 먼저면 정원을 bot으로 채워 room을 닫습니다. 늦은 human은 active-room cap에 여유가 있으면 새 waiting room으로 보내고, 여유가 없으면 기존 `409 room_cap_reached`를 반환합니다.
- 요청 시작 timestamp를 따로 저장하거나 보정하지 않습니다.

### SL-94의 비단조 ClientTick

- 양수 `ClientTick`이 이미 처리한 tick 이하이면 입력 전체를 조용히 무시합니다.
- 같은 player에게 더 큰 양수 pending tick이 있으면 그 이하의 새 양수 입력도 조용히 무시합니다.
- 필드가 없거나 `0`이면 legacy input으로 받아들이고 ACK는 올리지 않습니다.
- 음수 tick은 malformed input으로 보고 기존 `invalid_input` 경로로 응답합니다.
- ACK는 match마다 `0`으로 시작하며 감소하지 않습니다.
- 기존 last-write-wins는 단조 증가하는 정상 양수 입력과 legacy `0` 입력에 유지합니다. 역행·중복 양수 tick은 client 계약 밖의 stale input으로 분류합니다.

## 3. SL-91 설계

### 3.1 Timer 소유권과 생명주기

REST matchmaking join으로 waiting room의 human 수가 `0 -> 1`이 되는 시점에 room-owned one-shot ticker를 시작합니다. 새로 만든 room뿐 아니라 기존 empty waiting room에 첫 human이 합류하는 경우도 포함합니다. 후속 join이나 WebSocket attach는 deadline을 다시 설정하지 않습니다.

기존 clock/ticker 추상화를 사용해 fake clock으로 경계를 검증합니다. 중앙 scheduler나 실제 `time.AfterFunc`는 추가하지 않습니다. Room은 `botFillTicker`와 `botFillStop`을 한 쌍으로 소유하고, worker는 tick과 stop channel을 `select`합니다. `ticker.Stop()`은 channel을 닫지 않으므로 stop channel 없이 worker를 종료시키지 않습니다. Worker는 Store의 worker wait group에 등록해 Shutdown이 종료를 기다릴 수 있게 합니다.

Ticker는 다음 경우 반드시 중단하고 room resource에서 해제합니다.

- 10초가 지나 한 번 실행한 경우
- 10초 전에 human만으로 정원이 찬 경우
- room 삭제, clear, waiting TTL cleanup이 실행된 경우
- pre-start cancel 또는 Store Shutdown이 실행된 경우

모든 종료 경로는 room lock 아래에서 ticker와 stop channel의 소유권을 한 번만 detach한 뒤 모든 core lock을 풀고 `Stop`과 `close(stop)`을 실행합니다. Worker를 기다리는 동작도 core lock을 모두 푼 뒤에만 수행합니다. 이렇게 해서 이중 close와 timer worker의 `matchmakingMu` 대기 교착을 막고, Store가 worker 종료까지 확실히 기다릴 수 있게 합니다.

WebSocket upgrade나 attach가 완료되기 전 실패는 현재 재시도 동작을 유지하고 timer도 계속 진행합니다. 실제 attach가 끝난 human의 disconnect는 현재 `matchStatus` 경계를 그대로 따릅니다.

- 아직 unmatched라면 participant와 timer를 유지해 기존 credential 재접속을 허용합니다.
- matched/loading/starting이면 기존 pre-start match cancel로 room을 삭제하고 timer도 중단합니다.
- Timer fill과 disconnect가 맞물리면 room lock을 먼저 얻은 transition이 현재 상태를 결정합니다. Disconnect가 unmatched 상태를 먼저 관찰하면 timer가 이후 fill할 수 있고, timer가 matched 전이를 먼저 마치면 disconnect가 기존 cancel을 수행합니다.

Disconnect replacement bot이나 별도 reconnect grace는 추가하지 않습니다.

### 3.2 원자적 bot fill

Timer worker는 기존 mutation과 matchmaking 경계를 따라 아래 순서로 transition을 직렬화합니다.

```text
mutation gate
  -> matchmakingMu
    -> Store.mu
      -> room.mu
        -> registry pointer와 timer 소유권 재검증
        -> ticker/stop 소유권 원자적 detach
        -> 현재 남은 slot 재계산
        -> bot ID 전체 예약
        -> slot 순서대로 append
        -> matched transition
      -> Store.mu 해제
        -> room.mu만 유지하고 loading/Ready 전이
```

남은 수를 잠금 밖에서 계산한 뒤 기존 `addBots(count)`를 호출하지 않습니다. 계산과 append를 같은 critical section에서 수행해야 경계의 human join이 끼어들어 one-shot fill 전체가 실패하는 상황을 막을 수 있습니다.

Bot ID를 예약하기 전에 `s.rooms[roomID] == expectedRoom`, `room.botFillTicker == expectedTicker`, room이 제거되지 않은 waiting/unmatched 상태인지 확인합니다. 검증에 실패한 stale tick은 no-op으로 끝냅니다. 같은 ID의 replacement room이나 이미 삭제된 room 객체에는 절대 bot을 추가하지 않습니다.

Bot ID 생성은 append 전에 필요한 수만큼 모두 성공해야 합니다. 생성 실패 시 일부만 추가하지 않고 직접 structured logger로 `ERROR`, `event=bot_fill_failed`, 안전한 `room_id`와 오류를 정확히 한 번 기록한 뒤 waiting 상태를 유지합니다. 예상된 stale/full no-op은 실패 로그를 남기지 않습니다. 별도 retry worker는 만들지 않으며 이후 human join이나 기존 TTL cleanup이 room을 처리합니다.

### 3.3 배치와 Ready 전이

- Duel, Solo, Team capacity는 SL-87의 mode config를 그대로 사용합니다.
- Bot은 현재 player 수 다음의 config slot 순서대로 배치합니다.
- Team은 config의 red/blue slot 순서 덕분에 팀당 최대 3명을 넘지 않습니다.
- Bot은 session token, WebSocket, Ready ACK 대상이 아닙니다.
- SL-90의 human-only attach/Ready quorum과 shared `State.Step`을 그대로 사용합니다.
- Human ACK quorum이 만족되면 countdown과 gameplay loop를 각각 한 번만 시작합니다.
- Bot fill 이후 들어오는 human join은 해당 room을 건너뛰고 다른 waiting room을 선택하거나 만듭니다. Active-room cap 때문에 만들 수 없으면 기존 `409 room_cap_reached`를 유지합니다.

### 3.4 SL-91 검증

- Fake clock `9.999s`에는 bot이 없고 `10s`에는 mode 정원까지 정확히 한 번 채워지는지 확인합니다.
- 후속 human join이 deadline을 연장하지 않는지 확인합니다.
- 기존 empty waiting room에 첫 matchmaking human이 들어온 `0 -> 1` 전이에서도 timer가 정확히 한 번 시작되는지 확인합니다.
- Human full이 먼저인 경우 stale timer tick에도 bot이 추가되지 않는지 확인합니다.
- 실제 `matchmakingMu` 획득 직후를 제어하는 barrier test로 human-first와 timer-first 순서를 각각 고정해 중복, overflow, late join routing을 확인합니다.
- Room lock barrier로 unmatched-disconnect-first와 timer-matched-first 결과를 각각 고정해 기존 reconnect/cancel 경계를 확인합니다.
- Duel, Solo, Team에서 human 수 `1..capacity-1`을 모두 순회해 slot/team 배치를 확인합니다.
- `maxActiveRooms=1`인 timer-first late join이 기존 `409 room_cap_reached`를 반환하는지 확인합니다.
- Human-only Ready 전송, ACK quorum, countdown/start가 각각 한 번인지 확인합니다.
- Delete, clear, TTL cleanup, cancel, Shutdown에서 ticker와 worker가 남지 않는지 확인합니다.
- Human-full 취소와 동시에 worker가 matchmaking lock을 기다리는 경우에도 취소·Shutdown이 교착 없이 끝나는지 확인합니다.
- Bot ID 생성 실패와 중복 timer tick이 부분 append나 ID leak을 만들지 않는지 확인합니다.
- Stop 뒤 queue에 남은 tick과 같은 ID의 replacement room을 사용해 stale worker가 bot을 추가하거나 ID를 leak하지 않는지 확인합니다.
- Deadline이 다른 두 room의 timer가 서로의 identity나 stop signal에 영향을 주지 않는지 확인합니다.
- 실제 bot ID 생성 실패에는 `bot_fill_failed` 오류 로그가 한 번만 남고 stale/full no-op에는 남지 않는지 확인합니다.
- Targeted 반복·race test와 전체 `make ci`를 실행합니다.

## 4. SL-94 설계

### 4.1 Wire와 내부 모델

WebSocket input DTO와 simulation `InputCommand`에 `ClientTick int64`를 추가합니다. 필드는 optional이며 누락 시 Go zero value인 `0`을 사용합니다.

Snapshot의 `PlayerData`에는 required `LastProcessedClientTick int64`를 추가합니다. 최초 값과 bot 값은 `0`입니다. `Snapshot.Tick`의 server simulation tick 의미를 바꾸지 않습니다. Starting과 started lifecycle control snapshot은 계속 `Players: null`이고, 첫 gameplay snapshot부터 각 player ACK를 포함합니다.

### 4.2 Pending input 선택

Room mutex 아래에서 player별 pending input을 저장할 때 다음 규칙을 적용합니다.

1. 음수 tick은 저장하지 않고 `invalid_input`을 반환합니다.
2. `room.mu` 아래 snapshot cache인 `lastPlayers[playerID].LastProcessedClientTick`을 마지막 적용 ACK로 읽습니다. 별도 room ACK map은 만들지 않습니다.
3. 양수 tick이 마지막으로 적용된 ACK 이하이면 stale input으로 조용히 버립니다.
4. 양수 pending input이 이미 있으면 그 tick 이하의 새 양수 입력을 조용히 버립니다.
5. 더 큰 양수 tick은 기존 pending input을 덮어씁니다.
6. `0`인 legacy input은 기존 last-write-wins 동작을 유지합니다.

따라서 한 server tick 사이에 `10 -> 11 -> 12`가 들어오면 `12`만 Step에 전달됩니다. `12 -> 11`이나 `12 -> 12`는 뒤 입력을 버립니다.

### 4.3 ACK 적용

Simulation state가 ACK의 최종 소유자입니다.

- 존재하고 살아 있는 player의 유효한 input을 simulation이 처리하면 양수 `ClientTick`으로 ACK를 증가시킵니다.
- 충돌 때문에 이동하지 못했거나 공격 charge 부족·zero attack direction 때문에 눈에 보이는 효과가 없어도, 유효한 input을 소비했다면 ACK를 증가시킵니다.
- State에 직접 stale/duplicate 양수 command가 들어와도 input 전체를 적용하지 않습니다.
- `ClientTick == 0`인 legacy input은 movement/attack에 적용하되 기존 ACK를 보존합니다.
- Input이 없는 Step, unknown/dead player, non-finite direction input은 ACK를 바꾸지 않습니다.
- Match 안에서 reconnect한 client는 최신 snapshot ACK 다음 값부터 양수 tick을 이어갑니다. 새 match에서만 `0`으로 초기화합니다.
- Bot command는 `ClientTick == 0`을 유지하므로 bot ACK도 계속 `0`입니다.

이 경계는 ACK가 적용되지 않은 입력을 가리키거나 감소하는 일을 막습니다.

### 4.4 SL-94 검증

- `ClientTick` present/missing/negative JSON 입력을 확인합니다.
- 같은 server tick의 증가, 역행, 중복, legacy overwrite를 확인합니다.
- 음수 tick은 `invalid_input`을 한 번만 보내고 기존 pending input을 바꾸지 않는지 확인합니다.
- Stale/duplicate 양수 tick은 error나 control frame 없이 조용히 무시하는지 확인합니다.
- 누락/`0`인 legacy input은 정상 처리하면서 기존 ACK를 보존하는지 확인합니다.
- Pending 저장만으로 ACK가 바뀌지 않고 Step 이후 같은 snapshot에서 상태와 ACK가 함께 바뀌는지 확인합니다.
- 충돌·공격 charge 부족·zero attack direction처럼 유효하지만 상태 효과가 없는 input도 처리 ACK를 올리는지 확인합니다.
- Input 없는 Step에서 ACK 보존, player별 ACK 독립성, 새 match 초기값 `0`을 확인합니다.
- Unknown/dead/non-finite/stale input이 상태와 ACK를 바꾸지 않는지 확인합니다.
- Human command merge가 tick을 보존하고 bot command는 `0`인지 확인합니다.
- `Snapshot.Tick`과 starting/started lifecycle control의 `Players: null` 회귀가 없는지 확인합니다.
- `starting null -> started control null -> first gameplay players with ACK` 순서를 wire 수준에서 확인합니다.
- Started snapshot JSON은 값이 `0`이어도 server `Tick`과 모든 player의 `LastProcessedClientTick`을 생략하지 않는지 확인합니다.
- Targeted 반복·race test와 전체 `make ci`를 실행합니다.

## 5. 계약과 문서

SL-91에서는 REST matchmaking 동작과 WebSocket Ready 흐름 설명을 갱신합니다.

- `api/openapi.yaml`: join 후 10초 automatic fill 동작과 late join routing 설명
- `api/asyncapi.yaml`: bot-filled participant와 human-only Ready/ACK 설명
- 관련 `ai-docs/api-reference.md`, `api-docs.md`, `protocol.md`, `architecture.md`, `decisions.md`, `project-map.md`, `workflow.md`
- `docs-ui/scripts/validate.mjs`: SL-91 미구현 marker를 완료 계약으로 교체

SL-94에서는 AsyncAPI 계약 버전을 `0.5.0`으로 올리고 input/snapshot schema와 예시를 갱신합니다.

- `api/asyncapi.yaml`: optional `ClientTick`, required `LastProcessedClientTick`, human/bot 예시
- 관련 `ai-docs/api-reference.md`, `api-docs.md`, `protocol.md`, `architecture.md`, `decisions.md`, `project-map.md`
- `docs-ui/scripts/validate.mjs`: AsyncAPI `0.5.0`, input optional field, player required ACK marker 고정
- `api/openapi.yaml`에는 gameplay `PlayerData`가 없으므로 회귀 확인만 하고 불필요한 schema를 추가하지 않습니다.

## 6. 범위 밖

- Bot 난이도, pathfinding, randomness 변경
- Disconnect replacement bot, reconnect grace, Ready timeout
- Production queue timeout 또는 중앙 scheduler
- Client 구현과 UI
- Persistence, dashboard, runner, multi-agent orchestration
- 기존 `PressedAttack` pending overwrite 특성 수정

## 7. 완료 조건

각 issue는 다음 조건을 모두 만족할 때 ready-for-review PR로 올립니다.

- Linear acceptance criteria에 대응하는 자동화 테스트가 있습니다.
- 관련 race/repeat test와 `make ci`가 통과합니다.
- 공개 계약과 `ai-docs/`가 구현과 일치합니다.
- PR base/head가 의도한 stack 순서와 일치합니다.
- PR 본문과 Linear comment에 validation 결과를 짧게 남깁니다.
