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

## Local Equivalent

```sh
make ci
```

The Makefile sets `GOCACHE` inside the repository so local sandboxed runs do not depend on user-level Go cache permissions.

