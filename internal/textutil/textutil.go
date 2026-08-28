// Package textutil holds the string helpers every domain needs.
//
// They live outside hostruntime on purpose: hostruntime is identity, config,
// and execution handles (S9.2), and a blank-string fallback is none of those.
// They live outside the domains because seven private copies of a four-line
// function is worse than one shared package with no state and no imports.
package textutil

import "strings"

// FirstNonEmpty returns the first value that is not blank, trimmed.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Default substitutes a fallback when a value is blank. Unlike FirstNonEmpty it
// returns the value untrimmed, which several callers rely on.
func Default(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// ErrString renders an error, or a fallback when there is none.
func ErrString(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// ShellQuote wraps a value in single quotes, escaping any it contains, so it
// can be embedded in a shell command without changing its meaning.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
