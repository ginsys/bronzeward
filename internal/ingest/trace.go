package ingest

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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
// the stand-in, the member's key and the source path sit two pointers deep.
type Tracer struct{ p *tracer }

type tracer struct {
	id       int
	ref      string
	version  int64
	encoding string
	declared provider.Kind // the reference's declared kind
	member   int           // the member's position in key order, or -1 for a scalar reference
	members  int           // the mapping's member count, or 0 for a scalar reference
	kind     TraceKind
	// at and text are behind a second pointer: fmt prints a struct holding a Tracer in an
	// unexported field by reflection, and under a verb a pointer does not take (%s, %q) it prints
	// the pointed-to tracer's fields, but only an address for a pointer one level further down.
	at   *occurrence
	text *standInText
}

// occurrence is where a tracer stands: the mapping member's key, a value, and the source path,
// whose tokens may be values (compilation.md §8.3).
type occurrence struct {
	leaf string
	path Path
}

type standInText struct {
	value    string // the placed stand-in
	raw      string // for TraceBytes, the stand-in before encoding
	headless bool   // no line was long enough for the id, so other stand-ins can hold this one
	restored bool   // the real value of a schema secret was put back in place of the stand-in
	aliased  bool   // an alias of the reference holds the real value of a schema secret at its own path
	plain    string // the real value, decoded when the reference is base64-encoded
}

// ID is the tracer's id, unique within one composition's trace pass.
func (t Tracer) ID() int { return t.get().id }

// Ref is the reference name the occurrence declares.
func (t Tracer) Ref() string { return t.get().ref }

// Version is the declared version of the reference.
func (t Tracer) Version() int64 { return t.get().version }

// Encoding is the declared placement encoding, if any.
func (t Tracer) Encoding() string { return t.get().encoding }

// Declared is the reference's declared kind.
func (t Tracer) Declared() provider.Kind { return t.get().declared }

// Leaf is the key of the mapping member the tracer stands for, "" for a scalar reference. The key
// is a value the provider holds (compilation.md §4.2).
func (t Tracer) Leaf() string {
	if at := t.get().at; at != nil {
		return at.leaf
	}
	return ""
}

// Member is the position in key order of the mapping member the tracer stands for, or -1 for a
// scalar reference.
func (t Tracer) Member() int { return t.get().member }

// Members is the member count of the mapping reference the tracer stands for a member of, or 0 for
// a scalar reference.
func (t Tracer) Members() int { return t.get().members }

// Kind is how the stand-in is placed and found.
func (t Tracer) Kind() TraceKind { return t.get().kind }

// Path is where the occurrence stands in its source stream.
func (t Tracer) Path() Path {
	if at := t.get().at; at != nil {
		return at.path
	}
	return Path{}
}

func (t Tracer) get() tracer {
	if t.p == nil {
		return tracer{id: -1, member: -1}
	}
	return *t.p
}

// Indistinct refuses two tracers when either carries the other's stand-in: nothing could then tell
// which of them a leaf holds (compilation.md §8.1). It names both occurrences' paths.
func (t Tracer) Indistinct(u Tracer) error {
	if t.inStandIn(u) || u.inStandIn(t) {
		return refuse(RuleTraceIndistinct, t.Path().String(), u.Path().String())
	}
	return nil
}

// inStandIn reports whether u's placed or raw stand-in carries t.
func (t Tracer) inStandIn(u Tracer) bool {
	if u.p == nil || u.p.text == nil {
		return false
	}
	return t.Carried(u.p.text.value) || u.p.kind == TraceBytes && t.Carried(u.p.text.raw)
}

// Carried reports whether a composed leaf's text carries this tracer: a string's stand-in
// anywhere in it, or the stand-in's canonical base64 re-encoding, which the machinery writes for
// a byte field it decodes and encodes again (the last character before the padding changes); a
// base64-encoded string's placed or raw stand-in, or a leaf that decodes to text holding the raw
// one; an integer's exactly. A stand-in too short to hold the id is only found equal to the leaf
// or its decoding, since every longer stand-in holds its letters. A boolean is never carried: it
// is attributed by a flip pass.
func (t Tracer) Carried(v string) bool {
	if t.p == nil || t.p.text == nil {
		return false
	}
	x := t.p.text
	in := strings.Contains
	if x.headless {
		in = func(v, s string) bool { return v == s }
	}
	switch t.p.kind {
	case TraceString:
		if in(v, x.value) {
			return true
		}
		c, ok := canonicalBase64(x.value)
		return ok && in(v, c)
	case TraceBytes:
		if in(v, x.value) || in(v, x.raw) {
			return true
		}
		b, err := base64.StdEncoding.DecodeString(v)
		return err == nil && in(string(b), x.raw)
	case TraceInteger:
		return v == x.value
	}
	return false
}

