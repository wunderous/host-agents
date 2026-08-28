// Package k8sname validates Kubernetes object identifiers.
//
// It is a contract package, not a domain: pure validation, no state, no
// internal imports. Both kubernetes and oci name namespaces and objects, and
// neither may import the other -- so the shared spelling of "is this a legal
// name" cannot live in either of them.
package k8sname

import (
	"fmt"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// Validate rejects a blank or malformed identifier, naming the field so the
// caller does not have to wrap the error to make it readable.
func Validate(value, field string) error {
	if value = strings.TrimSpace(value); value == "" || !identifier.MatchString(value) {
		return fmt.Errorf("%s contains invalid characters", field)
	}
	return nil
}
