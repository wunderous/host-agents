package standalone_test

import "time"

// standaloneReadyTimeout is how long a freshly started agent has to answer its
// first request.
//
// It was 15 seconds in three separate tests, which is ample when a test runs
// alone -- the agent is listening in about two -- and not ample at all when
// `go test ./...` runs this package beside every other one on the same machine.
// The two E2E tests here failed only in the full suite and passed in isolation,
// which is the shape of a deadline that is really measuring contention rather
// than the thing it names. A longer one costs nothing on the passing path: the
// wait ends when the agent answers, not when the timer does.
const standaloneReadyTimeout = 90 * time.Second

// standaloneProcessTimeout bounds the child process. It has to outlast the
// readiness wait plus the test body, or the context kills the agent while the
// test is still waiting for it and the failure names the wrong cause.
const standaloneProcessTimeout = 3 * time.Minute
