// Package capability contains neutral capability result contracts shared by
// the Host Agent and independently built provider descriptors.
package capability

import "strings"

// Cardinality describes how many values a selector may expose.
type Cardinality string

const (
	CardinalityOne  Cardinality = "one"
	CardinalityMany Cardinality = "many"
)

// ResultSelector is declarative result metadata. It is intentionally free of
// provider, transport, execution, and UI concepts.
type ResultSelector struct {
	ID          string      `json:"id" yaml:"id"`
	SourcePath  string      `json:"sourcePath" yaml:"sourcePath"`
	Cardinality Cardinality `json:"cardinality" yaml:"cardinality"`
	LabelPath   string      `json:"labelPath,omitempty" yaml:"labelPath,omitempty"`
}

// ResultType describes a reusable output shape and the values that may be
// selected from it. Selectors are scoped to the result type, not a tool name.
type ResultType struct {
	ID        string           `json:"id" yaml:"id"`
	Version   int              `json:"version" yaml:"version"`
	Selectors []ResultSelector `json:"selectors,omitempty" yaml:"selectors,omitempty"`
}

func (s ResultSelector) NormalizedCardinality() Cardinality {
	if strings.TrimSpace(string(s.Cardinality)) == "" {
		return CardinalityOne
	}
	return s.Cardinality
}
