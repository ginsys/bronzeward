package compile

import (
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// Origin is the source a reference occurrence stands in: the import base, or one fragment by its
// index in composition order, with the revision and the SHA-256 of its stored text.
type Origin struct {
	Base     bool
	Fragment int // the fragment's index, -1 for the import base
	Revision string
	Digest   string
}

// Record is one row of the provenance record (compilation.md §8.2): a reference occurrence, or one
// member of a mapping reference, and exactly one outcome, the output path it reached (one row per
// path, so an alias has two) or the fragment that overrode it. A member is named by its position,
// since its key is a value the provider holds, and a path token holding a resolved value, such as
// that key, reads <redacted> (§8.3).
type Record struct {
	Reference    string
	Version      int64
	Kind         string // the declared kind it was compiled as: string, integer, boolean or mapping
	Encoding     string // the declared encoding modifier, if any
	Member       int    // the mapping member's position in key order, or -1 for a scalar reference
	Source       Origin
	SourcePath   string // the occurrence's document and path in its source
	Occurrence   int    // the occurrence's position among its source revision's, as its Occurrence's
	Output       string // the output document and path, or "" when overridden
	OverriddenBy *Origin
}

// Dependency is a reference at a pinned version.
type Dependency struct {
	Reference string
	Version   int64
}

// Occurrence is one reproduction dependency (compilation.md §9): a reference occurrence in the
// import base or a fragment, with its source revision and digest, overridden or not. Two whose
// redacted paths read the same are told apart by their position among their source revision's
// occurrences, which the provenance records of each name too.
type Occurrence struct {
	Reference  string
	Version    int64
	Source     Origin
	Path       string
	Occurrence int
}

func origins(sources []Source) []Origin {
	out := make([]Origin, len(sources))
	for i, s := range sources {
		out[i] = Origin{Base: i == 0, Fragment: i - 1, Revision: s.Revision, Digest: s.Digest}
	}
	return out
}

// Provenance is the provenance record, in occurrence order.
func (c Compiled) Provenance() []Record {
	var out []Record
	ordinals := c.ordinals()
	for i, o := range c.outcomes {
		t := o.tracer
		r := Record{Reference: t.Ref(), Version: t.Version(), Kind: string(t.Declared()), Encoding: t.Encoding(), Member: t.Member(),
			Source: c.origins[o.source], SourcePath: o.shownAt, Occurrence: ordinals[i]}
		if o.by >= 0 {
			by := c.origins[o.by+1]
			r.OverriddenBy = &by
			out = append(out, r)
			continue
		}
		for _, p := range o.shown {
			r.Output = p
			out = append(out, r)
		}
	}
	return out
}

// Effective is the effective dependencies: every reference and version whose stand-in reached an
// output leaf, booleans through their flip pass, once each.
func (c Compiled) Effective() []Dependency {
	var out []Dependency
	for _, o := range c.outcomes {
		d := Dependency{Reference: o.tracer.Ref(), Version: o.tracer.Version()}
		if len(o.shown) > 0 && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// Reproduction is the reproduction dependencies: every reference occurrence of the import base
// and the fragments, overridden ones included, numbered from 0 within each source revision.
func (c Compiled) Reproduction() []Occurrence {
	var out []Occurrence
	next := map[int]int{} // per source revision, the next occurrence not yet emitted
	for i, n := range c.ordinals() {
		o := c.outcomes[i]
		if n < next[o.source] {
			continue
		}
		next[o.source] = n + 1
		out = append(out, Occurrence{Reference: o.tracer.Ref(), Version: o.tracer.Version(), Source: c.origins[o.source],
			Path: o.shownAt, Occurrence: n})
	}
	return out
}

// ordinals is each outcome's occurrence: its position among its source revision's occurrences, in
// outcome order. A mapping reference is one occurrence: its members share their source and source
// path, which is told apart before it is redacted.
func (c Compiled) ordinals() []int {
	type occurrence struct {
		source int
		path   string
	}
	seen := map[occurrence]int{}
	next := map[int]int{}
	out := make([]int, len(c.outcomes))
	for i, o := range c.outcomes {
		k := occurrence{o.source, o.tracer.Path().String()}
		n, ok := seen[k]
		if !ok {
			n = next[o.source]
			seen[k], next[o.source] = n, n+1
		}
		out[i] = n
	}
	return out
}

// overrider is the fragment that overrode an occurrence of source own (0 the import base, i+1
// fragment i), from its presence in each prefix composition (present[k] for the base and the
// first k fragments, the last entry being the full composition): the fragment after which it is
// first absent. An occurrence absent from its own source's prefix, or never absent, fails closed.
func overrider(own int, present []bool) (int, error) {
	if own < 0 || own >= len(present) || !present[own] {
		return 0, fidelity()
	}
	for k := own + 1; k < len(present); k++ {
		if !present[k] {
			return k - 1, nil
		}
	}
	return 0, fidelity()
}

// prefixes composes the trace pass's proper prefixes on demand (compilation.md §8.1: one
// composition per proper fragment prefix), to name the fragment that overrode an occurrence.
type prefixes struct {
	sources []Source
	real    []ingest.Resolved
	trace   []ingest.Resolved
	first   []int
	hosts   []ingest.Host
	done    map[int]prefix
}

type prefix struct {
	leaves []leaf // the trace composition's
	real   []leaf // the real composition's, of the same shape
	hosts  map[string]string
}

// at is the trace and the real composition of the import base and the first k fragments.
func (p *prefixes) at(k int) (prefix, error) {
	if x, ok := p.done[k]; ok {
		return x, nil
	}
	m, err := Compose(p.trace[0], p.trace[1:k+1])
	if err != nil {
		return prefix{}, fidelity()
	}
	rm, err := Compose(p.real[0], p.real[1:k+1])
	if err != nil {
		return prefix{}, fidelity()
	}
	hosts, err := hostsOf(m.bytes(), p.hosts)
	if err != nil {
		return prefix{}, fidelity()
	}
	ls, err := leaves(m.bytes(), hosts)
	if err != nil {
		return prefix{}, fidelity()
	}
	rls, err := leaves(rm.bytes(), hosts)
	if err != nil {
		return prefix{}, fidelity()
	}
	if at, ok := shapeDiffers(rls, ls); ok {
		return prefix{}, fidelity(at)
	}
	if p.done == nil {
		p.done = map[int]prefix{}
	}
	p.done[k] = prefix{ls, rls, hosts}
	return p.done[k], nil
}

// presence is whether t is present in each prefix composition from its own source's on, the full
// composition, where it reached no output leaf, last.
func (p *prefixes) presence(t traced) ([]bool, error) {
	n := len(p.trace) - 1
	present := make([]bool, n+1)
	for k := t.source; k < n; k++ {
		c, err := p.count(t, k)
		if err != nil {
			return nil, err
		}
		present[k] = c > 0
	}
	return present, nil
}

// narrowed is the fragment that overrode some of the leaves an import base occurrence reaches in
// the base alone, though full leaves still carry it: an aliased reference one of whose aliases a
// fragment replaced or deleted. It is the fragment after which fewer leaves first carry it, or -1
// when none did. A fragment cannot add a leaf carrying the base's stand-in: a literal one reaching
// the full composition fails attribution, as it equals its real leaf. So the counts never grow, the
// prefixes are composed only when the full count is below the base's, and a count that grows
// fails closed.
func (p *prefixes) narrowed(t traced, full int) (int, error) {
	n := len(p.trace) - 1
	prev, err := p.count(t, 0)
	if err != nil || prev == full {
		return -1, err
	}
	for k := 1; k <= n; k++ {
		c := full
		if k < n {
			if c, err = p.count(t, k); err != nil {
				return 0, err
			}
		}
		if c < prev {
			return k - 1, nil
		}
		if c > prev {
			return 0, fidelity()
		}
	}
	return -1, nil
}

// count is the number of leaves of the prefix composition of the import base and the first k
// fragments that carry t: a value's where a leaf carries its stand-in and differs from the real
// prefix's, as attribution counts it, so a literal shaped like a stand-in is not one; a boolean's
// where flipping it changes a leaf.
func (p *prefixes) count(t traced, k int) (int, error) {
	x, err := p.at(k)
	if err != nil {
		return 0, err
	}
	if t.Kind() != ingest.TraceBoolean {
		c := 0
		for i, l := range x.leaves {
			if l.kind == yaml.ScalarNode && l.value != x.real[i].value && t.Carried(l.value) {
				c++
			}
		}
		return c, nil
	}
	fs := slices.Clone(p.trace[:k+1])
	s := p.sources[t.source]
	if fs[t.source], _, _, err = ingest.Trace(s.Text, s.Values, p.first[t.source], t.ID()); err != nil {
		return 0, fidelity()
	}
	fm, err := Compose(fs[0], fs[1:])
	if err != nil {
		return 0, fidelity()
	}
	changed, err := flipped(x.leaves, fm.bytes(), x.hosts)
	if err != nil {
		return 0, err
	}
	return len(changed), nil
}
