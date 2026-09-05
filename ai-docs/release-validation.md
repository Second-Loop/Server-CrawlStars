# 최종 검증 실행 방법

## 자동 검증

`make ci`는 source marker → 공식 OpenAPI/AsyncAPI schema → docs embed build 순서를 검증하고 Go vet/test/build 및 배포 회귀를 실행해요. 동시성 검사는 `go test -race ./... -count=1`로 별도 실행해요.

연결 종료 검사는 방 registry 제거와 플레이어 ID 반환을 별도로 기다려요. `releaseClient`가 방을 먼저 제거하고 함수 종료 시 ID를 반환하므로, 방이 없다는 사실만으로 전체 정리가 끝났다고 판단하지 않아요. 각 완료 조건에는 제한 시간이 있어 실제 정리 누락은 실패해요.

## 가속 용량 검사

```sh
CRAWLSTARS_CAPACITY_PROBE=1 go test ./internal/rooms -run TestCloseoutFiveRoomBotCapacityProbe -count=1 -v
```

운영과 분리한 로컬 테스트예요. 서버와 같은 경로로 `server-config/game-config.json` embedded 파일을 읽고, 30Hz 설정·40x40 `Map_0`·세 캐릭터 설정을 store에 주입해요. fixture 기본값으로 돌아가지 않았는지도 검사해요. 최대 5개 방을 동시에 registry에 두고 각 방은 human 1명 + bot 5명의 Team mode로 구성해요. 매 wave에서 5개 방과 30 participant 구성을 확인하고 10 wave 뒤 방 registry가 비었는지 검증해요.

처리시간 표본은 방별 `tickRoomState` 호출이에요. bot 판단, simulation Step, snapshot 변환과 JSON 직렬화, 종료 판정은 포함하지만 matchmaking·bot fill·start 준비와 cleanup 대기 시간은 포함하지 않아요. 방별 tick은 한 goroutine에서 순서대로 호출해요. 30Hz 실시간 스케줄러, 실제 WebSocket 연결·소켓 쓰기, 네트워크 혼잡, Unity 렌더링도 포함하지 않으므로 실제 네트워크 부하 검사가 아니에요.

각 방은 최대 1800 tick만 가속 실행해요. 그 안에 끝나지 않은 방은 완료 수에서 제외한 뒤 명시적으로 제거하고, 종료된 방은 비동기 cleanup 완료를 기다려요. 50경기 중 제한 안에 끝난 수, 전체 room tick 수, mean/p95/p99/max와 실행 환경을 함께 기록해야 해요. 시간 임계값을 CI에 고정하지 않아요.

2026-09-05 Apple M5 Pro, Go 1.25.0 단일 실행에서 18/50경기가 1800 tick 안에 종료됐고, 나머지 32개도 제거한 뒤 registry가 비었어요. 79,233 room tick의 평균은 19.38µs, p95는 41.75µs, p99는 117.917µs, max는 417.375µs였어요. 이 한 번의 로컬 가속 결과는 운영 용량 보장이나 실제 입력 지연으로 해석하지 않아요.

운영 `crawlstars_tick_duration_seconds`는 simulation Step만 측정해요. 봇 판단과 직렬화는 제외되므로 위 가속 검사와 측정 경계가 달라요.

## 실제 인수

같은 서버·클라이언트 revision의 빌드로 세 캐릭터·세 모드, 팀전 개인 사망 후 관전/팀 전멸, 공격·스킬 거절과 UI, 10회 재매칭, 실제 6인, 30분 플레이를 별도로 확인해요. 자동 테스트나 소스 검토를 실플레이 Pass로 바꾸지 않아요.
