#!/usr/bin/env bash
set -euo pipefail
export GIT_PAGER=cat
cd /home/houman/github/wunderous/opute-host-agent
git add .
echo "=== staged ==="
git diff --cached --name-only
if git diff --cached --name-only | grep -Ei '\.env($|\.)|credentials|\.pem$|id_rsa|\.p12$'; then
  echo "SECRET_PATHS_BLOCKED"
  exit 1
fi
git log -5 --oneline
git commit -m "$(cat <<'EOF'
feat(host): mount provider generations as Cordis plugins

Require at least one provider service and dispose the predecessor only after the replacement generation is mounted so activation stays reversible.

EOF
)"
git status -sb
git push
git log -1 --format='%H %s'
echo "HOST_AGENT_PUSH_PASS"
