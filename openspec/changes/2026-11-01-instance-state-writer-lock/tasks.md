## 1. Lock

- [x] 1.1 Add `internal/instancelock` with Unix (`flock`) and Windows (`LockFileEx`) files. Evidence: `go test ./internal/instancelock` (refusal, release, ordered roots, de-duplication, SIGKILLed holder).
- [x] 1.2 Take the lock in `app.NewRuntime` after `validateConfig` and before `buildHostRuntime`, release it in `Runtime.Close`, and take it in `app.Check`. Evidence: `go test ./internal/cli -run 'SecondAgent|StateDirectory|CheckRefuses'`.

## 2. Validation

- [x] 2.1 A second agent refuses with the line and exit 1, writes no store and binds no port; a running first agent keeps serving. Evidence: `internal/cli/instance_lock_test.go`.
- [x] 2.2 Isolate every agent-starting test on its own instance root, and require `--check` to refuse next to a running server. Evidence: `go test ./...` with `HOME` on an empty directory leaves no `host-agent.lock` there.
- [x] 2.3 `gofmt -l`, `go vet ./...` (Linux and Windows), `go test ./...` with go1.25.4.

## 3. Release

- [ ] 3.1 Cut a release through the normal release path after owner approval.
