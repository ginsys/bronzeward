package ingest

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

// RuleTraceIndistinct refuses a trace pass whose stand-in could not be told from the real value:
// one equal to the value it replaces, or an id past the three digits the stand-in's head holds
// (compilation.md §8.1: fidelity fails closed).
const RuleTraceIndistinct Rule = "trace-indistinct"

// maxTracerID is the largest id a stand-in's three-digit head holds.
const maxTracerID = 999

// TraceKind is how a tracer's stand-in is placed and found.
type TraceKind string

const (
	TraceString  TraceKind = "string"  // the shape stand-in, placed as is
	TraceBytes   TraceKind = "bytes"   // a base64-encoded string: the encoding of the shape stand-in
	TraceInteger TraceKind = "integer" // 61000 plus the id
	TraceBoolean TraceKind = "boolean" // the value itself; attributed by a flip pass
)

// Tracer is the stand-in one reference occurrence, or one member of a mapping reference, carries
// through a trace pass (compilation.md §8.1). It holds the value's length and character classes,
// so it is handled like the value: every fmt verb prints a placeholder, the marshallers fail and
// the stand-in sits behind a pointer.
type Tracer struct{ p *tracer }

type tracer struct {
	id       int
	ref      string
	version  int64
	encoding string
	leaf     string
	kind     TraceKind
	path     Path
	// text is behind a second pointer: fmt's reflection prints a pointed-to struct's fields
	// under a bad verb, but only an address for a pointer one level further down.
	text *standInText
}

type standInText struct {
	value string // the placed stand-in
	raw   string // for TraceBytes, the stand-in before encoding
	host  string // inside an identified embedded document, that document's written trace text
}

// ID is the tracer's id, unique within one composition's trace pass.
func (t Tracer) ID() int { return t.get().id }

// Ref is the reference name the occurrence declares.
func (t Tracer) Ref() string { return t.get().ref }

// Version is the declared version of the reference.
func (t Tracer) Version() int64 { return t.get().version }

// Encoding is the declared placement encoding, if any.
func (t Tracer) Encoding() string { return t.get().encoding }

// Leaf is the mapping member the tracer stands for, or "" for a scalar reference.
func (t Tracer) Leaf() string { return t.get().leaf }

// Kind is how the stand-in is placed and found.
func (t Tracer) Kind() TraceKind { return t.get().kind }

// Path is where the occurrence stands in its source stream.
func (t Tracer) Path() Path { return t.get().path }

func (t Tracer) get() tracer {
	if t.p == nil {
		return tracer{id: -1}
	}
	return *t.p
}

// Carried reports whether a composed leaf's text carries this tracer: a string's stand-in
// anywhere in it, or the stand-in's canonical base64 re-encoding, which the machinery writes for
// a byte field it decodes and encodes again (the last character before the padding changes); a
// base64-encoded string's placed or raw stand-in, or a leaf that decodes to text holding the raw
// one; an integer's exactly. A boolean is never carried: it is attributed by a flip pass.
func (t Tracer) Carried(v string) bool {
	if t.p == nil || t.p.text == nil {
		return false
	}
	x := t.p.text
	switch t.p.kind {
	case TraceString:
		if strings.Contains(v, x.value) {
			return true
		}
		c, ok := canonicalBase64(x.value)
		return ok && strings.Contains(v, c)
	case TraceBytes:
		if strings.Contains(v, x.value) || strings.Contains(v, x.raw) {
			return true
		}
		b, err := base64.StdEncoding.DecodeString(v)
		return err == nil && bytes.Contains(b, []byte(x.raw))
	case TraceInteger:
		return v == x.value
	}
	return false
}

// HostFormat is the format of the identified embedded document the tracer stands in, when v is
// exactly that document's text as the trace pass wrote it, and "" otherwise. The text holds the
// pass's stand-ins, so it is unique to the pass: it finds the document's copies in a composed
// trace output, to be walked as embedded there (WalkLeaves).
func (t Tracer) HostFormat(v string) string {
	if t.p == nil || t.p.text == nil || t.p.path.Format == "" || v != t.p.text.host {
		return ""
	}
	return t.p.path.Format
}

// canonicalBase64 is s decoded as standard base64 and encoded again, when that differs from s.
func canonicalBase64(s string) (string, bool) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", false
	}
	c := base64.StdEncoding.EncodeToString(b)
	return c, c != s
}

