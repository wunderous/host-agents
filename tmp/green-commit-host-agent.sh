#!/usr/bin/env bash
set -euo pipefail
export GIT_AUTHOR_NAME='Wundersmiths'
export GIT_AUTHOR_EMAIL='houman.kamali-github@outlook.com'
export GIT_COMMITTER_NAME='Wundersmiths'
export GIT_COMMITTER_EMAIL='houman.kamali-github@outlook.com'
export PATH="/home/opute/.local/go-toolchain/go/bin:/home/opute/.bun/bin:/usr/bin:/bin"
cd /mnt/c/Users/houma/code/opute-host-agent
cat > /tmp/host-agent-commit-msg.txt <<'EOF'
fix(host-agent): remove LiteRT and align Ollama inventory with tunnel catalog

Drop LiteRT LM operations from the Go agent, register helm/artifact standalone tools, and keep Incus inventory and Ollama GPU unit behavior aligned with dogfood tunnel execution.
EOF
/usr/bin/git commit -F /tmp/host-agent-commit-msg.txt
echo HOST_AGENT_COMMIT_OK
