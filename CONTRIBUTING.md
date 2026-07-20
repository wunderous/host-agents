# Contributing

Contributions are welcome. Please open an issue before substantial changes so the standalone MCP contract and Opute platform profile can be reviewed separately.

Before opening a pull request:

- Run `gofmt -w` on changed Go files.
- Run `go vet ./...` and `go test ./...`.
- Do not include credentials, tunnel tokens, hostnames, or user infrastructure state in fixtures or logs.
- For tool/schema changes, update the standalone catalog and documentation and add an end-to-end validation path.
- Preserve the boundary: standalone mode must not require Opute Platform, Bridge, onboarding tokens, or reverse tunnels.

Pull requests should explain the user-visible MCP contract change, security implications, cleanup behavior, and how the change was validated.