// Unmarkable reports whether a message of a pass could quote a real schema secret of the tracer that
// no quote check finds and a value check of floor bytes would not: the secret is shorter than floor,
// and either it stands in the tracer's own place with a stand-in too short to hold the id (Quotes
// finds nothing), or an alias of the reference holds it at a path of its own, where no tracer stands.
// Both passes then hold the real value, a message of one quotes it as the other does, and nothing
// shows where it stands. A secret of floor bytes or more is found by the value check, whatever its
// lines are, so its message keeps its diagnostic. The value check reads strings only, so an integer
// held by an alias is unmarkable at any length.
func (t Tracer) Unmarkable(floor int) bool {
	if t.p == nil || t.p.text == nil {
		return false
	}
	x := t.p.text
	if x.aliased && t.p.kind == TraceInteger {
		return true
	}
	return len(x.plain) < floor && (x.restored && x.headless || x.aliased)
}

// minQuote is the shortest quote of a stand-in a message is searched for: its head alone.
const minQuote = 5

// Quotes is every place, as [start, end) byte offsets in order, where a message of a step on the
// trace pass quotes this tracer: a string's stand-in, or a prefix of five bytes or more of it or of
// one of its lines (every such line starts with the head; a decode error quotes seven bytes); a
// base64-encoded string's placed stand-in exactly, or its raw one as a string's; an integer's
// bounded by non-digits. A boolean, and a stand-in too short for its id, mark no quote: the first
// is its own value and the second is the value's shape only (compilation.md §8.3; SP §6.2).
func (t Tracer) Quotes(msg string) [][2]int {
	if t.p == nil || t.p.text == nil || t.p.text.headless {
		return nil
	}
	x := t.p.text
	var all [][2]int
	switch t.p.kind {
	case TraceString:
		all = prefixQuotes(msg, x.value)
	case TraceBytes:
		for i := 0; x.value != ""; {
			j := strings.Index(msg[i:], x.value)
			if j < 0 {
				break
			}
			all = append(all, [2]int{i + j, i + j + len(x.value)})
			i += j + len(x.value)
		}
		all = append(all, prefixQuotes(msg, x.raw)...)
	case TraceInteger:
		digit := func(c byte) bool { return c >= '0' && c <= '9' }
		for i := 0; ; {
			j := strings.Index(msg[i:], x.value)
			if j < 0 {
				break
			}
			s, e := i+j, i+j+len(x.value)
			if (s == 0 || !digit(msg[s-1])) && (e == len(msg) || !digit(msg[e])) {
				all = append(all, [2]int{s, e})
			}
			i = s + 1
		}
	}
	slices.SortFunc(all, func(a, b [2]int) int { return cmp.Or(a[0]-b[0], b[1]-a[1]) })
	// Overlapping quotes merge, so a quote that runs past another's end is marked to its own.
	var out [][2]int
	for _, s := range all {
		if len(out) > 0 && s[0] < out[len(out)-1][1] {
			out[len(out)-1][1] = max(out[len(out)-1][1], s[1])
			continue
		}
		out = append(out, s)
	}
	return out
}

// prefixQuotes is each place msg quotes v, or one of its lines, from the start for at least
// minQuote bytes, taking the longest such quote at each.
func prefixQuotes(msg, v string) [][2]int {
	if len(v) < minQuote {
		return nil
	}
	candidates := append([]string{v}, strings.Split(v, "\n")...)
	head := v[:minQuote]
	var out [][2]int
	for i := 0; ; {
		j := strings.Index(msg[i:], head)
		if j < 0 {
			return out
		}
		s, best := i+j, 0
		for _, c := range candidates {
			n := 0
			for n < len(msg)-s && n < len(c) && msg[s+n] == c[n] {
				n++
			}
			best = max(best, n)
		}
		out = append(out, [2]int{s, s + best})
		i = s + best
	}
}

