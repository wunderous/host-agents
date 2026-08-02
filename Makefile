.PHONY: build test standalone-smoke standalone-http-smoke standalone-lifecycle-gate published-npm-canary npm-test artifacts clean agent-work

BINARY=opute-host-agent
DIST=dist
MODULE=github.com/wunderous/host-agents
VERSION ?= 0.1.1
LDFLAGS=-s -w -X $(MODULE)/internal/version.Version=$(VERSION)

build:
	go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/opute-host-agent

test:
	go test ./...

npm-test:
	cd npm/local-host-agent && npm test

standalone-smoke: build
	OPUTE_AGENT_MODE=standalone OPUTE_INFRA_PROVIDER_ID=incus OPUTE_STANDALONE_STATE_DIR="$$(mktemp -d)" $(DIST)/$(BINARY) --check

standalone-http-smoke: build
	OPUTE_STANDALONE_BINARY=$(CURDIR)/$(DIST)/$(BINARY) go test ./test/standalone -count=1

standalone-lifecycle-gate: build-linux-x64
	go test -tags=integration ./test/live -count=1

published-npm-canary:
	cd npm/local-host-agent && PUBLISHED_NPM_VERSION=$(VERSION) npm run test:published-canary

artifacts: build-linux-x64 build-linux-arm64 checksums

build-linux-x64:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/host-agent-linux-x64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-x64

build-linux-arm64:
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
agent-work:
	OPUTE_AGENT_WORK_REPOSITORY=opute-host-agent OPUTE_AGENT_WORK_REPO_ROOT=$(CURDIR) bun --cwd ../opute scripts/agent-work-coordination.ts $(ARGS)
