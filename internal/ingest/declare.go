package ingest

import (
	"maps"
	"slices"

	"github.com/ginsys/bronzeward/internal/provider"
)

// Declarations is what a fragment revision carries beside its YAML (compilation.md §5.2): a
// declaration for every name it references, and the embedded documents it identifies.
type Declarations struct {
	References map[string]Reference `json:"references,omitempty" yaml:"references,omitempty"`
	Embedded   []Embedded           `json:"embedded,omitempty" yaml:"embedded,omitempty"`
}

// Reference declares one logical secret name: its stored kind, the exact version it resolves to,
// and an optional placement encoding.
type Reference struct {
	Kind     provider.Kind `json:"kind" yaml:"kind"`
	Version  int64         `json:"version" yaml:"version"`
	Encoding string        `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

// Embedded identifies an embedded document: the path of the string scalar that holds it and its
// format, yaml or json.
type Embedded struct {
	Path   string `json:"path" yaml:"path"`
	Format string `json:"format" yaml:"format"`
}

func (d Declarations) clone() Declarations {
	return Declarations{References: maps.Clone(d.References), Embedded: slices.Clone(d.Embedded)}
}
