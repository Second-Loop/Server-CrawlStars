# CI

GitHub Actions는 pull request와 `main` push에서 실행되어야 합니다.

## 필수 단계

- checkout
- `go.mod` 기준 Go setup
- `go mod download`
- gofmt check
- `go vet ./...`
- `go test ./...`
- `go build ./cmd/server`
- deployment script 대상 `bash -n`

## 로컬 동등 명령

```sh
make ci
```

Makefile은 `GOCACHE`를 레포지토리 내부로 설정합니다. 따라서 sandboxed local run이 user-level Go cache 권한에 의존하지 않습니다.

# CD

CD workflow는 Oracle VM 배포를 위한 Go server package를 만듭니다.

## Trigger

- 수동 `workflow_dispatch`
- `main` push

## Output

- `./cmd/server`에서 build한 linux/amd64 binary
- `crawl-stars-server-linux-amd64.tar.gz`
- `SHA256SUMS`
- 단기 추적을 위한 GitHub Actions artifact
- VM pull deployment를 위한 `server-<commit-sha>` tag의 GitHub Release asset

VM deployment script는 기본적으로 레포지토리의 최신 GitHub Release asset을 사용합니다. 현재 레포지토리는 public이므로 초기 배포 경로에서는 VM에 GitHub token이 필요하지 않습니다. 레포지토리가 private으로 바뀌면 VM은 레포지토리 밖에서 `GH_TOKEN`으로 노출되는 최소 권한 token을 사용해야 합니다.
