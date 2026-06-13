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
```

Service는 `crawlstars` user로 실행되고 `SERVER_ADDR=127.0.0.1:8080`을 사용합니다.

## 최초 systemd 설치

VM에서 repo를 clone 또는 copy한 뒤 실행합니다.

```sh
sudo scripts/deploy/install-systemd.sh
```

Script는 `crawlstars` user, release directory, systemd unit을 준비하고 service를 enable합니다. 현재 binary가 이미 있을 때만 즉시 restart합니다.

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
```

Expected public hostname:

```text
api-crawlstars.tolerblanc.com           http://127.0.0.1:8080
tolerblanc.com                          http://127.0.0.1:8081
```

Go server와 Caddy는 certificate를 직접 소유하지 않습니다. HTTPS는 Cloudflare edge가 종료합니다.

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
curl -i https://api-crawlstars.tolerblanc.com/health
```

Public hostname이 실패하면 Cloudflare Tunnel log와 DNS를 먼저 봅니다.

```sh
sudo journalctl -u cloudflared -n 200 --no-pager
dig @1.1.1.1 api-crawlstars.tolerblanc.com A
```
