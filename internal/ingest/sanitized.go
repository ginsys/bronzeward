package ingest

import (
	"bytes"
	"errors"
)

// Sanitized is a document stream with every identified value replaced by a `!bwref` reference,
// and its declarations (compilation.md §2.1, §5.2). It is the only form of input ordinary
// persistence accepts. Its fields are unexported and only newSanitized fills them; every
// persistence function calls Check first, which refuses the zero value.
type Sanitized struct {
	docs []byte
	decl Declarations
}

// ErrZeroSanitized refuses a Sanitized not made by this package.
var ErrZeroSanitized = errors.New("ingest: a sanitized value made by ingestion is required")

func newSanitized(docs []byte, decl Declarations) Sanitized {
	return Sanitized{docs: bytes.Clone(docs), decl: decl.clone()}
}

// Check refuses the zero value.
func (s Sanitized) Check() error {
	if len(s.docs) == 0 {
		return ErrZeroSanitized
	}
	return nil
}

// Documents is the sanitized stream's YAML.
func (s Sanitized) Documents() []byte { return bytes.Clone(s.docs) }

// Declarations is the declaration of every name the stream references.
func (s Sanitized) Declarations() Declarations { return s.decl.clone() }
