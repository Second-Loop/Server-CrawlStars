# CI

GitHub Actions should run on pull requests and pushes to `main`.

## Required Steps

- checkout
- setup Go from `go.mod`
- `go mod download`
- gofmt check
- `go vet ./...`
- `go test ./...`
- `go build ./cmd/server`
- `bash -n` for deployment scripts

## Local Equivalent

```sh
make ci
```

The Makefile sets `GOCACHE` inside the repository so local sandboxed runs do not depend on user-level Go cache permissions.

# CD

The CD workflow packages the Go server for Oracle VM deployment.

## Trigger

- Manual `workflow_dispatch`
- Pushes to `main`

## Output

- linux/amd64 binary built from `./cmd/server`
- `crawl-stars-server-linux-amd64.tar.gz`
- `SHA256SUMS`
- GitHub Actions artifact retained for short-term traceability
- GitHub Release asset under tag `server-<commit-sha>` for VM pull deployment

The VM deployment script defaults to the repository's latest GitHub Release asset. The repository is currently public, so the VM does not need a GitHub token for the initial deployment path. If the repository becomes private, the VM should use a minimum-scope token exposed as `GH_TOKEN` outside the repository.
