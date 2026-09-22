// Package secret is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It holds the type contract the whole experiment rests on. Design §7.1 requires that known or
// operator-marked secrets be extracted before any ordinary plaintext persistence. A leak scan can
// only ever say that no exact copy of a secret was on disk at one instant; it cannot say that
// extraction happened first. What can say so is the shape of the program: if the only type that
// carries plaintext cannot reach a persistence function, ordering is a property of the type
// system rather than of a reviewer's attention.
//
// Two rules make that work, and both are load-bearing:
//
//   - Unresolved is the only type holding plaintext, and every way the fmt, encoding/json and
//     log/slog packages can render a value is overridden to emit a digest instead.
//   - Every persistence function takes Sanitized, whose fields are unexported, so the only way to
//     obtain one is through the extraction path that builds it.
package secret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
)

// redactedPrefix is what every rendering of an Unresolved produces. It carries a digest so that
// two renderings can be compared, correlated across a log and a journal, and recognised in a leak
// scan report, without the value itself ever being written.
const redactedPrefix = "bw:redacted:sha256:"

// digestLen is the number of hex characters of the SHA-256 kept in a rendering. Twelve is enough
// to correlate records within one run and far too few to attack the value.
const digestLen = 12

// Unresolved holds plaintext that has not yet been extracted to a secret provider. Its field is
// unexported, and no method returns it except Unsafe.
//
// The zero value renders as a redaction like any other, deliberately: a struct literal that
// forgot to set the field must not be distinguishable in output from one that did, or the absence
// of a secret becomes a signal about the presence of one.
type Unresolved struct {
	plaintext []byte
	digest    string
}

// Compile-time proof that every rendering path is overridden. If a future edit drops one of these
// methods, the build fails here rather than the value appearing in a log at run time.
var (
	_ fmt.Formatter  = Unresolved{}
	_ fmt.Stringer   = Unresolved{}
	_ fmt.GoStringer = Unresolved{}
	_ slog.LogValuer = Unresolved{}
)

// NewUnresolved wraps plaintext. It copies the input: the caller may reuse or zero its buffer, and
// a shared backing array would let the value change behind a digest already recorded.
func NewUnresolved(plaintext []byte) Unresolved {
	buf := make([]byte, len(plaintext))
	copy(buf, plaintext)
	sum := sha256.Sum256(buf)
	return Unresolved{plaintext: buf, digest: hex.EncodeToString(sum[:])}
}

// Digest is the full SHA-256 of the plaintext, hex encoded. It is safe to persist and is what the
// ordering journal records in place of the value.
func (u Unresolved) Digest() string { return u.digest }

// Redacted is the rendering every output path produces.
func (u Unresolved) Redacted() string {
	d := u.digest
	if len(d) > digestLen {
		d = d[:digestLen]
	}
	if d == "" {
		// The zero value: a digest of nothing would be the digest of the empty string, which is a
		// real and recognisable constant. Emit a marker instead, so a zero value is never mistaken
		// for a wrapped empty secret.
		d = "unset"
	}
	return redactedPrefix + d
}

// String implements fmt.Stringer.
func (u Unresolved) String() string { return u.Redacted() }

// GoString implements fmt.GoStringer, which is what %#v uses.
//
// This method is the one most easily forgotten and the reason the experiment tests every verb:
// %#v does not call String, so a type that implements only Stringer prints its raw fields under
// %#v. Formatter below takes precedence over both, but GoString is implemented anyway for a
// direct call and so that removing Formatter cannot silently reopen the hole.
func (u Unresolved) GoString() string { return u.Redacted() }

// Format implements fmt.Formatter, which fmt consults before Stringer, GoStringer or anything
// else. Every verb renders the redaction, including %q, which is quoted so that the output stays
// valid for the verb the caller asked for.
func (u Unresolved) Format(f fmt.State, verb rune) {
	switch verb {
	case 'q':
		fmt.Fprintf(f, "%q", u.Redacted())
	default:
		fmt.Fprint(f, u.Redacted())
	}
}

