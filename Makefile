.PHONY: fmt fmt-check docs-install docs-validate docs-schema docs-build vet vuln-check test build deploy-test deploy-check ci

GO ?= go
GOFMT ?= gofmt
NODE ?= node
NPX ?= npx
MISE ?= $(firstword $(shell command -v mise 2>/dev/null) $(wildcard /opt/homebrew/bin/mise) $(wildcard /usr/local/bin/mise))
GO_CMD := $(if $(MISE),$(MISE) exec -- $(GO),$(GO))
GOFMT_CMD := $(if $(MISE),$(MISE) exec -- $(GOFMT),$(GOFMT))
NODE_CMD := $(if $(MISE),$(MISE) exec -- $(NODE),$(NODE))
NPX_CMD := $(if $(MISE),$(MISE) exec -- $(NPX),$(NPX))
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

docs-schema: docs-validate
	REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true $(NPX_CMD) --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
	$(NPX_CMD) --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error

docs-build: docs-schema
	$(NODE_CMD) docs-ui/scripts/build.mjs

vet:
	$(GO_ENV) $(GO_CMD) vet ./...

vuln-check: docs-build
	$(GO_ENV) $(GO_CMD) run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

test:
	$(GO_ENV) $(GO_CMD) test ./...

build:
	$(GO_ENV) $(GO_CMD) build ./cmd/server

deploy-test:
	bash scripts/deploy/pull-latest_test.sh

deploy-check: deploy-test
	bash -n scripts/deploy/*.sh

ci: docs-install docs-build fmt-check vet vuln-check test build deploy-check
