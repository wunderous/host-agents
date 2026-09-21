# Host Agent MCP — reference for agents

Prefer live `tools/list`. Grouping below is navigational (178-tool capture order of magnitude).

## Capability groups (examples)

| Group | Example tools |
|-------|----------------|
| Catalog / session | `get_capability_catalog`, `list_agents` |
| Host | `get_host_info`, `run_host_command`, `ensure_host_file` |
| Guests (Incus) | `list_vms`, `create_vm`, `provision_vm`, `start_vm`, `delete_vm` |
| Kubernetes | `apply_manifest`, `list_pods`, `get_k8s_resource`, `opute.capability.kubernetes.*` |
| K3s membership | `opute.capability.kubernetes.provision`, `.join-node`, `.recover-quorum` |
| OCI | `build_and_push_oci_image`, `ensure_oci_builder`, `install_oci_registry` |
| Recipes / plans | `validate_host_local_recipe`, `run_host_local_recipe`, `run_host_plan`, `get_host_plan_run` |
| Tunneling | `opute.capability.tunneling.ensure-host-tunnel`, `.probe-host-tunnel` |
| Providers | `opute.provider.install`, `.status`, `.teardown` |

Dual naming: underscore aliases and dotted `opute.capability.*` may both appear — use the name from the live catalog.

## Cluster URI

Always use the full admitted URI from inventory (e.g. `cluster:local:opute-ha-a`), never a truncated `cluster:local`.

## Env cheat sheet

| Variable | Role |
|----------|------|
| `OPUTE_REMOTE_AGENT_ID` | Required canonical agent id |
| `MCP_AUTH_TOKEN` | Bearer for `/mcp` |
| `HOST_MCP_PORT` / `HOST_MCP_BIND_HOST` | Listen overrides |
| `OPUTE_STANDALONE_ALLOW_MUTATIONS` | Standalone mutation gate |
| `OPUTE_INFRA_PROVIDER_ID` | `incus` today |

## Docs map

| Need | URL |
|------|-----|
| First success | https://www.opute.io/docs/get-started/ |
| Install | https://www.opute.io/docs/install/ |
| Client JSON | https://www.opute.io/docs/mcp-clients/ |
| Capabilities | https://www.opute.io/docs/capabilities/ |
| Recipes why | https://www.opute.io/docs/recipes/ |
| Primitives | https://www.opute.io/docs/recipe-primitives/ |
| Networking | https://www.opute.io/docs/networking/ |
| Safety / redaction | https://www.opute.io/docs/resources/ |