// Trace is the trace pass of compilation.md §8.1 over s: every reference occurrence, in walk
// order, is resolved as Resolve resolves it and each of its scalar leaves (a mapping's members in
// key order) is replaced by a stand-in, with ids from first on. With flip ≥ 0 it is that boolean
// tracer's flip pass: the boolean is negated and every other leaf is as in the trace pass. A
// refusal is Resolve's, or trace-indistinct, by path.
func Trace(s Sanitized, values map[string]provider.Value, first, flip int) (Resolved, []Tracer, error) {
	next := first
	flipped := false
	var ts []Tracer
	r, err := resolveWith(s, func(name string, ref Reference, p Path) (*yaml.Node, error) {
		n, rule := placed(name, ref, values)
		if rule != "" {
			return nil, refuse(rule, p.String())
		}
		leaves := []*yaml.Node{n}
		keys := []string{""}
		if n.Kind == yaml.MappingNode {
			leaves, keys = nil, nil
			for i := 0; i+1 < len(n.Content); i += 2 {
				keys = append(keys, n.Content[i].Value)
				leaves = append(leaves, n.Content[i+1])
			}
		}
		for i, leaf := range leaves {
			if next > maxTracerID {
				return nil, refuse(RuleTraceIndistinct, p.String())
			}
			t := &tracer{id: next, ref: name, version: ref.Version, encoding: ref.Encoding, leaf: keys[i], path: p}
			x := &standInText{}
			switch {
			case leaf.Tag == "!!int":
				t.kind, x.value = TraceInteger, strconv.Itoa(61000+next)
			case leaf.Tag == "!!bool":
				t.kind, x.value = TraceBoolean, leaf.Value
				if flip == next {
					x.value = strconv.FormatBool(leaf.Value != "true")
					flipped = true
				}
			case ref.Encoding == "base64" && n.Kind != yaml.MappingNode:
				plain, err := base64.StdEncoding.DecodeString(leaf.Value)
				if err != nil {
					return nil, refuse(RuleUnresolved, p.String())
				}
				t.kind, x.raw = TraceBytes, standIn(string(plain), next)
				x.value = base64.StdEncoding.EncodeToString([]byte(x.raw))
			default:
				t.kind, x.value = TraceString, standIn(leaf.Value, next)
			}
			if t.kind != TraceBoolean && x.value == leaf.Value {
				return nil, refuse(RuleTraceIndistinct, p.String())
			}
			leaf.Value, t.text = x.value, x
			ts = append(ts, Tracer{t})
			next++
		}
		return n, nil
	}, func(p Path, text string) {
		for _, t := range ts {
			if x := t.p; x.path.Format != "" && x.path.Doc == p.Doc && slices.Equal(x.path.Pointer, p.Pointer) && x.text.host == "" {
				x.text.host = text
			}
		}
	})
	if err != nil {
		return Resolved{}, nil, err
	}
	if flip >= 0 && !flipped {
		return Resolved{}, nil, errors.New("ingest: the flip pass names no boolean tracer of the stream")
	}
	return r, ts, nil
}

// standIn is SP's shape stand-in (compilation.md §8.1, choice §16.22): each letter becomes x or
// X, each digit 0, every other byte stays, and each line of five bytes or more starts with zq and
// the three-digit id.
func standIn(real string, id int) string {
	head := fmt.Sprintf("zq%03d", id)
	lines := strings.Split(real, "\n")
	for i, line := range lines {
		b := []byte(line)
		for j, c := range b {
			switch {
			case c >= 'a' && c <= 'z':
				b[j] = 'x'
			case c >= 'A' && c <= 'Z':
				b[j] = 'X'
			case c >= '0' && c <= '9':
				b[j] = '0'
			}
		}
		if len(b) >= len(head) {
			copy(b, head)
		}
		lines[i] = string(b)
	}
	return strings.Join(lines, "\n")
}

const tracerText = "[tracer]"

var errTracerRender = errors.New("ingest: a tracer is not marshalled")

func (Tracer) String() string               { return tracerText }
func (Tracer) GoString() string             { return tracerText }
func (Tracer) Format(f fmt.State, _ rune)   { io.WriteString(f, tracerText) }
func (Tracer) MarshalJSON() ([]byte, error) { return nil, errTracerRender }
func (Tracer) MarshalText() ([]byte, error) { return nil, errTracerRender }
func (Tracer) MarshalYAML() (any, error)    { return nil, errTracerRender }
