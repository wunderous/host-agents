// Package fsutil holds filesystem predicates shared across domains.
//
// A contract-style package: no state, no internal imports. It exists because
// host and incus both ask "is this path there?" and neither may import the
// other.
package fsutil

import "os"

// Exists reports whether a path resolves. It deliberately does not distinguish
// a missing file from an unreadable one -- every caller here treats both as
// "cannot use it".
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
