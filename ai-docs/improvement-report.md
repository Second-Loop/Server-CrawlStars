# 개선 보고서 (2026-07-11)

코드베이스 분석 결과를 Linear 티켓 발행용으로 정리한 문서입니다.
각 항목은 독립 이슈 1개 기준이며, 제목/문제/권장/수용 기준을 그대로 티켓에 옮길 수 있습니다.
분석 기준 커밋: `78c73bf`.

## 우선순위 요약

| 순위 | 항목 | 분류 |
|---|---|---|
| P0-1 | input 검증 부재로 speed hack 가능 | 치트 방지 |
| P0-2 | WebSocket 인증 부재 + 예측 가능한 player ID | 보안 |
| P1-1 | debug API 무인증 노출 | 보안 |
| P1-2 | Store 전역 단일 mutex 병목 | 성능 |
| P1-3 | 스냅샷 브로드캐스트 순차/블로킹 구조 | 성능 |
| P2-1 | graceful shutdown 부재 | 운영 |
| P2-2 | 구조화 로깅/메트릭 부재 | 운영 |
| P2-3 | rooms.go 코드 정리 | 코드 품질 |
| P3-1 | 배포/기타 하드닝 묶음 | 보안/운영 |

---

## P0-1. input 검증: MoveDir/AttackDir 정규화 + attack budget

- 상태: SL-81 Stack 1에서 구현됨

- 문제:
  - `internal/simulation/simulation.go`의 `applyInput`은 NaN/Inf만 거르고 벡터 크기를 검증하지 않는다.
  - `MoveDir=(100,0)`을 보내면 100배속 이동이 되고, 최종 위치만 벽 충돌 검사하므로 얇은 벽 통과도 가능하다.
  - `AttackDir`도 정규화 없이 projectile `Dir`로 쓰여 투사체 속도 조작이 가능하다.
  - 발사 쿨다운이 없어 `PressedAttack`을 매 tick 보내면 초당 30발 발사된다.
  - `IsDead` player의 input을 무시하지 않는다. 지금은 사망 즉시 GameEnd라 실해가 없지만 respawn 도입 시 버그가 된다.
- 권장:
  - 서버에서 `MoveDir`/`AttackDir` 크기를 1로 clamp/정규화한다.
  - server game config에 player별 최대 attack charge와 recharge tick을 추가하고 `Step`에서 강제한다.
  - `IsDead` player의 input을 무시한다.
- 수용 기준:
  - 크기 > 1 벡터 input이 정규화된 값과 같은 이동/발사 결과를 내는 회귀 테스트.
  - 기본 4 charge 소진, 30 tick 회복, player별 독립 budget 회귀 테스트.
  - dead player input 무시 테스트.
- 반영 결과:
  - `MoveDir`은 크기 `1` 이하를 보존하고 큰 값만 clamp하며, non-zero `AttackDir`은 unit vector로 정규화한다.
  - attack charge는 `simulation.State` 내부에만 두고 기본 4 charge, 30 tick recharge를 적용한다.
  - public snapshot schema와 `client-config/game-config.json`은 변경하지 않았다.

## P0-2. player 세션 토큰 도입 (WebSocket 인증)

- 문제:
  - player ID가 `player-1`, `player-2`로 순차 발급되고 WebSocket 연결(`/rooms/{roomID}/players/{playerID}`)에 인증이 없다.
  - 상대보다 먼저 해당 playerID로 연결하면 타인 캐릭터를 조종할 수 있다.
- 권장:
  - `POST /matchmaking/join` 응답에 `crypto/rand` 기반 세션 토큰을 추가한다.
  - WebSocket 연결 시 토큰을 검증한다(쿼리 파라미터 또는 첫 메시지).
  - room/player ID 자체도 예측 불가능한 값으로 바꾸는 것을 검토한다.
- 수용 기준:
  - 토큰 없이/잘못된 토큰으로 연결 시 거부되는 테스트.
  - openapi.yaml, asyncapi.yaml, api-reference.md 갱신.

## P1-1. debug API 인증 게이트

- 문제:
  - `DELETE /rooms`(전체 삭제), `DELETE /rooms/{id}`, `POST /rooms/{id}/start`, `POST /rooms/{id}/players`가 무인증으로 프로덕션 바이너리에 포함된다.
  - 외부 노출 시 누구나 진행 중인 모든 게임을 삭제할 수 있다.
- 권장:
  - `DEBUG_API_TOKEN` 헤더 인증 또는 `ENABLE_DEBUG_API=true`일 때만 라우트 등록.
- 수용 기준:
  - 게이트 꺼진 상태에서 debug 라우트가 404/401을 반환하는 테스트.

## P1-2. Store 전역 락 분리 + cleanup janitor 분리

- 문제:
  - `Store.mu` 하나가 모든 room의 30Hz tick, HTTP 요청, WebSocket input을 직렬화한다. `state.Step`도 락 안에서 실행되어 room들이 순차로 시뮬레이션된다.
  - `tickRoom`이 매 tick `cleanupExpired()`를 호출해 30Hz × room 수만큼 전체 room map을 순회한다.
