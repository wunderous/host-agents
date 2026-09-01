# Host Agent command catalog

This is the command inventory and live-invocation plan for the Go Host Agent.
It deliberately covers both checked-in command surfaces:

| Surface | Source of truth | Count | Meaning |
| --- | --- | ---: | --- |
| Incus/platform export | `schemas/all-tools.json` | 105 | Platform/tunnel export, including commands that are internal or omitted from the agent-facing catalog. |
| Standalone HTTP | `schemas/standalone-tools.json` | 107 | Direct `host-agent` Streamable HTTP contract. |

The two lists overlap, but are not interchangeable. The dispatch contract is
checked independently by `internal/contract/toolname`; the standalone contract
is checked by `ValidateStandaloneToolContract`. The checked-in schemas are the
authoritative names and input shapes. This document is an operational index,
not a second schema.

## Invocation rules

* CI runs deterministic contract, transport, and packaged-HTTP tests. It does
  not mutate a shared Incus host, install a local model, or shut down WSL.
* Live mutation checks use a unique `opute-command-catalog-<run>` resource name,
  the smallest supported profile, and a `defer`/cleanup step immediately after
  creation. Existing resources are never selected by discovery alone.
* The VM/container lifecycle chain is `create_vm` or `provision_vm` ->
  `list_vms` -> `get_vm_info` -> `start_vm`/`stop_vm`/`restart_vm` ->
  `update_vm_resources` -> `delete_vm`.
* Kubernetes, PostgreSQL, OCI, service-domain, storage, relay, and console
  commands are only invoked after their target URI or operation/session ID is
  returned by the preceding command in the same chain.
* `shutdown_wsl`, `terminate_wsl_distribution`, `reset_incus_stack`, generic
  shell/command execution, and host-service state changes are guarded. They
  cannot be part of an unattended shared-host CI lane; they require an
  explicitly isolated host and a destructive-test authorization.
* Local-LLM commands test the local runtime boundary only. They are not used
  to validate product chat. Product LLM tests use the configured
  `OPENROUTER_API_KEY` and the `ibm-granite/granite-4.1-8b` model.

## Chain matrix

| Chain | Minimal fixture | Commands covered | Cleanup |
| --- | --- | --- | --- |
| Host and inventory | None | `detect_host_platform`, `get_host_info`, `get_host_capacity`, `list_agents`, `list_vms`, `list_operations`, `get_capability_catalog`, `open_assistant_session` | None |
| VM lifecycle | 2 vCPU, 2 GiB container; unique name; omit disk on an ext4 `dir` pool | `create_vm`, `provision_vm`, `get_vm_info`, `start_vm`, `stop_vm`, `restart_vm`, `update_vm_resources`, `delete_vm` | Delete the unique VM and verify it is absent |
| SQLite | Caller-scoped database name | `ensure_sqlite_database`, `get_sqlite_database_status`, `remove_sqlite_database` | Remove with `confirm: true` |
| Kubernetes resource | Existing disposable K3s/cluster URI | `list_namespaces`, `list_ingress_classes`, `list_pods`, `list_services`, `list_deployments`, `list_storage_classes`, `list_k8s_events`, `get_k8s_resource`, `get_k8s_resource_status`, `apply_manifest`, `put_k8s_secret`, `delete_k8s_resource` | Delete only resources carrying the run name; never delete the cluster |
| PostgreSQL | Existing disposable cluster; minimum supported storage | `reconcile_postgresql_service`, `get_postgresql_service_status`, `remove_postgresql_service` | PASS live on a unique namespace/cluster; remove completed successfully |
| OCI registry/build | Existing disposable K3s/VM and a tiny image context | `ensure_oci_builder`, `configure_oci_storage`, `inspect_container_storage`, `install_oci_registry`, `get_oci_registry_status`, `delete_oci_registry`, `cleanup_container_storage` | PASS live for builder/storage/registry; build-context staging and image push remain unverified because no disposable context/push endpoint was needed |
| Service/domain/storage | Platform/provider-owned schemas plus an optional disposable service fixture | `create_service_storage`, `get_service_storage`, `update_service_storage`, `backup_service_storage`, `restore_service_storage`, `delete_service_storage`, `get_service_domain_binding`, `sync_service_domain_binding`, `upsert_service_domain_binding`, `delete_service_domain_binding` | Not Host Agent-callable; direct catalog omits these families by policy. Require platform/provider fixture and credentials; unverified here |
| Host files/services | User-owned temporary file and a validated disposable service | `ensure_host_file`, `inspect_host_file`, `remove_host_file`, `list_host_services`, `inspect_host_service`, `ensure_host_service_supervisor` | Remove only the run file; service state is restored |
| Console | Existing disposable container and console operation | `stream_vm_console`, `send_console_input`, `resize_console` | PASS live; the PTY closed after control-D and no `incus console` process remained |
| Local LLM | Existing shared runtime; no model install in CI | `check_local_llm_prerequisites`, `get_local_status`, `list_local_llm_models`, `probe_local_llm`, `configure_local_llm_runtime`, `start_local_llm_runtime`, `stop_local_llm_runtime` | Do not change shared model/runtime state |

