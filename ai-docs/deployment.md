# Deployment

## Scope

SL-6은 Go server를 위한 첫 Oracle VM CD path를 구현합니다. SL-35는 OCI public application ingress를 닫아둔 채 Cloudflare Tunnel로 runtime을 노출합니다. 또한 `tolerblanc.com`을 위한 local-only Caddy 2 hello page를 유지합니다. 이 범위에는 Tailscale, Docker, Kubernetes, nginx, dashboard, persistence, gameplay service 구성이 포함되지 않습니다.

## CD Choice

이 레포지토리는 VM pull deployment를 사용합니다.

1. GitHub Actions가 `main` push 또는 수동 `workflow_dispatch`에서 실행됩니다.
2. Actions가 docs UI와 raw spec embed 파일을 build합니다.
3. Actions가 `linux/amd64`용 `./cmd/server`를 build합니다.
4. Actions가 binary를 `crawl-stars-server-linux-amd64.tar.gz`로 package합니다.
5. Actions가 package를 short-lived workflow artifact와 GitHub Release asset으로 저장합니다.
6. VM이 최신 Release asset을 pull하고 local에 install합니다.
7. systemd가 server를 restart합니다.
8. deploy script가 `http://127.0.0.1:8080/health`를 확인합니다.

이 방식은 SSH push deployment를 피하고 VM에 inbound application port를 요구하지 않습니다. 현재 repository는 public이므로 VM은 token 없이 Release asset을 download할 수 있습니다. Repository가 private이 되면 VM에 `GH_TOKEN`을 repository 밖에서 구성합니다.

## VM Layout

```text
/opt/crawl-stars-server/
  releases/
    <commit-sha>/
      crawl-stars-server
      VERSION
      COMMIT_SHA
  current -> /opt/crawl-stars-server/releases/<current-sha>
  previous -> /opt/crawl-stars-server/releases/<previous-sha>

/etc/systemd/system/crawl-stars-server.service
```

Service는 `crawlstars` user로 실행되고 `SERVER_ADDR`를 통해 `127.0.0.1:8080`에 bind합니다. Go server port를 public internet에 직접 노출하지 않습니다. Cloudflare Tunnel이 public HTTP/HTTPS entrypoint여야 합니다.

## Bootstrap systemd

VM에서 repository를 copy 또는 clone한 뒤 실행합니다.

```sh
sudo scripts/deploy/install-systemd.sh
```

Script가 수행하는 작업:

- 없으면 `crawlstars` system user 생성
- `/opt/crawl-stars-server/releases` 생성
- `scripts/deploy/crawl-stars-server.service` 설치
- `systemctl daemon-reload` 실행
- `crawl-stars-server` enable

`/opt/crawl-stars-server/current/crawl-stars-server`가 이미 있을 때만 service를 즉시 restart합니다.

## Deploy Latest Release

VM에서 실행합니다.

```sh
sudo scripts/deploy/pull-latest.sh
```

유용한 override:

```sh
REPO=Second-Loop/Server-CrawlStars sudo scripts/deploy/pull-latest.sh
RELEASE_TAG=server-<commit-sha> sudo scripts/deploy/pull-latest.sh
GH_TOKEN=<token> sudo -E scripts/deploy/pull-latest.sh
```

Script는 package를 download하고 `/opt/crawl-stars-server/releases/<sha>` 아래에 install한 뒤, `previous`와 `current`를 update하고, `crawl-stars-server`를 restart하고, 다음을 실행합니다.

```sh
curl -fsS http://127.0.0.1:8080/health
```

Restart 또는 smoke check가 실패하면, 이전 `current` symlink가 있을 때 prior release를 restore하고 previous release를 restart합니다.

## Service Management

```sh
sudo systemctl status crawl-stars-server
sudo systemctl restart crawl-stars-server
sudo journalctl -u crawl-stars-server -f
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:8080/openapi
curl -i http://127.0.0.1:8080/asyncapi
```

## Public HTTPS With Cloudflare Tunnel

Production public edge는 Cloudflare입니다. VM은 `cloudflared`를 실행하고 Cloudflare로 outbound connectivity만 필요합니다.

```text
internet
  -> Cloudflare HTTPS edge
  -> Cloudflare Tunnel
  -> cloudflared on VM
     -> api-crawlstars.tolerblanc.com  -> Go server 127.0.0.1:8080
     -> tolerblanc.com                 -> Caddy 127.0.0.1:8081
```

Cloudflare Zero Trust에서 named tunnel을 만들고, 생성된 connector command를 VM에 설치한 뒤 public hostname을 구성합니다.

```text
Tunnel name: crawl-stars-vm

Public hostname                         Service
api-crawlstars.tolerblanc.com           http://127.0.0.1:8080
tolerblanc.com                          http://127.0.0.1:8081
```

