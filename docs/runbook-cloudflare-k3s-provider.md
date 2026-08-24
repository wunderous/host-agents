# Cloudflare and K3s Provider Runbook

## Provider startup

Start the providers on disposable local ports and expose only Streamable HTTP
MCP endpoints:

```bash
go run ./plugins/kubernetes/k3s/cmd/opute-provider-k3s
CLOUDFLARE_ACCOUNT_ID=... CLOUDFLARE_ZONE_ID=... \
CLOUDFLARE_API_TOKEN=... OPUTE_PROVIDER_PORT=4319 \
OPUTE_HOST_AGENT_ENDPOINT=http://127.0.0.1:3014/mcp \
go run ./plugins/tunneling/cloudflare/cmd/opute-provider-cloudflare
```

The Host Agent must connect using trusted local descriptors. Verify each
provider with MCP `initialize`, `tools/list`, and a typed `tools/call` before
installing or activating a generation.

In a reverse-tunnel deployment, `OPUTE_HOST_AGENT_ENDPOINT` must point to the
authorized Host Agent MCP endpoint for the owning instance. A health-only
reverse-tunnel listener is not a provider callback endpoint. Inject
credentials at process launch; never place them in a recipe, plan, or provider
result.

## Target requirements

Kubernetes operations invoked through Host Agent require a registered,
tenant-scoped URI such as `cluster:local:<id>`. Display names, `vmName`, and
same-name fallback targets are invalid. Host Agent resolves the URI and passes
the provider-native Incus instance coordinate only after authorization.

For a system container, use `container:<tenant>:<id>` for instance commands;
never replace it with a `vm:` URI. The K3s provider accepts only resolved VM or
container instance types.

## Credentials and redaction

Inject Cloudflare credentials into the provider process environment or the
supported secret reference. Do not place API tokens in plans, provider output,
logs, MCP evidence, or shell history. Local ignored credential files must be
owner-readable only:

```bash
chmod 600 /path/to/.env.cloudflare.local /path/to/platform-opute-tunnel-token.txt
```

## Cleanup and rollback

Provider teardown is two-phase:

1. Host Agent prepares and runs the generic stop/disable/file or container
   cleanup plan.
2. Cloudflare finalizes tunnel and DNS deletion through the provider API.

If finalization fails, leave the generation retryable and rerun the same
teardown with the original external IDs. Do not report the generation fully
removed until API and external DNS inventory both confirm deletion.

The live validator must track secrets, provider connections, exposure IDs,
tunnel IDs, and DNS record IDs and execute best-effort cleanup in `finally`,
while preserving the original failure.

For an explicit Go-provider finalizer check, set
`OPUTE_CLOUDFLARE_PROVIDER_FINALIZER_URL` to the provider MCP endpoint. The
validator passes the observed tunnel and DNS record IDs to the provider before
the product binding cleanup. Verify Cloudflare inventory with
`is_deleted=false/true` and verify the DNS record lookup is absent. A failed
finalization must be retried with the same IDs; the generation remains
retryable until that retry succeeds.

Cloudflare DELETE responses must be checked for both HTTP success and the JSON
API `success` field. Cloudflare can return HTTP 200 with `success: false` when
connections are still active; treat that as a retryable finalization failure.
