# Architecture

## 단계

이 레포지토리는 부트스트랩을 마치고 E1 서버 권위 코어 루프 준비 단계로 이동하고 있습니다. 서버 architecture는 아직 의도적으로 작게 유지합니다.

## 현재 구조

```text
cmd/server
  process entrypoint

internal/health
  health status model
  HTTP health handler
```

현재 서버는 로컬 및 CI 검증을 위한 최소 `/health` endpoint를 노출합니다. 아직 Unity client를 위한 gameplay, room, matchmaking, persistence, physics, networking protocol은 구현하지 않았습니다.

## Runtime 배포 구조

초기 Oracle VM runtime은 의도적으로 단순합니다.

```text
GitHub Actions
  linux/amd64 tarball build
  GitHub artifact와 Release asset publish

Oracle VM
  Release asset pull
  /opt/crawl-stars-server/releases/<sha> 아래에 immutable release 저장
  /opt/crawl-stars-server/current 전환
  systemd로 /opt/crawl-stars-server/current/crawl-stars-server 실행

Cloudflare
  tolerblanc.com public HTTPS 종료
  Cloudflare Tunnel로 api-crawlstars.tolerblanc.com을 127.0.0.1:8080에 route
  Cloudflare Tunnel로 tolerblanc.com을 local Caddy 127.0.0.1:8081에 route
```

systemd unit은 `SERVER_ADDR=127.0.0.1:8080`을 설정합니다. Public exposure path는 Cloudflare Tunnel입니다. Caddy는 apex hello page를 위한 local-only 용도입니다. Tailscale, Docker, Kubernetes, dashboard는 현재 범위 밖입니다.

## 가까운 방향

다음 architecture 작업은 구현 전에 첫 vertical slice를 정의해야 합니다.

- process model
- protocol boundary
- room lifecycle vocabulary
- validation 및 test strategy
- observability basics

첫 slice가 선택되기 전에 game architecture를 과도하게 일반화하지 않습니다.
