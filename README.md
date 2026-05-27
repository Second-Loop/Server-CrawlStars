# Server Crawl Stars

Brawl Stars 스타일 실시간 멀티플레이어 게임을 위한 Go 서버 레포지토리입니다.

이 레포지토리는 부트스트랩 단계를 지나 E1 서버 권위 코어 루프를 준비하는 중입니다. 현재 목표는 게임플레이 로직, 매치메이킹, 물리, 영속 저장소를 한 번에 구현하는 것이 아니라, Linear 이슈, GitHub pull request, CI, Oracle VM pull 방식 CD 경로와 함께 반복 가능한 서버 개발/배포 루프를 유지하는 것입니다.

## 현재 범위

- Go module: `github.com/Second-Loop/Server-CrawlStars`
- `cmd/server`의 최소 HTTP 서버 entrypoint
- `internal/health`의 health package와 test
- format, vet, test, build를 실행하는 GitHub Actions CI
- linux/amd64 서버 release를 패키징하는 GitHub Actions CD
- systemd가 관리하는 release를 위한 VM pull 배포 script
- `AGENTS.md`의 얇은 agent entrypoint
- `ai-docs/`의 공유 workflow 문서

## 명령어

```sh
make fmt
make vet
make test
make build
make deploy-check
make ci
```

로컬에서 서버 실행:

```sh
go run ./cmd/server
```

Health check:

```sh
curl http://127.0.0.1:8080/health
```

서버는 기본적으로 `127.0.0.1:8080`에 bind합니다. 다른 host에서 접근 가능해야 하는 경우에만 의도적으로 `SERVER_ADDR=:8080 go run ./cmd/server`를 사용합니다.

배포 문서는 `ai-docs/deployment.md`에 있습니다. production systemd unit도 `SERVER_ADDR=127.0.0.1:8080`을 설정합니다. Cloudflare Tunnel은 `api-crawlstars.tolerblanc.com`을 Go 서버로, `tolerblanc.com`을 local-only Caddy hello page로 노출합니다.

## 작업 합의

작업 범위와 acceptance criteria의 source of truth는 Linear issue입니다. 구현 review는 GitHub branch와 pull request를 사용합니다. Agent는 `AGENTS.md`에서 시작해야 하며, 자세한 협업 규칙은 `ai-docs/workflow.md`에 있습니다.
