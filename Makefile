# phosphor — the host CLI: one static binary per platform.
SHELL := /bin/bash
GO ?= go
PKG := github.com/asafg8/Phosphor
VERSION := $(shell git describe --always --tags --dirty 2>/dev/null || echo dev)
SRC_DIRS := src
BUILD := $(shell find $(SRC_DIRS) -name '*.go' -not -name '*_test.go' | LC_ALL=C sort | xargs cat | shasum -a 256 | cut -c1-8)
LDFLAGS := -s -w -X $(PKG)/src/internal/version.Describe=$(VERSION) -X $(PKG)/src/internal/version.Build=$(BUILD)
# every package but the e2e suite
PKGS := $(shell $(GO) list ./... | grep -v '/testing/e2e')
T ?=
E2E_TAGS_LOCAL := e2e local

.PHONY: build test lint schemas bundle version lab-up lab-down lab-reset lab-fixture-reset \
	e2e e2e-local e2e-local-fast e2e-regress regress-verify

version:
	@echo $(VERSION)-$(BUILD)

# build: bin/phosphor-linux-{amd64,arm64} (what goes to hosts) and bin/phosphor for this machine. The linux binaries
# carry the PHPCompatibility scan bundle of their arch (src/verifier/scan/bundle, a copy of `make bundle`'s dist/ output),
# the data locks and patches; nothing is fetched at run time except pinned upstream sources for a build.
build:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/phosphor-linux-amd64 ./src/cmd/phosphor
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/phosphor-linux-arm64 ./src/cmd/phosphor
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/phosphor ./src/cmd/phosphor

test: lint
	$(GO) test -short $(PKGS)

lint:
	@test -z "$$(gofmt -l $(SRC_DIRS) 2>/dev/null)" || (gofmt -l $(SRC_DIRS); exit 1)
	$(GO) vet $(PKGS)

schemas:
	$(GO) run ./src/contract/cmd/genschema src/contract/schema

# bundle: the PHPCompatibility scan bundles (dist/scanbundle-{amd64,arm64}.tar.zst) from src/scanbundle/bundle.lock,
# copied to src/verifier/scan/bundle/ where the binary embeds them.
bundle:
	$(GO) run ./src/scanbundle/cmd/mkbundle -lock src/scanbundle/bundle.lock -cache .cache/bundle -out dist
	cp dist/scanbundle-amd64.tar.zst dist/scanbundle-arm64.tar.zst src/verifier/scan/bundle/

# ---- local lab ----
comma := ,
lab-up:
	testing/lab/bin/lab up $(subst $(comma), ,$(FIXTURES))

lab-down:
	testing/lab/bin/lab down

lab-reset:
	testing/lab/bin/lab reset

lab-fixture-reset:
	testing/lab/bin/lab fixture-reset $(FX)

# ---- e2e ----
e2e: lint
	@test -n "$(T)" || (echo "usage: make e2e T=T-702"; exit 1)
	source testing/lab/env.local && $(GO) test -count=1 -tags '$(E2E_TAGS_LOCAL)' -timeout 120m -v -run '^Test$(subst -,,$(T))_' ./testing/e2e/local/... ./testing/e2e/regress/...

e2e-local: lint build bundle
	testing/lab/bin/lab reset && testing/lab/bin/lab up
	source testing/lab/env.local && $(GO) test -count=1 -tags '$(E2E_TAGS_LOCAL)' -timeout 240m -parallel 6 -v ./testing/e2e/local/... ./testing/e2e/regress/...; rc=$$?; \
	  if [ "$(KEEP)" != "1" ]; then testing/lab/bin/lab down; fi; exit $$rc

e2e-local-fast: lint build
	source testing/lab/env.local && $(GO) test -count=1 -short -tags '$(E2E_TAGS_LOCAL)' -timeout 240m -parallel 6 -v ./testing/e2e/local/... ./testing/e2e/regress/...

e2e-regress: lint
	source testing/lab/env.local && $(GO) test -count=1 -tags '$(E2E_TAGS_LOCAL)' -timeout 120m -v ./testing/e2e/regress/...

regress-verify:
	@test -n "$(BUG)" || (echo "usage: make regress-verify BUG=BUG-007"; exit 1)
	testing/scripts/regress-verify.sh $(BUG)
