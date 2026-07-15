# 배포

현재 배포는 Oracle VM이 GitHub Release asset을 pull하는 방식입니다. Go server port는 public internet에 직접 열지 않고 Cloudflare Tunnel로 노출합니다.

## 흐름

```text
main push 또는 workflow_dispatch
  -> GitHub Actions
  -> docs build
  -> linux/amd64 server build
  -> crawl-stars-server-linux-amd64.tar.gz
  -> GitHub Release asset
  -> VM pull script
  -> systemd restart
  -> /health smoke check
```

이 방식은 SSH push deployment와 public inbound app port를 피합니다. 현재 repo는 public이라 VM은 token 없이 Release asset을 받을 수 있습니다. Repo가 private이 되면 VM 밖에서 `GH_TOKEN`을 구성합니다.

## VM layout

```text
/opt/crawl-stars-server/
  releases/
    <commit-sha>/
      crawl-stars-server
      VERSION
      COMMIT_SHA
  current -> releases/<current-sha>
  previous -> releases/<previous-sha>

/etc/systemd/system/crawl-stars-server.service
/etc/crawl-stars-server/environment
```

Service는 `crawlstars` user로 실행됩니다. Systemd unit은 application HTTP에 `SERVER_ADDR=127.0.0.1:8080`, private Prometheus endpoint에 `METRICS_ADDR=127.0.0.1:9090`을 사용합니다.

## Runtime environment와 secret

Systemd unit은 `/etc/crawl-stars-server/environment`를 읽습니다. 이 파일은 repo나 release directory에 두지 않고 root-owned mode `0600`으로 관리합니다.

```sh
sudo install -d -o root -g root -m 0755 /etc/crawl-stars-server
sudo touch /etc/crawl-stars-server/environment
sudo chown root:root /etc/crawl-stars-server/environment
sudo chmod 0600 /etc/crawl-stars-server/environment
sudoedit /etc/crawl-stars-server/environment
sudo stat -c '%U %G %a %n' /etc/crawl-stars-server/environment
```

설정 구조는 다음과 같습니다. `<...>`는 설명용 placeholder이며 실제 secret을 문서, shell history, Git에 복사하지 않습니다.

```dotenv
ENABLE_DEBUG_API=false
MATCHMAKING_JOIN_RATE_PER_MINUTE=10
MATCHMAKING_JOIN_BURST=4
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
# DEBUG_API_TOKEN=<set-only-when-debug-is-enabled>
```

- Production 기본은 `ENABLE_DEBUG_API=false`입니다. 이때 debug REST와 method fallback은 Bearer를 보내도 `404 not_found`입니다.
- Debug를 명시적으로 켤 때만 `DEBUG_API_TOKEN`을 강한 random secret으로 설정합니다. 빠졌거나 공백이면 server가 시작하지 않습니다.
- Debug enabled 상태에서 missing/wrong/multiple `Authorization`은 route dispatch 전에 `401 unauthorized`입니다. 올바른 Bearer 뒤에만 route별 2xx/404/405/409/500을 평가합니다. WebSocket GET은 debug Bearer 대신 player session query token을 씁니다.
- Join limiter 기본값은 10 requests/minute, burst 4입니다. Override 중 하나만 쓰면 다른 값은 default를 사용합니다. Non-positive, non-finite rate나 유효하지 않은 burst는 startup error입니다.
- `TRUSTED_PROXY_CIDRS`는 comma-separated CIDR입니다. Empty element, bare IP, invalid CIDR은 startup error입니다.
- Systemd unit의 `METRICS_ADDR`를 바꿀 때도 loopback IP literal과 숫자 port만 사용할 수 있습니다. `127.0.0.1:9090`, `[::1]:9090`은 가능하지만 hostname, wildcard, private/Tailscale IP는 startup error입니다.

설정을 바꾼 뒤에는 `sudo systemctl restart crawl-stars-server`와 status/health check를 실행합니다.

## 최초 systemd 설치

VM에서 repo를 clone 또는 copy한 뒤 실행합니다.

```sh
sudo scripts/deploy/install-systemd.sh
```

Script는 `crawlstars` user, release directory, systemd unit을 준비하고 service를 enable합니다. 현재 binary가 이미 있을 때만 즉시 restart합니다.

Server는 application과 metrics listener를 둘 다 먼저 bind하고 성공한 뒤에만 요청 처리를 시작합니다. 어느 한 listener가 bind에 실패하면 process가 non-zero로 종료되어 systemd의 `Restart=on-failure`가 적용됩니다.

## 최신 release 배포

```sh
sudo scripts/deploy/pull-latest.sh
```

Override:

```sh
REPO=Second-Loop/Server-CrawlStars sudo scripts/deploy/pull-latest.sh
RELEASE_TAG=server-<commit-sha> sudo scripts/deploy/pull-latest.sh
GH_TOKEN=<token> sudo -E scripts/deploy/pull-latest.sh
```

Script는 package를 download하고 release directory를 만든 뒤 `current` symlink를 전환합니다. Restart나 `/health` check가 실패하면 가능한 경우 이전 release로 rollback합니다.

## 운영 명령

```sh
sudo systemctl status crawl-stars-server
sudo systemctl restart crawl-stars-server
sudo journalctl -u crawl-stars-server -f
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:8080/openapi
curl -i http://127.0.0.1:8080/asyncapi
curl -i http://127.0.0.1:9090/metrics
```

Process, room lifecycle, WebSocket, HTTP server error는 journal에 JSON 한 줄로 기록합니다. Process event 이름은 `msg`에, room/WebSocket event 이름은 `event`와 `msg`에 기록합니다. Room/WebSocket log는 `roomID`, `playerID`처럼 정해진 필드만 사용하며 raw session token, request query, transport error 문자열은 기록하지 않습니다.

