.PHONY: build test artifacts clean

BINARY=opute-host-agent
DIST=dist
MODULE=github.com/opute-io/host-agents

build:
	go build -o $(DIST)/$(BINARY) ./cmd/opute-host-agent

test:
	go test ./...

artifacts: build-linux-x64 build-linux-arm64

build-linux-x64:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST)/host-agent-linux-x64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-x64

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST)/host-agent-linux-arm64 ./cmd/opute-host-agent
	gzip -9 -kf $(DIST)/host-agent-linux-arm64

clean:
	rm -rf $(DIST)

export-schemas:
	cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas
