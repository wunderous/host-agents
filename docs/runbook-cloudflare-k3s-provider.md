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
validator first runs Host Agent prepare cleanup, then passes the observed
tunnel and DNS record IDs to the provider finalizer. Verify Cloudflare inventory with
`is_deleted=false/true` and verify the DNS record lookup is absent. A failed
finalization must be retried with the same IDs; the generation remains
retryable until that retry succeeds.

Cloudflare DELETE responses must be checked for both HTTP success and the JSON
API `success` field. Cloudflare can return HTTP 200 with `success: false` when
connections are still active; treat that as a retryable finalization failure.

## Live validation evidence

The disposable validation command is run from the Opute repository with the
credentials loaded from its ignored `.env.cloudflare.local` file:

```bash
OPUTE_CLOUDFLARE_LIVE=1 \
OPUTE_CLOUDFLARE_PROVIDER_FINALIZER_URL=http://127.0.0.1:4319/mcp \
bun scripts/validate-cloudflare-provider-phase.ts --phase mcp --target dev
```

The current connector-enabled run `bf82533b` reached active publication and a
public HTTP 200 probe, then completed prepare, provider finalization, provider
connection deletion, and secret revocation. Exact Cloudflare API queries for
`cf-canary-bf82533b.opute.io` and `opute-canary-bf82533b` returned zero DNS
records and zero non-deleted tunnels. Public recursive DNS may retain a stale
answer during propagation; use the authoritative Cloudflare API result for the
deletion assertion.

Kubernetes validation is likewise Host Agent-only. Use the dynamic
`host__opute.capability.kubernetes.*` tools with
`targetUri=cluster:<tenant>:<id>`, `providerInstanceName`, and the resolved
`instanceType`; do not invoke local `kubectl`. The current disposable run
applied and removed a unique Namespace and ConfigMap through
`com.opute.k3s`, listed the K3s cluster, returned events, and verified the
namespace missing afterward. Cross-tenant, display-name-only, missing-URI, and
container-as-cluster targets were rejected at Host Agent admission.

The provider teardown recovery test is
`internal/hostmcp/provider_teardown_test.go`: it injects one finalization
failure, verifies the active generation remains connected and retryable, then
retries with the same inputs and verifies the generation is stopped only after
successful finalization.

The real active-generation validation on 2026-08-24 used isolated Host Agent
state and unique Cloudflare resources. A managed connector reached
`systemd --user` active state and Cloudflare reported one live connection;
Host Agent `opute.provider.teardown` then completed prepare and finalization,
left the service inactive/disabled with no unit file, reported the provider
generation inactive/disconnected, and authoritative Cloudflare API checks
returned no tunnel and zero DNS records.

The real recovery validation removed provider credentials after activation.
Prepare completed, finalization failed with the credential diagnostic, and
`opute.provider.status` still reported `active=true, connected=true` while the
Cloudflare tunnel and DNS record remained. After restarting the provider with
credentials, the same teardown was resumed; the durable run completed, the
generation became inactive/disconnected, and the exact tunnel/DNS API checks
returned not-found/zero. The provider callback receives the finalization phase
inside its declared `inputs` object, and service cleanup uses a canonical
tenant-scoped `host-service:<tenant>:<scope>/<name>` URI.
