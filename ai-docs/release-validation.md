# 최종 검증 실행 방법

## 자동 검증

`make ci`는 source marker → 공식 OpenAPI/AsyncAPI schema → docs embed build 순서를 검증하고 Go vet/test/build 및 배포 회귀를 실행해요. 동시성 검사는 `go test -race ./... -count=1`로 별도 실행해요.

## 가속 용량 검사

```sh
CRAWLSTARS_CAPACITY_PROBE=1 go test ./internal/rooms -run TestCloseoutFiveRoomBotCapacityProbe -count=1 -v
```

운영과 분리한 로컬 테스트예요. 최대 5개 방을 동시에 registry에 두고 각 방은 human 1명 + bot 5명의 Team mode로 구성해요. 실제 production config, bot 판단, simulation, JSON 준비, 결과/방 cleanup을 사용해요. 방별 tick은 한 goroutine에서 순서대로 호출해요. 10 wave 뒤 방 registry가 비었는지 검증하고 처리시간 mean/p95/p99/max를 기록해요.

이 검사는 30Hz 실시간 스케줄러, 실제 소켓 전송, 네트워크 혼잡, Unity 렌더링을 포함하지 않아요. 1800 tick 안에 끝나지 않은 방은 종료 완료 수에 넣지 않고 명시적으로 정리해요. 50경기 완료 수와 각 실행 환경을 함께 기록해야 해요. 시간 임계값을 CI에 고정하지 않아요.

2026-09-05 Apple M5 Pro, Go 1.25.0 실행에서 50/50경기 종료, 5916 room tick, 평균 24.379µs, p95 42.458µs, p99 107.208µs였어요. 이 값은 운영 보장이나 실제 입력 지연으로 해석하지 않아요.

운영 `crawlstars_tick_duration_seconds`는 simulation Step만 측정해요. 봇 판단과 직렬화는 제외되므로 위 가속 검사와 측정 경계가 달라요.

## 실제 인수

같은 서버·클라이언트 revision의 빌드로 세 캐릭터·세 모드, 팀전 개인 사망 후 관전/팀 전멸, 공격·스킬 거절과 UI, 10회 재매칭, 실제 6인, 30분 플레이를 별도로 확인해요. 자동 테스트나 소스 검토를 실플레이 Pass로 바꾸지 않아요.