## Incus/platform export: all 105 names

Required inputs are kept in `schemas/all-tools.json`; names below are listed
verbatim from that file so schema drift is visible in review.

```text
agent_shell
apply_manifest
backup_service_storage
build_and_push_oci_image
cleanup_container_storage
configure_host_network
configure_network
configure_oci_storage
configure_service_domain
create_host_onboarding_session
create_postgresql_database
create_service_storage
delete_cloudflared_connector
delete_postgresql_database
delete_service_domain_binding
delete_service_storage
delete_oci_registry
delete_vm
detect_host_platform
terminate_wsl_distribution
shutdown_wsl
diagnose_bridge
ensure_docker
ensure_host_firewall_rule
ensure_host_service_supervisor
ensure_host_tool
ensure_k3d
ensure_oci_builder
ensure_sql_connector
ensure_sqlite_database
exec_command
exec_kubernetes_command
get_host_info
get_host_onboarding_session
get_k8s_resource
delete_k8s_resource
list_k8s_events
get_k8s_resource_status
get_oci_registry_status
get_operation
get_postgresql_database
get_postgresql_service_status
get_service_domain_binding
get_service_storage
get_sql_connector_status
get_sqlite_database_status
get_vm_info
inspect_container_storage
inspect_workload
install_cloudflared_connector
install_helm_chart
install_oci_registry
install_provider_tools
install_sql_forward_sidecar
list_agents
list_certificate_issuers
list_configmap_keys
list_deployments
list_ingress_classes
list_kubernetes_clusters
list_namespaces
list_operations
list_pods
list_postgresql_databases
list_secret_keys
list_service_domain_bindings
list_service_storages
list_services
list_storage_classes
list_vm_network_devices
list_vms
probe_host_exposure
provision_vm
put_k8s_secret
reconcile_postgresql_service
recover_bridge
register_kubernetes_cluster
release_postgresql_service_relay
release_sql_connector
remove_host_exposure
remove_postgresql_service
remove_service_domain
remove_sqlite_database
remove_vm_network_device
reset_incus_stack
resize_console
resize_postgresql_database
restart_host_service
restart_vm
restore_service_storage
send_console_input
set_host_service_state
stage_build_context
start_vm
stop_vm
stream_vm_console
sync_service_domain_binding
uninstall_helm_chart
uninstall_provider_tools
update_service_storage
update_vm_resources
upsert_service_domain_binding
get_host_capacity
reconcile_host_resource_policy
```

The export intentionally contains names that are not agent-facing. In
particular, `agent_shell`, `exec_command`, `exec_kubernetes_command`, and
operation/relay helpers must not be interpreted as a promise that a public
client may call them. `internal/tools/catalog.go` is the authority for the
filtered Incus catalog and its omitted/provider-owned partitions.

The direct service also advertises these provider capability operations at
runtime; they are not duplicated in either checked-in 107-entry schema:

```text
opute.capability.kubernetes.apply-manifest
opute.capability.kubernetes.configure-registry
opute.capability.kubernetes.delete-resource
opute.capability.kubernetes.exec-command
opute.capability.kubernetes.get-cluster-info
opute.capability.kubernetes.get-resource
opute.capability.kubernetes.get-resource-status
opute.capability.kubernetes.list-clusters
opute.capability.kubernetes.list-events
opute.capability.kubernetes.provision
opute.capability.kubernetes.put-secret
opute.capability.kubernetes.remove
opute.capability.kubernetes.restart
opute.capability.kubernetes.status
opute.capability.kubernetes.validate
```

