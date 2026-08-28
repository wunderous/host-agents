//go:build windows

package llm

import "context"

type hostRequestLock struct{}

func newHostRequestLock(string) *hostRequestLock                   { return nil }
func (l *hostRequestLock) acquire(context.Context) (func(), error) { return func() {}, nil }