// Host is one identified embedded document of a stream as a trace pass wrote it. It finds the
// document's copies in a composed output, to be walked as embedded there (WalkLeaves), whether or
// not a reference stands in it. A document without one is written as its source holds it, and
// one with a reference holds stand-ins, so a host is handled like a value: every fmt verb prints a
// placeholder, the marshallers fail and the text sits behind a pointer.
type Host struct{ p *host }

type host struct {
	format string
	text   *string // behind a second pointer, as a tracer's stand-in
}

// HostFormat is the document's format when v is exactly the text the trace pass wrote, and ""
// otherwise.
func (h Host) HostFormat(v string) string {
	if h.p == nil || h.p.text == nil || v != *h.p.text {
		return ""
	}
	return h.p.format
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
// tracer's flip pass: the boolean is negated and every other leaf is as in the trace pass. It
// also gives each identified embedded document as written. A refusal is Resolve's, or
// trace-indistinct, by path.
func Trace(s Sanitized, values map[string]provider.Value, first, flip int) (Resolved, []Tracer, []Host, error) {
	return TraceKeeping(s, values, first, flip, SecretLeaves{})
}

// SecretLeaves is the places of one source that hold a field the pinned machinery redacts: the
// same fields identification extracts without a mark. It holds no value.
type SecretLeaves struct{ paths []Path }

// SchemaSecretLeaves is the places of s, with its values resolved, that hold a secret field of the
// pinned machinery. A value is read only to ask the machinery which fields it redacts.
func SchemaSecretLeaves(s Sanitized, values map[string]provider.Value) (SecretLeaves, error) {
	r, err := Resolve(s, values)
	if err != nil {
		return SecretLeaves{}, err
	}
	docs, err := parseStream(r.bytes())
	if err != nil {
		return SecretLeaves{}, errors.New("ingest: a resolved stream does not parse")
	}
	var out SecretLeaves
	for i, d := range docs {
		pointers, err := schemaPointers(d, i, nil)
		if err != nil {
			return SecretLeaves{}, err
		}
		for _, sp := range pointers {
			out.paths = append(out.paths, Path{Doc: i, Pointer: sp.pointer})
		}
	}
	return out, nil
}

// holds is whether the value placed at p is, or holds, a secret field: p names the field, a mapping
// that holds it, or a place inside it. A reference inside an embedded document holds none, since the
// machinery does not read embedded text.
func (l SecretLeaves) holds(p Path) bool {
	within := func(a, b []string) bool { return len(a) >= len(b) && slices.Equal(a[:len(b)], b) }
	return p.Format == "" && slices.ContainsFunc(l.paths, func(q Path) bool {
		return q.Doc == p.Doc && (within(q.Pointer, p.Pointer) || within(p.Pointer, q.Pointer))
	})
}

// TraceKeeping is Trace, except that a reference that holds a field in secret keeps its value
// where a trace pass would put a stand-in. The tracers and their ids are those of Trace, but the
// text holds a real secret: it is for the validation of a pass only, in memory, and no text of it
// may leave the compiler (compilation.md §8.1).
func TraceKeeping(s Sanitized, values map[string]provider.Value, first, flip int, secret SecretLeaves) (Resolved, []Tracer, []Host, error) {
	next := first
	flipped := false
	var ts []Tracer
	var hosts []Host
	// anchored gives, for the node of a restored reference that carries an anchor, the node an alias
	// of it at a path holds: an alias is a place of its own, restored only where the schema holds a
	// secret.
	anchored := map[*yaml.Node]func(Path) *yaml.Node{}
	r, err := resolveWith(s, func(name string, ref Reference, p Path, site *yaml.Node) (*yaml.Node, error) {
		n, rule := placed(name, ref, values)
		if rule != "" {
			return nil, refuse(rule, p.String())
		}
		leaves := []*yaml.Node{n}
		keys := []string{""}
		if n.Kind == yaml.MappingNode && len(n.Content) == 0 {
			// no leaf would carry a stand-in, so nothing could show where it went
			return nil, refuse(RuleTraceIndistinct, p.String())
		}
		if n.Kind == yaml.MappingNode {
			leaves, keys = nil, nil
			for i := 0; i+1 < len(n.Content); i += 2 {
				keys = append(keys, n.Content[i].Value)
				leaves = append(leaves, n.Content[i+1])
			}
		}
		kept := make([][2]string, len(leaves))
		texts := make([]*standInText, len(leaves))
		for i, leaf := range leaves {
			if next > maxTracerID {
				return nil, refuse(RuleTraceIndistinct, p.String())
			}
			t := &tracer{id: next, ref: name, version: ref.Version, encoding: ref.Encoding, declared: ref.Kind, member: -1,
				at: &occurrence{keys[i], p}}
			if n.Kind == yaml.MappingNode {
				t.member, t.members = i, len(leaves)
			}
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
			switch t.kind {
			case TraceString:
				x.headless = !strings.Contains(x.value, fmt.Sprintf("zq%03d", next))
			case TraceBytes:
				x.headless = !strings.Contains(x.raw, fmt.Sprintf("zq%03d", next))
			}
			if t.kind != TraceBoolean && x.value == leaf.Value {
				return nil, refuse(RuleTraceIndistinct, p.String())
			}
			x.plain = leaf.Value
			if plain, err := base64.StdEncoding.DecodeString(leaf.Value); err == nil && t.kind == TraceBytes {
				x.plain = string(plain)
			}
			texts[i] = x
			kept[i] = [2]string{leaf.Value, x.value}
			if t.kind == TraceBoolean {
				kept[i][0] = x.value
			}
			if t.kind != TraceBoolean && secret.holds(memberPath(p, n, keys[i])) {
				x.value, x.raw, x.restored = leaf.Value, "", true
				if plain, err := base64.StdEncoding.DecodeString(leaf.Value); err == nil && t.kind == TraceBytes {
					x.raw = string(plain)
				}
			}
			leaf.Value, t.text = x.value, x
			ts = append(ts, Tracer{t})
			next++
		}
		if site.Anchor != "" && len(secret.paths) > 0 {
			anchored[site] = func(q Path) *yaml.Node {
				c := *n
				c.Anchor = ""
				if n.Kind != yaml.MappingNode {
					c.Value = pickKept(secret, q, kept[0], texts[0])
					return &c
				}
				c.Content = slices.Clone(n.Content)
				for i, k := range keys {
					m := *n.Content[2*i+1]
					m.Value = pickKept(secret, memberPath(q, n, k), kept[i], texts[i])
					c.Content[2*i+1] = &m
				}
				return &c
			}
		}
		return n, nil
	}, func(format, text string) {
		hosts = append(hosts, Host{&host{format: format, text: &text}})
	}, func(a *yaml.Node, q Path) {
		if f := anchored[a.Alias]; f != nil {
			*a = *f(q)
		}
	})
	if err != nil {
		return Resolved{}, nil, nil, err
	}
	if flip >= 0 && !flipped {
		return Resolved{}, nil, nil, errors.New("ingest: the flip pass names no boolean tracer of the stream")
	}
	return r, ts, hosts, nil
}

// memberPath is the path of the member key of a value placed at p, when the value is a mapping, and
// p itself otherwise: each member is a field of its own, so a secret member must not restore the
// one beside it.
func memberPath(p Path, v *yaml.Node, key string) Path {
	if v.Kind == yaml.MappingNode {
		p.Pointer = append(slices.Clone(p.Pointer), key)
	}
	return p
}

// pickKept is the real value of a leaf at p when the schema holds a secret there, and its stand-in
// otherwise. The leaf's text records that an alias holds the real value, since no tracer stands in
// the alias's place.
func pickKept(secret SecretLeaves, p Path, kept [2]string, x *standInText) string {
	if kept[0] != kept[1] && secret.holds(p) {
		x.aliased = true
		return kept[0]
	}
	return kept[1]
}

// standIn is SP's shape stand-in (compilation.md §8.1, choice §16.22): each letter becomes x or
// X, each digit 0, every other byte stays, and each line of five bytes or more starts with zq and
// the three-digit id. The bytes left of a character the head cuts become x, so the stand-in stays
// UTF-8 and keeps its length.
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
			for j := len(head); j < len(b) && !utf8.RuneStart(b[j]); j++ {
				b[j] = 'x'
			}
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

const hostText = "[embedded document]"

var errHostRender = errors.New("ingest: an embedded document's trace text is not marshalled")

func (Host) String() string               { return hostText }
func (Host) GoString() string             { return hostText }
func (Host) Format(f fmt.State, _ rune)   { io.WriteString(f, hostText) }
func (Host) MarshalJSON() ([]byte, error) { return nil, errHostRender }
func (Host) MarshalText() ([]byte, error) { return nil, errHostRender }
func (Host) MarshalYAML() (any, error)    { return nil, errHostRender }
