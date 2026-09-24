.PHONY: build build-agent test check-host-file-confinement test-all-modules openrouter-llm-smoke standalone-smoke standalone-http-smoke standalone-lifecycle-gate published-npm-canary published-npm-readonly-canary npm-test local-npm-readonly-canary check-site artifacts build-provider-linux-x64 build-provider-cloudflare-linux-x64 build-provider-tailscale-linux-x64 build-windows-x64 clean agent-work

BINARY=opute-host-agent
DIST=dist
MODULE=github.com/wunderous/host-agents
VERSION ?= 0.2.0
LDFLAGS=-s -w -X $(MODULE)/internal/version.Version=$(VERSION)

build: build-agent

build-agent:
	mkdir -p $(DIST)
	go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/opute-host-agent

test: check-host-file-confinement
	go test ./...

check-host-file-confinement:
	python3 scripts/check_managed_host_file_confinement.py

test-all-modules: test
	cd plugins/kubernetes/k3s && go test ./...
	cd plugins/llm/ollama && go test ./...
	cd plugins/tunneling/cloudflare && go test ./...
	cd plugins/tunneling/tailscale && go test ./...
	cd plugins/platform/hostos && go test ./...

openrouter-llm-smoke:
	for env_file in .env ../opute/.env; do \
		if [ -z "$$OPENROUTER_API_KEY" ] && [ -f "$$env_file" ]; then \
			set -a; . "$$env_file"; set +a; \
		fi; \
	done; \
	go test -tags=openrouter ./test/openrouter -count=1 -timeout=2m -v

npm-test:
	cd npm/local-host-agent && npm test

standalone-smoke: build
	OPUTE_REMOTE_AGENT_ID=test-host-agent OPUTE_AGENT_MODE=standalone OPUTE_INFRA_PROVIDER_ID=incus OPUTE_STANDALONE_STATE_DIR="$$(mktemp -d)" $(DIST)/$(BINARY) --check

standalone-http-smoke: build-agent
	OPUTE_STANDALONE_BINARY=$(CURDIR)/$(DIST)/$(BINARY) go test ./test/standalone -count=1

standalone-lifecycle-gate: build-linux-x64
	go test -tags=integration ./test/live -count=1

published-npm-canary:
	cd npm/local-host-agent && PUBLISHED_NPM_VERSION=$(VERSION) npm run test:published-canary

published-npm-readonly-canary:
	cd npm/local-host-agent && PUBLISHED_NPM_VERSION=$(VERSION) npm run test:published-readonly-canary

local-npm-readonly-canary: build-agent
	mkdir -p $(DIST)/npm-package
	cd npm/local-host-agent && npm pack --pack-destination $(CURDIR)/$(DIST)/npm-package
	cd npm/local-host-agent && PUBLISHED_NPM_PACKAGE=$(CURDIR)/$(DIST)/npm-package/opute-host-agent-$(VERSION).tgz OPUTE_HOST_AGENT_BINARY=$(CURDIR)/$(DIST)/$(BINARY) RUN_PUBLISHED_READONLY_NPM_CANARY=true node --test published-readonly-canary.test.js

check-site: local-npm-readonly-canary
	node site/scripts/capture-catalog.mjs
	bun run site/scripts/generate-docs.ts
	python3 scripts/check_site_release_boundary.py
	python3 scripts/check_site_release_parity.py
	python3 scripts/test_validate_generated_site.py
	python3 scripts/test_check_generated_site_clean.py
	python3 scripts/validate-generated-site.py
	python3 scripts/check_generated_site_clean.py

artifacts: build-linux-x64 build-linux-arm64 build-windows-x64 build-provider-linux-x64 build-provider-cloudflare-linux-x64 build-provider-tailscale-linux-x64 checksums

build-linux-x64:
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-linux-x64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-x64

build-linux-arm64:
	mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-linux-arm64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-arm64

build-windows-x64:
	mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-windows-x64.exe ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-windows-x64.exe
	mv -f $(DIST)/host-agent-windows-x64.exe.gz $(DIST)/host-agent-windows-x64.gz

build-provider-linux-x64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C plugins/kubernetes/k3s build -a -ldflags='-s -w' -o $(CURDIR)/$(DIST)/opute-provider-k3s-linux-x64 ./cmd/opute-provider-k3s

build-provider-cloudflare-linux-x64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C plugins/tunneling/cloudflare build -a -ldflags='-s -w' -o $(CURDIR)/$(DIST)/opute-provider-cloudflare-linux-x64 ./cmd/opute-provider-cloudflare

build-provider-tailscale-linux-x64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C plugins/tunneling/tailscale build -a -ldflags='-s -w' -o $(CURDIR)/$(DIST)/opute-provider-tailscale-linux-x64 ./cmd/opute-provider-tailscale

checksums:
	(cd $(DIST) && sha256sum host-agent-linux-x64.gz host-agent-linux-arm64.gz host-agent-windows-x64.gz opute-provider-k3s-linux-x64 opute-provider-cloudflare-linux-x64 opute-provider-tailscale-linux-x64 > SHA256SUMS)

clean:
	rm -rf $(DIST)

export-schemas:
	cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas

# The shared adapter lives in the TypeScript control-plane repo so both
# repositories use one Beads database and one metadata convention. The
# launcher is intentionally kept in the control-plane checkout; this repo
# does not carry a second copy of the coordination script.
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
	OPUTE_BUN_PATH="$(BUN)" "$(OPUTE_ROOT)/scripts/agent-work" $(ARGS)