Metrics는 private listener의 정확한 `GET /metrics`에서만 제공합니다. Application `127.0.0.1:8080/metrics`, metrics listener의 다른 method/path는 404이고, `9090`을 Cloudflare Tunnel이나 public firewall에 연결하지 않습니다.

## Graceful shutdown

SIGINT/SIGTERM이나 어느 한 HTTP server 종료가 전체 application shutdown을 시작합니다. Process는 `rooms.Store`, application HTTP, metrics HTTP를 병렬로 정리하고 최대 10초 기다립니다. Store는 WebSocket에 normal close `1000 / server shutting down`을 보낸 뒤 room ticker, writer, heartbeat까지 join합니다. 10초가 지나면 남은 HTTP transport를 강제로 닫습니다.

Systemd unit의 `TimeoutStopSec=15s`가 process 내부 10초 grace보다 5초 더 길어서 종료 결과를 기록할 여유가 있습니다. 수동 검증은 다른 terminal에서 WebSocket을 연결한 뒤 다음 명령으로 실행합니다.

```sh
sudo systemctl stop crawl-stars-server
sudo journalctl -u crawl-stars-server -n 100 --no-pager
```

## Cloudflare Tunnel

Public edge는 Cloudflare입니다.

```text
internet
  -> Cloudflare HTTPS edge
  -> Cloudflare Tunnel
  -> cloudflared on VM
     -> api-crawlstars.tolerblanc.com  -> Go server 127.0.0.1:8080
     -> tolerblanc.com                 -> Caddy 127.0.0.1:8081

private operator only
  -> Prometheus metrics                -> Go server 127.0.0.1:9090
```

Expected public hostname:

```text
api-crawlstars.tolerblanc.com           http://127.0.0.1:8080
tolerblanc.com                          http://127.0.0.1:8081
```

Go server와 Caddy는 certificate를 직접 소유하지 않습니다. HTTPS는 Cloudflare edge가 종료합니다.

### Trusted client IP 경계

Cloudflare Tunnel은 VM의 `127.0.0.1:8080`으로 연결하므로 Go server가 보는 immediate peer는 보통 loopback cloudflared입니다. 위 예시처럼 실제 peer CIDR만 `TRUSTED_PROXY_CIDRS`에 넣었을 때에만 `CF-Connecting-IP`를 client IP로 사용합니다.

```text
immediate peer가 trusted CIDR 밖
  -> CF-Connecting-IP 무시
  -> peer IP bucket 사용

immediate peer가 trusted CIDR 안
  -> CF-Connecting-IP가 정확히 1개 valid IP면 client bucket 사용
  -> header가 missing/malformed/multiple이면 peer IP bucket으로 fallback
```

`X-Forwarded-For`는 항상 무시합니다. Loopback cloudflared를 trust하지 않거나 CF header가 올바르지 않으면 public client가 하나의 loopback bucket을 공유해 429를 함께 받을 수 있습니다. 이는 header spoofing을 막는 fail-closed fallback이지만 가용성에 영향을 주므로 배포 후 서로 다른 client IP에서 rate-limit 동작을 확인합니다. Invalid `RemoteAddr`도 하나의 invalid-IP bucket을 공유합니다.

Join limiter는 store보다 먼저 quota를 평가합니다. Bucket이 비면 room cap 409나 발급 실패 500보다 429가 우선하고, 허용된 요청이 나중에 409/500으로 끝나도 quota를 소비합니다. 429는 `rate_limited` JSON과 대기 시간을 올림한 최소 1초 정수 `Retry-After`를 반환합니다.

Player 발급 JSON의 `sessionToken`, tokenized `webSocketPath`, inbound WebSocket query, `DEBUG_API_TOKEN`은 모두 secret-bearing surface입니다. Raw 값이나 전체 request query를 journal, access log, telemetry에 기록하지 않습니다.

## Local Caddy hello page

Caddy는 apex `tolerblanc.com` hello response용 local service입니다.

```sh
sudo scripts/deploy/install-caddy.sh
```

현재 Caddyfile:

```caddyfile
{
	auto_https off
}

:8081 {
	bind 127.0.0.1
	respond "Hello from Server Crawl Stars" 200
}
```

Caddy는 `127.0.0.1:8081`에서만 listen합니다. Public path는 Cloudflare Tunnel뿐입니다.

## Firewall

이 tunnel-backed 구조에서 application HTTP/HTTPS용 public inbound는 필요 없습니다.

열지 않는 port:

```text
80/tcp
443/tcp
8080/tcp
8081/tcp
9090/tcp
```

Public Caddy edge나 direct ingress로 바꾸려면 별도 issue와 명시적 approval이 필요합니다.

## Rollback

```sh
sudo scripts/deploy/rollback.sh
```

Manual equivalent:

```sh
sudo ln -sfn /opt/crawl-stars-server/releases/<previous-sha> /opt/crawl-stars-server/current
sudo systemctl restart crawl-stars-server
curl -fsS http://127.0.0.1:8080/health
```

## Validation

Local:

```sh
make ci
bash -n scripts/deploy/*.sh
```

VM:

```sh
sudo scripts/deploy/install-systemd.sh
sudo scripts/deploy/pull-latest.sh
systemctl status crawl-stars-server
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:9090/metrics
curl -i https://api-crawlstars.tolerblanc.com/health
```

Public hostname이 실패하면 Cloudflare Tunnel log와 DNS를 먼저 봅니다.

```sh
sudo journalctl -u cloudflared -n 200 --no-pager
dig @1.1.1.1 api-crawlstars.tolerblanc.com A
```
