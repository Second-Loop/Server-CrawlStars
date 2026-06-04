.PHONY: fmt fmt-check docs-install docs-validate docs-build vet test build deploy-check ci

GO ?= go
GOFMT ?= gofmt
NODE ?= node
MISE ?= $(firstword $(shell command -v mise 2>/dev/null) $(wildcard /opt/homebrew/bin/mise) $(wildcard /usr/local/bin/mise))
GO_CMD := $(if $(MISE),$(MISE) exec -- $(GO),$(GO))
GOFMT_CMD := $(if $(MISE),$(MISE) exec -- $(GOFMT),$(GOFMT))
NODE_CMD := $(if $(MISE),$(MISE) exec -- $(NODE),$(NODE))
GO_CACHE ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE ?= $(CURDIR)/.cache/go-mod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)
GO_FILES := $(shell find . \
	-path './.git' -prune -o \
	-path './.cache' -prune -o \
	-path './docs-ui/node_modules' -prune -o \
	-name '*.go' -print)

fmt:
	$(GOFMT_CMD) -w $(GO_FILES)

fmt-check:
	@files="$$($(GOFMT_CMD) -l $(GO_FILES))" || exit $$?; test -z "$$files"

docs-install:
	$(NODE_CMD) --version >/dev/null

docs-validate:
	$(NODE_CMD) docs-ui/scripts/validate.mjs

docs-build: docs-validate
	$(NODE_CMD) docs-ui/scripts/build.mjs

vet:
	$(GO_ENV) $(GO_CMD) vet ./...

test:
	$(GO_ENV) $(GO_CMD) test ./...

build:
	$(GO_ENV) $(GO_CMD) build ./cmd/server

deploy-check:
	bash -n scripts/deploy/*.sh

ci: docs-install docs-build fmt-check vet test build deploy-check