// MarshalJSON implements json.Marshaler.
func (u Unresolved) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", u.Redacted())), nil
}

// MarshalText implements encoding.TextMarshaler, which several encoders reach for before falling
// back to reflection over the struct's fields.
func (u Unresolved) MarshalText() ([]byte, error) { return []byte(u.Redacted()), nil }

// LogValue implements slog.LogValuer.
func (u Unresolved) LogValue() slog.Value { return slog.StringValue(u.Redacted()) }

// Unsafe returns the plaintext. It is the single accessor by which plaintext leaves this type, so
// that the question "what may touch a secret?" is answered by one grep rather than by reading
// every package.
//
// Only extraction and the deliberate-failure controls may call it. That restriction is a test in
// this package, not a language guarantee: Go cannot express "only package X may call this".
//
// It returns a copy. Returning the field let a caller rewrite the bytes behind a digest that had
// already been computed, so the value a provider stored and the digest the journal recorded could
// silently disagree — the aliasing NewUnresolved's own copy exists to rule out.
func (u Unresolved) Unsafe() []byte {
	out := make([]byte, len(u.plaintext))
	copy(out, u.plaintext)
	return out
}

// Reference replaces a secret in a sanitized document. It names where the value came from and
// where it now lives, and carries the digest so that a persisted document can be checked against
// the journal without either holding the value.
type Reference struct {
	// Path is the location in the source document, in dotted form.
	Path string `json:"path"`
	// URI is the provider location the value was extracted to.
	URI string `json:"uri"`
	// Digest is the full SHA-256 of the extracted plaintext.
	Digest string `json:"digest"`
}

// Sanitized is a document that has been through extraction: every marked secret has been replaced
// by a Reference, and no plaintext remains. Persistence functions take this type and no other.
//
// Its fields are unexported and NewSanitized is the only constructor. That does not by itself stop
// a caller assembling one around a document that was never extracted: NewSanitized is exported,
// because Go cannot make a function visible to one sibling package and no other, so any package
// in this module could call it. What restricts it is TestNewSanitizedHasNoUnexpectedCallers, which
// fails if a non-test file outside extraction and staging does. The zero value is the other gap;
// Valid reports it and every persistence function rejects it.
//
// A v1 implementation that wants the compiler to enforce this would put the type in the package
// that constructs it, so that no other package can name its constructor at all.
type Sanitized struct {
	document []byte
	refs     []Reference
	valid    bool
}

// Valid reports whether this value came from Sanitize rather than from a zero-value literal.
func (s Sanitized) Valid() bool { return s.valid }

// Document is the sanitized bytes, safe to persist.
//
// It returns a copy. Returning the field itself would let any caller write through it — plaintext
// copied back into a document that already passed extraction, and read later by persistence — and
// the constructor's own copying would then protect only one direction of the aliasing it exists
// to rule out.
func (s Sanitized) Document() []byte {
	out := make([]byte, len(s.document))
	copy(out, s.document)
	return out
}

// References are the replacements made, in document order. It returns a copy, for the reason
// Document does.
func (s Sanitized) References() []Reference {
	out := make([]Reference, len(s.refs))
	copy(out, s.refs)
	return out
}

// NewSanitized builds a Sanitized document. It is exported for the extraction package and for
// staging's resume path, the only two legitimate callers. Nothing in the language enforces that;
// TestNewSanitizedHasNoUnexpectedCallers does.
//
// It does not itself extract. It is the point at which a caller asserts extraction has happened,
// and the type is what carries that assertion to the persistence layer.
func NewSanitized(document []byte, refs []Reference) Sanitized {
	buf := make([]byte, len(document))
	copy(buf, document)
	out := make([]Reference, len(refs))
	copy(out, refs)
	return Sanitized{document: buf, refs: out, valid: true}
}
