.PHONY: build build-agent test tui-test tui-typed-gate test-all-modules standalone-smoke standalone-http-smoke standalone-lifecycle-gate provider-reset-chat-e2e published-npm-canary npm-test artifacts clean agent-work

BINARY=opute-host-agent
DIST=dist
MODULE=github.com/wunderous/host-agents
VERSION ?= 0.1.1
LDFLAGS=-s -w -X $(MODULE)/internal/version.Version=$(VERSION)

build: build-agent build-tui

build-agent:
	mkdir -p $(DIST)
	go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/opute-host-agent

build-tui:
	mkdir -p $(DIST)
	go -C clients/tui build -o ../../$(DIST)/opute-host-agent-tui ./cmd/opute-host-agent-tui

test:
	go test ./...

tui-test:
	cd clients/tui && go test ./...

tui-typed-gate:
	cd clients/tui && go test -v ./internal/tui -run 'TestTyped(EntityFlowUsesCanonicalBindingAndCurrentCatalog|DraftUsesCatalogSchemaAndPreservesProvenance|EntityFlowRejectsStaleBinding)|TestParserDoesNotInferProseAndSupportsQuotedValues'

test-all-modules: test tui-test
	cd plugins/llm/ollama && go test ./...
	cd plugins/tunneling/cloudflare && go test ./...

npm-test:
	cd npm/local-host-agent && npm test

standalone-smoke: build
	OPUTE_AGENT_MODE=standalone OPUTE_INFRA_PROVIDER_ID=incus OPUTE_STANDALONE_STATE_DIR="$$(mktemp -d)" $(DIST)/$(BINARY) --check

standalone-http-smoke: build-agent
	OPUTE_STANDALONE_BINARY=$(CURDIR)/$(DIST)/$(BINARY) go test ./test/standalone -count=1

standalone-lifecycle-gate: build-linux-x64
	go test -tags=integration ./test/live -count=1

provider-reset-chat-e2e:
	./scripts/provider-reset-chat-e2e.sh

published-npm-canary:
	cd npm/local-host-agent && PUBLISHED_NPM_VERSION=$(VERSION) npm run test:published-canary

artifacts: build-linux-x64 build-linux-arm64 checksums

build-linux-x64:
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-linux-x64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-x64

build-linux-arm64:
	mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-linux-arm64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-arm64

checksums:
	sha256sum $(DIST)/host-agent-linux-x64.gz $(DIST)/host-agent-linux-arm64.gz > $(DIST)/SHA256SUMS

clean:
	rm -rf $(DIST)

export-schemas:
	cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas

# The shared adapter lives in the TypeScript control-plane repo so both
# repositories use one Beads database and one metadata convention.
# Example: make agent-work ARGS="status" or
#         make agent-work ARGS="start --title=... --touches=..."
AGENT_WORK_REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
OPUTE_ROOT := $(abspath $(AGENT_WORK_REPO_ROOT)/../opute)
BUN := $(OPUTE_BUN_PATH)
ifeq ($(strip $(BUN)),)
BUN := $(shell command -v bun 2>/dev/null)
endif
ifeq ($(strip $(BUN)),)
BUN := $(HOME)/.bun/bin/bun
endif

agent-work:
	OPUTE_BUN_PATH="$(BUN)" "$(AGENT_WORK_REPO_ROOT)/scripts/agent-work" $(ARGS)
