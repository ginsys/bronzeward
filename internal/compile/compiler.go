package compile

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// RuleFidelity refuses a composition its trace or flip passes do not reproduce: a pass that does
// not compose or validate when the real one does, a different tree shape, a leaf that differs
// from the real one without carrying a tracer, or one that carries a tracer without differing
// (compilation.md §8.1: fidelity fails closed).
const RuleFidelity Rule = "fidelity"

// Source is one composition input: the import base or one fragment revision, sanitized, with the
// pinned value of every reference it declares.
type Source struct {
	Revision string                    // the source revision
	Digest   string                    // the hex SHA-256 of the revision's stored text
	Text     ingest.Sanitized          // the sanitized text
	Values   map[string]provider.Value // the pinned values, by reference name
}

// Input is one machine's composition: its import base, its fragments in composition order
// (compilation.md §6 step 1) and the platform mode it is validated in.
type Input struct {
	Base      Source
	Fragments []Source
	Mode      Mode
}

// Compiled is one machine's compiled configuration and the outcome of every reference
// occurrence. Like Materialized, every fmt verb prints a placeholder and the marshallers fail.
type Compiled struct {
	m        Materialized
	outcomes []outcome
	origins  []Origin // by source: 0 the import base, i+1 fragment i
}

// outcome is where one tracer's value ended up: the output paths holding it, or the fragment
// that overrode it.
type outcome struct {
	tracer  ingest.Tracer
	source  int      // 0 the import base, i+1 fragment i
	paths   []string // the output paths, or none when overridden; cleared once shown is set
	by      int      // the overriding fragment's index, or -1
	shown   []string // paths, redacted (compilation.md §8.3)
	shownAt string   // the occurrence's source path, redacted
}

// traced is a tracer and the source it was placed in.
type traced struct {
	ingest.Tracer
	source int
}

// Materialized is the compiled configuration.
func (c Compiled) Materialized() Materialized { return c.m }

// Compile performs compilation.md §6 steps 4 to 8 on in: it resolves every source, composes them,
// traces the composition with a trace pass and a flip pass per boolean reference, attributes
// every output leaf holding a reference's value, names the fragment that overrode every other
// occurrence from the trace pass's prefix compositions, checks the output for copies and base
// overrides (step 7), and validates the real composition and every pass in the node's mode. A
// refusal names rules and paths only, a path token holding a resolved value redacted (§8.3). A
// real rejection or an invalid real configuration is returned as Compose and Validate return it,
// whatever the passes do.
func Compile(in Input) (Compiled, error) {
	sources := append([]Source{in.Base}, in.Fragments...)
	c, err := compile(in, sources)
	if err != nil {
		return Compiled{}, newRedactor(sources).paths(err)
	}
	return c, nil
}

func compile(in Input, sources []Source) (Compiled, error) {
	if _, err := ParseMode(string(in.Mode)); err != nil {
		return Compiled{}, err
	}
	real := make([]ingest.Resolved, len(sources))
	for i, s := range sources {
		r, err := ingest.Resolve(s.Text, s.Values)
		if err != nil {
			return Compiled{}, fmt.Errorf("compile: %s: %w", inputName(i), err)
		}
		real[i] = r
	}
	m, err := Compose(real[0], real[1:])
	if err != nil {
		return Compiled{}, err
	}
	trace := make([]ingest.Resolved, len(sources))
	first := make([]int, len(sources))
	var ts []traced
	var hs []ingest.Host
	for i, s := range sources {
		first[i] = len(ts)
		r, xs, h, err := ingest.Trace(s.Text, s.Values, first[i], -1)
		if err != nil {
			return Compiled{}, fmt.Errorf("compile: %s: %w", inputName(i), err)
		}
		trace[i] = r
		hs = append(hs, h...)
		for _, x := range xs {
			ts = append(ts, traced{x, i})
		}
	}
	for i, t := range ts {
		for _, u := range ts[i+1:] {
			if err := t.Indistinct(u.Tracer); err != nil {
				return Compiled{}, fmt.Errorf("compile: %w", err)
			}
		}
	}
	tm, err := Compose(trace[0], trace[1:])
	if err != nil {
		return Compiled{}, fidelity()
	}
	attrs, hosts, err := attribute(m.bytes(), tm.bytes(), ts, hs)
	if err != nil {
		return Compiled{}, err
	}
	traceLeaves, err := leaves(tm.bytes(), hosts)
	if err != nil {
		return Compiled{}, fidelity()
	}
	passes := []Materialized{tm}
	for _, t := range ts {
		if t.Kind() != ingest.TraceBoolean {
			continue
		}
		fs := slices.Clone(trace)
		s := sources[t.source]
		if fs[t.source], _, _, err = ingest.Trace(s.Text, s.Values, first[t.source], t.ID()); err != nil {
			return Compiled{}, fidelity()
		}
		fm, err := Compose(fs[0], fs[1:])
		if err != nil {
			return Compiled{}, fidelity()
		}
		if attrs[t.ID()], err = flipped(traceLeaves, fm.bytes(), hosts); err != nil {
			return Compiled{}, err
		}
		passes = append(passes, fm)
	}
	pre := prefixes{sources: sources, trace: trace, first: first, hosts: hs}
	outcomes := make([]outcome, len(ts))
	for i, t := range ts {
		o := outcome{tracer: t.Tracer, source: t.source, paths: attrs[t.ID()], by: -1}
		if len(o.paths) == 0 {
			present, err := pre.presence(t)
			if err != nil {
				return Compiled{}, err
			}
			if o.by, err = overrider(t.source, present); err != nil {
				return Compiled{}, err
			}
		}
		outcomes[i] = o
	}
	realLeaves, err := leaves(m.bytes(), hosts)
	if err != nil {
		return Compiled{}, fidelity()
	}
	if err := checkOutput(realLeaves, sources, outcomes); err != nil {
		return Compiled{}, err
	}
	if err := m.Validate(in.Mode); err != nil {
		return Compiled{}, err
	}
	for _, p := range passes {
		if err := p.Validate(in.Mode); err != nil {
			return Compiled{}, fidelity()
		}
	}
	// The kept outcomes hold redacted paths only: fmt reaches an unexported field by reflection,
	// past Compiled's own placeholder.
	red := newRedactor(sources)
	for i, o := range outcomes {
		outcomes[i].shownAt = red.path(o.tracer.Path().String())
		for _, p := range o.paths {
			outcomes[i].shown = append(outcomes[i].shown, red.path(p))
		}
		outcomes[i].paths = nil
	}
	return Compiled{m: m, outcomes: outcomes, origins: origins(sources)}, nil
}

