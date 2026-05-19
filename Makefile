.PHONY: fmt fmt-check vet test build ci

GO_CACHE ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE ?= $(CURDIR)/.cache/go-mod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)"

vet:
	$(GO_ENV) go vet ./...

test:
	$(GO_ENV) go test ./...

build:
	$(GO_ENV) go build ./cmd/server

ci: fmt-check vet test build