These provider operations are catalogued separately because their lifecycle
and schemas are owned by the provider capability layer rather than the legacy
tool-name registries.

## Standalone HTTP: all 107 names

Each row below includes the standalone classification and support level from
`schemas/standalone-tools.json`.

| Name | Classification | Support |
| --- | --- | --- |
| `run_host_command` | mutation | experimental |
| `request_task_input` | read_only | experimental |
| `probe_openai_compatible_server` | read_only | experimental |
| `detect_host_platform` | read_only | experimental |
| `probe_http_endpoint` | read_only | experimental |
| `ensure_host_file` | mutation | experimental |
| `remove_host_file` | destructive | experimental |
| `inspect_host_file` | read_only | experimental |
| `reconcile_serving_assignment` | mutation | experimental |
| `get_host_info` | read_only | stable |
| `check_local_prerequisites` | read_only | stable |
| `get_local_status` | read_only | stable |
| `check_local_llm_prerequisites` | read_only | experimental |
| `ensure_local_llm_server_binary` | mutation | experimental |
| `list_local_llm_models` | read_only | experimental |
| `probe_local_llm` | read_only | experimental |
| `install_local_llm_model` | mutation | experimental |
| `configure_local_llm_model` | mutation | experimental |
| `start_local_llm_runtime` | mutation | experimental |
| `configure_local_llm_runtime` | mutation | experimental |
| `stop_local_llm_runtime` | mutation | experimental |
| `remove_local_llm_model` | destructive | experimental |
| `ensure_local_llm_relay` | credential_bearing | experimental |
| `remove_local_llm_relay` | destructive | experimental |
| `ensure_local_llm_k3s_proxy` | credential_bearing | legacy |
| `remove_local_llm_k3s_proxy` | destructive | legacy |
| `list_operations` | read_only | stable |
| `get_operation` | read_only | stable |
| `cancel_operation` | mutation | stable |
| `validate_host_plan` | read_only | experimental |
| `run_host_plan` | mutation | experimental |
| `get_host_plan_run` | read_only | experimental |
| `validate_runtime_recipe` | read_only | experimental |
| `run_runtime_recipe` | mutation | experimental |
| `get_runtime_recipe_run` | read_only | experimental |
| `validate_tunnel_recipe` | read_only | experimental |
| `run_tunnel_recipe` | mutation | experimental |
| `get_tunnel_run` | read_only | experimental |
| `opute.provider.install` | mutation | experimental |
| `opute.provider.validate` | read_only | experimental |
| `opute.provider.status` | read_only | stable |
| `opute.provider.reload` | mutation | experimental |
| `opute.provider.teardown` | destructive | experimental |
| `ensure_host_artifact` | mutation | experimental |
| `extract_host_archive` | mutation | experimental |
| `inspect_host_service` | read_only | experimental |
| `list_host_services` | read_only | experimental |
| `get_capability_catalog` | read_only | stable |
| `open_assistant_session` | read_only | stable |
| `list_vms` | read_only | stable |
| `get_vm_info` | read_only | stable |
| `create_vm` | mutation | stable |
| `provision_vm` | mutation | stable |
| `start_vm` | mutation | stable |
| `stop_vm` | mutation | stable |
| `restart_vm` | mutation | stable |
| `update_vm_resources` | mutation | experimental |
| `delete_vm` | destructive | stable |
| `list_namespaces` | read_only | experimental |
| `list_ingress_classes` | read_only | experimental |
| `list_pods` | read_only | experimental |
| `list_services` | read_only | experimental |
| `install_postgresql` | mutation | experimental |
| `get_postgresql_status` | read_only | experimental |
| `delete_postgresql` | destructive | experimental |
| `run_sql` | mutation | experimental |
| `apply_manifest` | credential_bearing | experimental |
| `put_k8s_secret` | credential_bearing | experimental |
| `get_k8s_resource` | read_only | experimental |
| `delete_k8s_resource` | destructive | experimental |
| `get_k8s_resource_status` | read_only | experimental |
| `list_k8s_events` | read_only | experimental |
| `install_oci_registry` | mutation | experimental |
| `get_oci_registry_status` | read_only | experimental |
| `delete_oci_registry` | destructive | experimental |
| `configure_service_domain` | mutation | experimental |
| `remove_service_domain` | destructive | experimental |
| `install_cloudflared_connector` | credential_bearing | experimental |
| `ensure_oci_builder` | mutation | experimental |
| `configure_oci_storage` | mutation | experimental |
| `inspect_container_storage` | read_only | experimental |
| `cleanup_container_storage` | mutation | experimental |
| `build_and_push_oci_image` | mutation | experimental |
| `stage_build_context` | mutation | experimental |
| `ensure_host_tool` | mutation | experimental |
| `render_helm_template` | read_only | experimental |
| `prepare_host_agent_artifacts` | mutation | experimental |
| `restart_host_service` | mutation | experimental |
| `set_host_service_state` | mutation | experimental |
| `ensure_host_service_supervisor` | mutation | experimental |
| `delete_cloudflared_connector` | destructive | experimental |
| `install_incus_stack` | mutation | experimental |
| `reset_incus_stack` | destructive | experimental |
| `probe_incus_gpu` | read_only | experimental |
| `provision_container` | mutation | experimental |
| `probe_gpu_container` | read_only | experimental |
| `configure_agent_connection` | mutation | experimental |
| `discover_service_ingress` | read_only | experimental |
| `ensure_sqlite_database` | mutation | stable |
| `get_sqlite_database_status` | read_only | stable |
| `remove_sqlite_database` | destructive | stable |
| `reconcile_postgresql_service` | mutation | experimental |
| `get_postgresql_service_status` | read_only | experimental |
| `remove_postgresql_service` | destructive | experimental |
| `release_postgresql_service_relay` | mutation | experimental |
| `get_host_capacity` | read_only | stable |
| `reconcile_host_resource_policy` | mutation | experimental |

