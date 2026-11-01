# One process per Host Agent instance state

## Why

`state.Open` and `authz.Open` open SQLite in WAL mode with a busy timeout, so
a second agent started on the same instance waits for the write lock and then
writes. It is never refused. The host-wide `admission.lock` and
`reservations.lock` are shared by co-resident agents on purpose, so they
cannot refuse one. Two units (the user unit and the bootstrap unit), an
operator's manual start, or a Go → Rust → Go cutover can therefore run two
writers on one instance state. Each sees only its own in-memory tasks, and a
later restart sees both agents' rows.

The owners decided to refuse the second process, in this repository first
(decision D25, recorded in `wunderous/opute-host-agent`
`openspec/changes/state-directory-writer-lock`, with the details confirmed
on 2026-11-01).

## What Changes

- New package `internal/instancelock`: an exclusive, non-blocking lock on
  `host-agent.lock` (created `0600`, never deleted) in each locked root,
  with `flock(LOCK_EX|LOCK_NB)` on Unix and
  `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY)` on Windows.
  The kernel releases it when the process exits, so a crash leaves no stale
  lock.
- `app.NewRuntime` takes the lock after `validateConfig` and before
  `buildHostRuntime`, so before any store is opened or a port is bound, and
  `Runtime.Close` releases it. It locks the instance root, then
  `OPUTE_STANDALONE_STATE_DIR` and `OPUTE_HOST_AGENT_SQLITE_ROOT` when they
  resolve outside it, in that fixed order.
- `app.Check` takes the same lock: it opens the state store, which writes.
- A refused process exits 1 through the existing error path with one stderr
  line: `host agent instance is already in use: <absolute lock path> is
  locked by another process`.
- Because they build a `Runtime`, the in-process `public-mcp`, `recipe` and
  `provider` commands are refused the same way while an agent holds the
  instance.

Unchanged: systemd units (a refused unit fails and `Restart=on-failure`
retries until the holder stops), every flag and default, the wire, and the
host-wide coordination locks. No blocking wait, timeout or takeover flag.
Network filesystems are out of scope.

## Impact

- Tests that ran several agents on the shared default instance root now give
  each agent its own `OPUTE_HOST_AGENT_INSTANCE_ROOT`.
- `TestPackagedSingleBinaryServerOnly` ran `--check` next to the running
  server on the same instance; it now requires the refusal there and success
  on an instance of its own.
- `wunderous/opute-host-agent` follows this release through its parity rebase
  (its tasks 3.1–4.4); until then it keeps the pinned behaviour.
