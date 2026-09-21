package schemas

import "embed"

// FS holds embedded tool schema JSON files for the host agent catalog.
//
//go:embed all-tools.json assistant-session.json catalog-meta.json incus-tools.json standalone-tools.json streamable-http-client.json opute-provider-plugin.v1.json opute-provider-install-manifest.v1.json llm-serving.v1.json llm-serving-context-size.v1.json tunneling.v1.json network-overlay.v1.json mesh-membership.v1.json private-mesh.v1.json public-ingress.v1.json
var FS embed.FS
