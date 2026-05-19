# Deployment

## Scope

SL-6 implements the first Oracle VM CD path for the Go server. It does not configure OCI Security Lists, NSGs, public ingress, Cloudflare Tunnel, Tailscale, Docker, Kubernetes, nginx, caddy, dashboards, persistence, or gameplay services.

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

The service runs as user `crawlstars` and binds to `127.0.0.1:8080` through `SERVER_ADDR`.

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
bash -n scripts/deploy/install-systemd.sh scripts/deploy/pull-latest.sh scripts/deploy/rollback.sh
```

VM validation after a real release exists:

```sh
sudo scripts/deploy/install-systemd.sh
sudo scripts/deploy/pull-latest.sh
systemctl status crawl-stars-server
curl -i http://127.0.0.1:8080/health
```
