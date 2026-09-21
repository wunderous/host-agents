#!/usr/bin/env bash
# Fail-closed e2e validation for production public surfaces under *.opute.io.
set -euo pipefail

HOSTS="${OPUTE_E2E_HOSTS:-www.opute.io,opute.io}"
EXPECT_STATUS="${OPUTE_E2E_EXPECT_STATUS:-200}"
EXPECT_BODY="${OPUTE_E2E_EXPECT_BODY:-}"
NEGATIVE_PATH="${OPUTE_E2E_NEGATIVE_PATH:-/__opute_guard_missing__}"
JSON_OUT="${OPUTE_E2E_JSON_OUT:-}"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
FAILURES=0
RESULT_FILE="$(mktemp)"
echo '[]' > "$RESULT_FILE"

log() { printf '%s\n' "$*" >&2; }

resolve_dns() {
  python3 - "$1" <<'PY'
import socket, sys
host = sys.argv[1]
addrs = sorted({ai[4][0] for ai in socket.getaddrinfo(host, 443)})
if not addrs:
    raise SystemExit("no addresses")
print(" ".join(addrs))
PY
}

record_host() {
  python3 - "$RESULT_FILE" "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" <<'PY'
import json, sys
path, host, dns_ok, tls_ok, http_ok, neg_ok, status, neg_status, dns = sys.argv[1:10]
data = json.loads(open(path).read())
data.append({
  "host": host,
  "dnsOk": dns_ok == "true",
  "tlsOk": tls_ok == "true",
  "httpOk": http_ok == "true",
  "negativeOk": neg_ok == "true",
  "status": status,
  "negativeStatus": neg_status,
  "dns": dns,
})
open(path, "w").write(json.dumps(data))
PY
}

check_host() {
  local host="$1"
  local url="https://${host}/"
  local dns_ok=false tls_ok=false http_ok=false neg_ok=false
  local status="" neg_status="" dns_out="" tls_info=""

  log "== host ${host} =="

  if dns_out="$(resolve_dns "$host" 2>/dev/null)"; then
    dns_ok=true
    log "DNS: ${dns_out}"
  else
    log "DNS: FAIL"
    FAILURES=$((FAILURES+1))
  fi

  if tls_info="$(echo | openssl s_client -servername "$host" -connect "${host}:443" 2>/dev/null | openssl x509 -noout -subject -dates 2>/dev/null)"; then
    if printf '%s\n' "$tls_info" | grep -qiE "opute\.io|${host}"; then
      tls_ok=true
      log "TLS: ok"
      log "$tls_info"
    else
      log "TLS: unexpected subject"
      log "$tls_info"
      FAILURES=$((FAILURES+1))
    fi
  else
    log "TLS: FAIL handshake"
    FAILURES=$((FAILURES+1))
  fi

  local tmp body
  tmp="$(mktemp)"
  status="$(curl -4 -sS -o "$tmp" -w '%{http_code}' --max-time 30 "$url" || true)"
  body="$(head -c 4096 "$tmp" || true)"
  rm -f "$tmp"
  if [[ "$status" == "$EXPECT_STATUS" ]]; then
    http_ok=true
    log "HTTPS GET ${url} -> ${status}"
  else
    log "HTTPS GET ${url} -> ${status} (want ${EXPECT_STATUS})"
    FAILURES=$((FAILURES+1))
  fi
  if [[ -n "$EXPECT_BODY" ]]; then
    if printf '%s' "$body" | grep -EEq "$EXPECT_BODY"; then
      log "body: matched ${EXPECT_BODY}"
    else
      log "body: FAIL regex ${EXPECT_BODY}"
      http_ok=false
      FAILURES=$((FAILURES+1))
    fi
  fi

  local neg_url="https://${host}${NEGATIVE_PATH}"
  neg_status="$(curl -4 -sS -o /dev/null -w '%{http_code}' --max-time 30 "$neg_url" || true)"
  if [[ "$neg_status" != "$EXPECT_STATUS" ]]; then
    neg_ok=true
    log "negative ${neg_url} -> ${neg_status} (ok)"
  else
    log "negative ${neg_url} -> ${neg_status} FAIL silent success"
    FAILURES=$((FAILURES+1))
  fi

  record_host "$host" "$dns_ok" "$tls_ok" "$http_ok" "$neg_ok" "$status" "$neg_status" "$dns_out"
}

IFS=',' read -r -a HOST_ARR <<< "$HOSTS"
for raw in "${HOST_ARR[@]}"; do
  host="$(echo "$raw" | tr -d '[:space:]')"
  [[ -z "$host" ]] && continue
  if [[ "$host" != *.opute.io && "$host" != "opute.io" && "${OPUTE_E2E_ALLOW_NON_OPUTE:-}" != "1" ]]; then
    log "refusing non-*.opute.io host without OPUTE_E2E_ALLOW_NON_OPUTE=1: $host"
    FAILURES=$((FAILURES+1))
    continue
  fi
  check_host "$host"
done

ENDED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SUMMARY="$(python3 - "$RESULT_FILE" "$STARTED_AT" "$ENDED_AT" "$HOSTS" "$EXPECT_STATUS" "$FAILURES" <<'PY'
import json, sys
path, started, ended, hosts, expect, failures = sys.argv[1:7]
print(json.dumps({
  "startedAt": started,
  "endedAt": ended,
  "hosts": hosts,
  "expectStatus": expect,
  "failures": int(failures),
  "results": json.loads(open(path).read()),
}, indent=2))
PY
)"
printf '%s\n' "$SUMMARY"
if [[ -n "$JSON_OUT" ]]; then
  printf '%s\n' "$SUMMARY" > "$JSON_OUT"
  log "wrote ${JSON_OUT}"
fi
rm -f "$RESULT_FILE"
if [[ "$FAILURES" -gt 0 ]]; then
  log "e2e-opute-io-guard FAILED (${FAILURES})"
  exit 1
fi
log "e2e-opute-io-guard PASSED"
exit 0