Cloudflare는 두 이름에 대한 Tunnel DNS record를 만듭니다. Route는 있지만 DNS가 resolve되지 않으면 다음 target으로 proxied CNAME/Tunnel record를 추가합니다.

```text
<tunnel-id>.cfargotunnel.com
```

Cloudflare는 public HTTPS를 종료하고 이 hostname들의 public certificate를 처리합니다. Go server는 certificate를 소유하지 않습니다. 이 tunnel-backed setup에서는 Caddy도 public certificate를 소유하지 않습니다.

## Local Caddy Hello Page

Caddy는 apex `tolerblanc.com` hello response를 위한 local HTTP service로만 사용합니다. VM에서 이 repository를 checkout한 위치에서 Caddy를 설치 또는 업데이트합니다.

```sh
sudo scripts/deploy/install-caddy.sh
```

Script는 Caddy가 없으면 official Caddy Debian/Ubuntu package repository를 통해 설치하고, `scripts/deploy/Caddyfile`을 `/etc/caddy/Caddyfile`로 copy하고, config를 validate하고, `caddy` systemd service를 enable하고 reload 또는 restart합니다. `/etc/caddy/Caddyfile`이 이미 있고 내용이 다르면 timestamped backup을 남깁니다. `/var/lib/caddy` 또는 `/etc/caddy`는 삭제하지 않습니다.

Caddy가 이미 설치되어 있고 package installation을 skip해야 할 때:

```sh
SKIP_CADDY_INSTALL=1 sudo -E scripts/deploy/install-caddy.sh
```

Current Caddyfile:

```caddyfile
{
	auto_https off
}

:8081 {
	bind 127.0.0.1
	respond "Hello from Server Crawl Stars" 200
}
```

Caddy는 의도적으로 `127.0.0.1:8081`에서만 listen합니다. 이 hello page로 가는 유일한 public path는 Cloudflare Tunnel입니다.

### Cloudflare DNS

Expected DNS records:

```text
Tunnel  tolerblanc.com                 crawl-stars-vm
Tunnel  api-crawlstars.tolerblanc.com  crawl-stars-vm
```

이 record들은 보통 Zero Trust tunnel public hostname UI가 생성합니다. 수동 복구가 필요하면 tunnel target으로 proxied CNAME record를 만듭니다. Go server가 WebSocket route를 구현하면 Cloudflare WebSocket도 같은 hostname을 통해 지원됩니다.

### Firewall And Ingress

이 tunnel-backed path에 필요한 public inbound:

```text
none for application HTTP/HTTPS
```

`8080/tcp` 또는 `8081/tcp`를 public으로 열지 않습니다. Go server와 local Caddy hello page는 VM 자체에서만 접근 가능해야 합니다.

```sh
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:8081/
```

`ufw`가 active여도 이 Cloudflare Tunnel path에는 새 public HTTP/HTTPS allow rule이 필요하지 않습니다. OCI Security Lists 또는 NSG를 사용한다면, architecture가 direct public Caddy로 의도적으로 바뀌기 전까지 `80/tcp`, `443/tcp`, `8080/tcp`, `8081/tcp` public application ingress를 추가하지 않습니다.

## Rollback

Manual rollback은 `previous` symlink를 사용합니다.

```sh
sudo scripts/deploy/rollback.sh
```

Script는 `current`를 `previous`로 전환하고, 이전 current release를 `previous`로 옮기고, systemd를 restart하고, `/health`를 확인합니다.

Manual equivalent:

```sh
sudo ln -sfn /opt/crawl-stars-server/releases/<previous-sha> /opt/crawl-stars-server/current
sudo systemctl restart crawl-stars-server
curl -fsS http://127.0.0.1:8080/health
```

## Validation

Local validation:

```sh
make ci
bash -n scripts/deploy/*.sh
```

실제 release가 존재한 뒤 VM validation:

```sh
sudo scripts/deploy/install-systemd.sh
sudo scripts/deploy/pull-latest.sh
systemctl status crawl-stars-server
curl -i http://127.0.0.1:8080/health
sudo scripts/deploy/install-caddy.sh
caddy validate --config /etc/caddy/Caddyfile
systemctl status caddy
systemctl status cloudflared
curl -i http://127.0.0.1:8081/
curl -i https://api-crawlstars.tolerblanc.com/health
curl -i https://tolerblanc.com
```

Public hostname이 실패하면 Cloudflare Tunnel log와 DNS를 먼저 확인합니다.

```sh
sudo journalctl -u cloudflared -n 200 --no-pager
dig @1.1.1.1 api-crawlstars.tolerblanc.com A
```

일반적인 원인은 누락된 Tunnel DNS record, 중지된 `cloudflared` service, 오래된 tunnel public hostname route, 또는 local upstream이 `127.0.0.1:8080`이나 `127.0.0.1:8081`에서 listen하지 않는 경우입니다.