## Audit status

| Check | Status | Evidence/next action |
| --- | --- | --- |
| Schema names and counts | PASS | The checked-in Incus export contains 105 callable/owned entries and the standalone contract contains 107 entries; this file records both surfaces. |
| Dispatch/contract coverage | PASS | `go test ./...` passes, including registry parity and standalone catalog validation. |
| Packaged standalone HTTP smoke | PASS | CI builds the release binary and runs `go test ./test/standalone`. |
| OpenRouter Granite smoke | PASS when secret is available | CI passes `OPENROUTER_API_KEY` to `make openrouter-llm-smoke`; missing secrets skip that opt-in test. |
| Public `/chat` Granite model search and response | PASS | Verified separately with the live browser path using `ibm-granite/granite-4.1-8b`; see the task evidence, not this schema catalog. |
| Safe read-only command sweep | PASS for supported direct commands | Direct host inventory, VM, SQLite, host, HTTP, GPU/storage, OpenRouter, local-LLM, capability/session, host-service, and Kubernetes reads passed on 2026-08-31. Platform-only inventory and profile-specific commands remain route/profile scoped. |
| Resource-backed lifecycle chains | PARTIAL, with boundaries documented | Minimal VM, SQLite, Kubernetes ConfigMap/Secret, host-file, PostgreSQL service, OCI builder/storage/registry, and console chains passed and cleaned on 2026-08-31. OCI context/push remains unverified; service-storage/domain families are platform/provider-owned and require a separate fixture/credential path. |
| Shared-host destructive commands | Guarded | Do not run from GitHub CI or against the shared WSL host; require an isolated host authorization. |

## Live evidence — 2026-08-31

The direct Host Agent was invoked through its authenticated Streamable HTTP MCP
endpoint, using the existing authorized host and disposable cluster. No token
or API key is written to this catalog.

* Passed read-only calls: `detect_host_platform`, `get_host_info`,
  `get_host_capacity`, `list_vms`, `list_kubernetes_clusters`,
  `get_capability_catalog`, `list_host_services`, `probe_http_endpoint`,
  `probe_incus_gpu`, `inspect_container_storage`,
  `check_local_llm_prerequisites`, and
  `probe_openai_compatible_server` against OpenRouter Granite 4.1 8B.