func inputName(i int) string {
	if i == 0 {
		return "base"
	}
	return fmt.Sprintf("fragment[%d]", i-1)
}

func fidelity(paths ...string) error { return &Error{Rule: RuleFidelity, Paths: paths} }

// leaf is one value node of a composed stream. On the real side its value is plaintext: it is
// compared and never printed or returned.
type leaf struct {
	path  string
	kind  yaml.Kind
	tag   string
	value string
}

// leaves is every value node of a composed stream, descending into the identified embedded
// documents hosts names.
func leaves(b []byte, hosts map[string]string) ([]leaf, error) {
	var out []leaf
	err := ingest.WalkLeaves(b, hosts, func(p ingest.Path, n *yaml.Node) error {
		l := leaf{path: p.String(), kind: n.Kind}
		if n.Kind == yaml.ScalarNode {
			l.tag, l.value = n.Tag, n.Value
		}
		out = append(out, l)
		return nil
	})
	return out, err
}

// hostsOf finds a composed trace stream's identified embedded documents: every string that is
// exactly a document the trace pass wrote (ingest.Host), by path.
func hostsOf(b []byte, hs []ingest.Host) (map[string]string, error) {
	hosts := map[string]string{}
	err := ingest.WalkLeaves(b, nil, func(p ingest.Path, n *yaml.Node) error {
		if n.Kind != yaml.ScalarNode {
			return nil
		}
		for _, h := range hs {
			if f := h.HostFormat(n.Value); f != "" {
				hosts[p.String()] = f
				return nil
			}
		}
		return nil
	})
	return hosts, err
}

// shapeDiffers is the first path at which two walks stop having the same nodes, kinds and scalar
// tags in the same places, or "" when they do not.
func shapeDiffers(a, b []leaf) (string, bool) {
	for i := range min(len(a), len(b)) {
		if a[i].path != b[i].path || a[i].kind != b[i].kind || a[i].tag != b[i].tag {
			return a[i].path, true
		}
	}
	return "", len(a) != len(b)
}

// attribute walks the real and the trace composition in parallel (SP's attribution, compilation.md
// §8.1): every leaf where they differ must carry a tracer and is attributed to every tracer it
// carries, and a leaf that carries one must differ. It returns the attributed output paths by
// tracer id and the trace composition's identified embedded documents.
func attribute(real, trace []byte, ts []traced, hs []ingest.Host) (map[int][]string, map[string]string, error) {
	hosts, err := hostsOf(trace, hs)
	if err != nil {
		return nil, nil, fidelity()
	}
	rl, err := leaves(real, hosts)
	if err != nil {
		return nil, nil, fidelity()
	}
	tl, err := leaves(trace, hosts)
	if err != nil {
		return nil, nil, fidelity()
	}
	if at, ok := shapeDiffers(rl, tl); ok {
		return nil, nil, fidelity(at)
	}
	attrs := map[int][]string{}
	for i, l := range tl {
		if l.kind != yaml.ScalarNode {
			continue
		}
		differs := rl[i].value != l.value
		carried := false
		for _, t := range ts {
			if t.Carried(l.value) {
				carried = true
				attrs[t.ID()] = append(attrs[t.ID()], l.path)
			}
		}
		if differs != carried {
			return nil, nil, fidelity(l.path)
		}
	}
	return attrs, hosts, nil
}

// flipped is the output paths a flip pass changes against the trace pass: the flipped boolean's
// (SP's attributeFlip). The two must have the same shape.
func flipped(trace []leaf, flip []byte, hosts map[string]string) ([]string, error) {
	fl, err := leaves(flip, hosts)
	if err != nil {
		return nil, fidelity()
	}
	if at, ok := shapeDiffers(trace, fl); ok {
		return nil, fidelity(at)
	}
	var paths []string
	for i, l := range trace {
		if l.kind == yaml.ScalarNode && l.value != fl[i].value {
			paths = append(paths, l.path)
		}
	}
	return paths, nil
}

const compiledText = "[compiled configuration]"

var errCompiledRender = errors.New("compile: a compiled configuration is not marshalled")

func (Compiled) String() string               { return compiledText }
func (Compiled) GoString() string             { return compiledText }
func (Compiled) Format(f fmt.State, _ rune)   { io.WriteString(f, compiledText) }
func (Compiled) MarshalJSON() ([]byte, error) { return nil, errCompiledRender }
func (Compiled) MarshalText() ([]byte, error) { return nil, errCompiledRender }
func (Compiled) MarshalYAML() (any, error)    { return nil, errCompiledRender }
