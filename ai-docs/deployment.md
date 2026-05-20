# Deployment

## Scope

SL-6 implements the first Oracle VM CD path for the Go server. SL-35 exposes that runtime through Cloudflare Tunnel so OCI public application ingress can stay closed. It also keeps a local-only Caddy 2 hello page for `tolerblanc.com`. It does not configure Tailscale, Docker, Kubernetes, nginx, dashboards, persistence, or gameplay services.

## CD Choice

The repository uses VM pull deployment:

1. GitHub Actions runs on `main` push or manual `workflow_dispatch`.
2. Actions builds `./cmd/server` for `linux/amd64`.
3. Actions packages the binary as `crawl-stars-server-linux-amd64.tar.gz`.
4. Actions stores the package as both a short-lived workflow artifact and a GitHub Release asset.
5. The VM pulls the latest Release asset and installs it locally.
6. systemd restarts the server.
7. The deploy script checks `http://127.0.0.1:8080/health`.

This avoids SSH push deployment and avoids requiring any inbound application port on the VM. Because the repository is currently public, the VM can download Release assets without committing a token. If the repository becomes private, configure `GH_TOKEN` on the VM outside the repository.

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

The service runs as user `crawlstars` and binds to `127.0.0.1:8080` through `SERVER_ADDR`. Do not expose this Go server port directly to the public internet; Cloudflare Tunnel should be the public HTTP/HTTPS entrypoint.

## Bootstrap systemd

Copy or clone the repository on the VM, then run:

```sh
sudo scripts/deploy/install-systemd.sh
```

The script:

- creates the `crawlstars` system user if missing
- creates `/opt/crawl-stars-server/releases`
- installs `scripts/deploy/crawl-stars-server.service`
- runs `systemctl daemon-reload`
- enables `crawl-stars-server`

It only restarts the service immediately when `/opt/crawl-stars-server/current/crawl-stars-server` already exists.

## Deploy Latest Release

Run on the VM:

```sh
sudo scripts/deploy/pull-latest.sh
```

Useful overrides:

```sh
REPO=Second-Loop/Server-CrawlStars sudo scripts/deploy/pull-latest.sh
RELEASE_TAG=server-<commit-sha> sudo scripts/deploy/pull-latest.sh
GH_TOKEN=<token> sudo -E scripts/deploy/pull-latest.sh
```

The script downloads the package, installs it under `/opt/crawl-stars-server/releases/<sha>`, updates `previous` and `current`, restarts `crawl-stars-server`, and runs:

```sh
curl -fsS http://127.0.0.1:8080/health
```

If restart or smoke check fails, the script restores the prior `current` symlink when one exists and restarts the previous release.

## Service Management

```sh
sudo systemctl status crawl-stars-server
sudo systemctl restart crawl-stars-server
sudo journalctl -u crawl-stars-server -f
curl -i http://127.0.0.1:8080/health
```

## Public HTTPS With Cloudflare Tunnel

The production public edge is Cloudflare. The VM runs `cloudflared` and only needs outbound connectivity to Cloudflare:

```text
internet
  -> Cloudflare HTTPS edge
  -> Cloudflare Tunnel
  -> cloudflared on VM
     -> api-crawlstars.tolerblanc.com  -> Go server 127.0.0.1:8080
     -> tolerblanc.com                 -> Caddy 127.0.0.1:8081
```

Create a named tunnel in Cloudflare Zero Trust, install the generated connector command on the VM, and configure public hostnames:

```text
Tunnel name: crawl-stars-vm

Public hostname                         Service
api-crawlstars.tolerblanc.com           http://127.0.0.1:8080
tolerblanc.com                          http://127.0.0.1:8081
```

Cloudflare creates Tunnel DNS records for both names. If a route exists but DNS does not resolve, add a proxied CNAME/Tunnel record pointing to:

```text
<tunnel-id>.cfargotunnel.com
```

Cloudflare terminates public HTTPS and handles the public certificate for these hostnames. The Go server does not own certificates. Caddy also does not own public certificates in this tunnel-backed setup.

## Local Caddy Hello Page

Caddy is only used as a local HTTP service for the apex `tolerblanc.com` hello response. Install or update Caddy on the VM from a checked-out copy of this repository:

```sh
sudo scripts/deploy/install-caddy.sh
```

The script installs Caddy through the official Caddy Debian/Ubuntu package repository when Caddy is missing, copies `scripts/deploy/Caddyfile` to `/etc/caddy/Caddyfile`, validates the config, enables the `caddy` systemd service, and reloads or restarts it. If `/etc/caddy/Caddyfile` already exists and differs, the script keeps a timestamped backup. It does not delete `/var/lib/caddy` or `/etc/caddy`.

If Caddy is already installed and package installation should be skipped:

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

Caddy intentionally listens only on `127.0.0.1:8081`; Cloudflare Tunnel is the only public path to this hello page.

### Cloudflare DNS

Expected DNS records:

```text
Tunnel  tolerblanc.com                 crawl-stars-vm
Tunnel  api-crawlstars.tolerblanc.com  crawl-stars-vm
```

These are usually created by the Zero Trust tunnel public hostname UI. For manual repair, create proxied CNAME records to the tunnel target. Cloudflare WebSockets are supported through the same hostname once the Go server implements a WebSocket route.

### Firewall And Ingress

Required public inbound for this tunnel-backed path:

```text
none for application HTTP/HTTPS
```

Do not open `8080/tcp` or `8081/tcp` publicly. The Go server and local Caddy hello page should remain reachable only from the VM itself:

```sh
curl -i http://127.0.0.1:8080/health
curl -i http://127.0.0.1:8081/
```

If `ufw` is active, no new public HTTP/HTTPS allow rules are needed for this Cloudflare Tunnel path. If OCI Security Lists or NSGs are in use, do not add public application ingress for `80/tcp`, `443/tcp`, `8080/tcp`, or `8081/tcp` unless the architecture intentionally changes back to direct public Caddy.


## Rollback

Manual rollback uses the `previous` symlink:

```sh
sudo scripts/deploy/rollback.sh
```

The script switches `current` to `previous`, moves the old current release into `previous`, restarts systemd, and checks `/health`.

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

VM validation after a real release exists:

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

If the public hostname fails, inspect Cloudflare Tunnel logs and DNS first:

```sh
sudo journalctl -u cloudflared -n 200 --no-pager
dig @1.1.1.1 api-crawlstars.tolerblanc.com A
```

Common causes are missing Tunnel DNS records, a stopped `cloudflared` service, a stale tunnel public hostname route, or the local upstream not listening on `127.0.0.1:8080` or `127.0.0.1:8081`.