* Passed SQLite chain: `ensure_sqlite_database` ->
  `get_sqlite_database_status` -> `remove_sqlite_database`; the caller-scoped
  database was removed successfully.
* Passed VM chain: `provision_vm` (2 vCPU, 2 GiB container, no explicit disk)
  -> `list_vms` -> `get_vm_info` -> `stop_vm` -> `start_vm` -> `restart_vm`
  -> `update_vm_resources` -> `delete_vm`; the unique container was removed
  successfully.
* Passed Kubernetes resource chain on `cluster:local:opute-clean-k3s`:
  `apply_manifest` -> `put_k8s_secret` -> `get_k8s_resource` ->
  `get_k8s_resource_status` -> `delete_k8s_resource` for unique run-scoped
  ConfigMap and Secret names; both resources were removed.
* Passed host-file chain beneath the current user's home:
  `ensure_host_file` -> `inspect_host_file` -> `remove_host_file`; the unique
  file was removed.
* Passed PostgreSQL service chain on `cluster:local:opute-clean-k3s` with a
  unique namespace and one-database cluster: `reconcile_postgresql_service` ->
  `get_postgresql_service_status` -> `remove_postgresql_service`. The result
  included operator/cluster/primary/service/secret/SQL readiness evidence, and
  the namespace and reservation were cleaned.
* Passed OCI chain on the same disposable cluster: `ensure_oci_builder` ->
  `configure_oci_storage` -> `install_oci_registry` ->
  `get_oci_registry_status` -> `delete_oci_registry`. The builder was already
  available; storage inspection and a dry-run age-gated cleanup also passed.
  `stage_build_context` and `build_and_push_oci_image` remain unverified so
  this audit does not claim a registry push without a disposable context and
  routable endpoint.
* Passed console chain against the existing disposable container:
  `stream_vm_console` -> `resize_console` -> `send_console_input` with a
  control-D close. The operation returned a canonical-bound provider instance,
  and no console process remained afterward.
* Service-storage and service-domain schemas remain compatibility exports, not
  direct Host Agent capabilities. `internal/tools/catalog.go` omits those
  families, and the live direct `tools/list` surface returned no matching
  names. Their validation belongs to the platform/provider fixture path and is
  explicitly unverified in this Host Agent audit; the direct
  `configure_service_domain`/`remove_service_domain` handlers are likewise not
  advertised by this profile.
* Passed control reads: `get_capability_catalog`,
  `open_assistant_session`, `list_host_services`, and `inspect_host_service`.
  The local-model reads `list_local_llm_models` and `probe_local_llm` also
  completed against the shared runtime without changing it.
* The VM chain initially exposed two defects: explicit disk sizing is not
  enforceable on the host's ext4 `dir` pool, and `start_vm` rejected the
  container resource kind. The test fixture now omits disk sizing on that pool
  and the `start_vm` capability declares both VM and container resource kinds.
* Kubernetes `list_namespaces`, `list_ingress_classes`,
  `list_storage_classes`, and `list_deployments` initially failed output-schema
  validation because their handlers omitted the bound cluster URI. Their
  handlers now return the same URI binding as the already-correct pod,
  service, and event handlers; the service must be rebuilt and the reads
  rerun before this row can become PASS.
* `list_agents` is platform inventory, not a direct Host Agent command. The
  direct endpoint correctly reports it as unavailable; invoke it through the
  platform MCP route when testing platform inventory.

The live platform-mode Host Agent advertised 130 tools after the stale export
removal and typed OCI/console additions, including the 15 provider capability
operations listed above. The
standalone profile has a separate 107-entry contract and exposes additional
profile-only lifecycle/local-runtime commands; do not use the platform-mode
tool list as proof that every standalone-only command is available in that
profile.

The following four stale names were removed from the Incus export during this
audit because they had no dispatch implementation and could not be invoked:
`list_persistent_volume_claims`, `list_configmaps`,
`list_kubernetes_secrets`, and `list_service_ports`. They remain historical
names in the audit record, not passing commands or compatibility aliases.

When a command is added or removed, update the owning schema/contract first,
regenerate any derived export, run the contract suite, and update this file's
counts and audit row. Do not add a compatibility alias merely to make this
index appear complete.