- 권장:
  - Store 락은 room 목록 관리만 담당하고, tick/input/clients는 room별 락으로 분리한다.
  - cleanup은 30초 주기 janitor goroutine 1개로 옮긴다.
- 수용 기준:
  - `-race` 포함 기존 테스트 전부 통과.
  - tick 경로에서 store 전역 락을 잡지 않는 구조 확인.

## P1-3. 스냅샷 브로드캐스트 구조 개선

- 문제:
  - tick goroutine이 클라이언트별로 순차 write하며, write timeout이 10ms로 너무 짧아 정상 클라이언트도 스냅샷이 조용히 유실된다.
  - 느린 클라이언트가 여럿이면 순차 write 합계가 33ms tick 예산을 초과한다.
  - 같은 메시지를 delivery마다 다시 `json.Marshal`한다.
- 권장:
  - 클라이언트별 버퍼 채널 + 전용 writer goroutine 패턴(밀리면 오래된 스냅샷 drop).
  - marshal은 tick당 1회로 줄이고, write timeout을 현실적인 값으로 조정한다.
- 수용 기준:
  - 느린 클라이언트 1명이 다른 클라이언트의 스냅샷 수신을 지연시키지 않는 테스트.

## P2-1. graceful shutdown + HTTP 서버 하드닝

- 문제:
  - `cmd/server/main.go`가 `http.ListenAndServe`를 그대로 사용해 SIGTERM 시 모든 연결이 즉시 끊긴다(systemd `TimeoutStopSec=15s` 미활용).
  - `Store.Close()`가 구현돼 있으나 호출되는 곳이 없다.
  - `ReadHeaderTimeout`/`IdleTimeout`이 없어 slowloris에 취약하다.
- 권장:
  - `signal.NotifyContext` + `http.Server.Shutdown` + `store.Close()` 연결.
  - `http.Server`에 timeout 설정 추가.
- 수용 기준:
  - SIGTERM 시 진행 중 연결이 close 메시지와 함께 정리되는 테스트 또는 수동 검증 절차 문서화.

## P2-2. 구조화 로깅 + 메트릭

- 문제:
  - room 생성/시작/종료, WebSocket 연결/해제, 에러가 전혀 로그되지 않아 운영 중 관측이 불가능하다.
- 권장:
  - `log/slog` 구조화 로깅 도입(room lifecycle, 연결 이벤트, 에러).
  - `/metrics` Prometheus 엔드포인트(active rooms, connected clients, tick duration) 추가.
- 수용 기준:
  - 주요 lifecycle 이벤트가 roomID/playerID 필드와 함께 로그된다.

## P2-3. rooms.go 코드 정리

- 상태: SL-81 Stack 2에서 구현됨

- 분석 당시 문제:
  - `internal/rooms/rooms.go` 1,250줄에 HTTP 라우팅, matchmaking, WebSocket, room lifecycle, 직렬화가 모두 들어있다.
  - 에러 분기가 문자열 비교(`err.Error() == "room full"`)로 되어 있다.
  - 라우팅이 `strings.Split` 수동 파싱이다(Go 1.22+ `ServeMux` 패턴 사용 가능, go.mod는 1.25).
  - 손수 만든 `itoa`(→ `strconv.Itoa`), 미사용 `snapshotMessage` 타입이 있다.
- 권장:
  - 동작 변경 없이 handler/store/websocket/messages 파일 분리.
  - sentinel error + `errors.Is`, `ServeMux` 패턴 라우팅 전환.
- 수용 기준:
  - 기존 테스트 전부 통과, API 응답 변경 없음.
- 반영 결과:
  - `rooms.go` 책임을 `handler.go`, `store.go`, `websocket.go`, `messages.go`, `cleanup.go`로 나누고 production 파일을 500줄 이하로 정리했다.
  - room lifecycle 오류를 sentinel error와 `errors.Is`로 전환하고 custom `itoa`와 production test decoder를 제거했다.
  - Go `ServeMux` method pattern과 `PathValue`를 사용하며, explicit JSON HEAD/405/404 fallback과 canonical path preflight로 기존 wire response를 유지한다.
  - REST/WebSocket schema와 성공·오류 응답 계약은 변경하지 않았다.

## P3-1. 배포/기타 하드닝 묶음

- 문제와 권장 (개별 소형 이슈로 쪼개도 됨):
  - `scripts/deploy/pull-latest.sh`가 `SHA256SUMS`를 다운로드 후 검증하지 않는다 → `sha256sum -c` 추가.
  - `/matchmaking/join` 스팸 6회로 room cap이 차서 매치메이킹이 마비된다 → IP 기반 rate limit.
  - WebSocket ping/pong이 없어 유령 연결이 `len(clients) > 0` 조건으로 room TTL cleanup을 막을 수 있다 → heartbeat 도입.
- 수용 기준:
  - 각 항목별 회귀 테스트 또는 배포 검증 절차.

---

## 참고: 현 단계에서 하지 않아도 되는 것

- in-memory 단일 프로세스 구조 자체는 E2 스코프에 적절하다. 수평 확장은 이슈로만 남긴다.
- 스냅샷 델타 압축, 벽 충돌 O(1) 타일 인덱싱은 맵/인원 규모가 커질 때 처리한다.
