package schemas

import "embed"

// FS holds embedded tool schema JSON files for the host agent catalog.
//
//go:embed all-tools.json catalog-meta.json incus-tools.json standalone-tools.json streamable-http-client.json
var FS embed.FS
