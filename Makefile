.PHONY: fmt fmt-check docs-install docs-validate docs-build vet test build deploy-check ci

GO_CACHE ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE ?= $(CURDIR)/.cache/go-mod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)"

docs-install:
	npm --prefix docs-ui ci

docs-validate:
	npm --prefix docs-ui run validate

docs-build: docs-validate
	npm --prefix docs-ui run build

vet:
	$(GO_ENV) go vet ./...

test:
	$(GO_ENV) go test ./...

build:
	$(GO_ENV) go build ./cmd/server

deploy-check:
	bash -n scripts/deploy/*.sh

ci: docs-install docs-build fmt-check vet test build deploy-check
