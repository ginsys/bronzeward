package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/talos"
)

// Unresolved is input as read, before extraction (compilation.md §2.1): a configuration read
// back from a node or a draft update's text. No sink accepts it. Every fmt verb prints a
// placeholder and the marshallers fail. The text sits behind a pointer to a string: fmt prints a
// struct holding an Unresolved in an unexported field by reflection, past those methods, and
// under a verb a pointer does not take (%s, %q) it dereferences a pointer to a slice, array,
// struct or map, but never one to a string. Only this package's parser reads it.
type Unresolved struct{ s *string }

// ErrEmptyInput refuses an input with no bytes.
var ErrEmptyInput = errors.New("ingest: the input is empty")

// Read reads r whole into an Unresolved, refusing more than max bytes. Errors never quote the
// input.
func Read(r io.Reader, max int64) (Unresolved, error) {
	if max <= 0 {
		return Unresolved{}, errors.New("ingest: a positive input limit is required")
	}
	// One byte past the limit shows an input over it; the largest limit has no byte past it.
	b, err := io.ReadAll(io.LimitReader(r, max+min(1, math.MaxInt64-max)))
	if err != nil {
		return Unresolved{}, errors.New("ingest: the input could not be read")
	}
	if int64(len(b)) > max {
		return Unresolved{}, fmt.Errorf("ingest: the input is over %d bytes", max)
	}
	if len(b) == 0 {
		return Unresolved{}, ErrEmptyInput
	}
	s := string(b)
	return Unresolved{s: &s}, nil
}

// FromTalos is a machine configuration read from a node, as input.
func FromTalos(c talos.Config) Unresolved {
	s := string(c.Bytes())
	if s == "" {
		return Unresolved{}
	}
	return Unresolved{s: &s}
}

// MaxDocument is the largest document a request body carries, in bytes as decoded.
const MaxDocument = 512 << 10

var errDocument = errors.New("ingest: the document must be a non-empty JSON string of valid UTF-8 within the size limit")

// UnmarshalJSON takes a request body's document member: one JSON string, at most MaxDocument
// bytes decoded. encoding/json replaces invalid UTF-8 and a lone surrogate escape with U+FFFD,
// which would make the input differ from what was sent, so both are refused, as is a document
// holding U+FFFD at all. U+0000 is refused here too: PostgreSQL text cannot hold it, and the API
// layer checks only the body's other members. Every refusal is errDocument, which quotes nothing;
// the decoder's own error is never wrapped.
func (u *Unresolved) UnmarshalJSON(b []byte) error {
	*u = Unresolved{}
	var s string
	if !utf8.Valid(b) || len(b) == 0 || b[0] != '"' || json.Unmarshal(b, &s) != nil {
		return errDocument
	}
	if len(s) == 0 || len(s) > MaxDocument || strings.ContainsRune(s, utf8.RuneError) || strings.ContainsRune(s, 0) {
		return errDocument
	}
	u.s = &s
	return nil
}

// Size is the input's length in bytes.
func (u Unresolved) Size() int {
	if u.s == nil {
		return 0
	}
	return len(*u.s)
}

// bytes is a copy of the input, for the parser only.
func (u Unresolved) bytes() []byte {
	if u.s == nil {
		return nil
	}
	return []byte(*u.s)
}

const unresolvedText = "[unresolved input]"

var errUnresolvedRender = errors.New("ingest: unresolved input is not marshalled")

func (Unresolved) String() string               { return unresolvedText }
func (Unresolved) GoString() string             { return unresolvedText }
func (Unresolved) Format(f fmt.State, _ rune)   { io.WriteString(f, unresolvedText) }
func (Unresolved) MarshalJSON() ([]byte, error) { return nil, errUnresolvedRender }
func (Unresolved) MarshalText() ([]byte, error) { return nil, errUnresolvedRender }
func (Unresolved) MarshalYAML() (any, error)    { return nil, errUnresolvedRender }
