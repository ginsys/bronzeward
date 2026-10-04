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
	Encoding     string // the declared encoding modifier, if any
	Member       int    // the mapping member's position in key order, or -1 for a scalar reference
	Source       Origin
	SourcePath   string // the occurrence's document and path in its source
	Output       string // the output document and path, or "" when overridden
	OverriddenBy *Origin
}

// Dependency is a reference at a pinned version.
type Dependency struct {
	Reference string
	Version   int64
}

// Occurrence is one reproduction dependency (compilation.md §9): a reference occurrence in the
// import base or a fragment, with its source revision and digest, overridden or not.
type Occurrence struct {
	Reference string
	Version   int64
	Source    Origin
	Path      string
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
	for _, o := range c.outcomes {
		t := o.tracer
		r := Record{Reference: t.Ref(), Version: t.Version(), Encoding: t.Encoding(), Member: t.Member(),
			Source: c.origins[o.source], SourcePath: o.shownAt}
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
		if len(o.paths) > 0 && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// Reproduction is the reproduction dependencies: every reference occurrence of the import base
// and the fragments, overridden ones included. A mapping reference is one occurrence.
func (c Compiled) Reproduction() []Occurrence {
	var out []Occurrence
	for _, o := range c.outcomes {
		x := Occurrence{Reference: o.tracer.Ref(), Version: o.tracer.Version(), Source: c.origins[o.source],
			Path: o.shownAt}
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
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
	trace   []ingest.Resolved
	first   []int
	hosts   []ingest.Host
	done    map[int]prefix
}

type prefix struct {
	leaves []leaf
	hosts  map[string]string
}

// at is the trace composition of the import base and the first k fragments.
func (p *prefixes) at(k int) (prefix, error) {
	if x, ok := p.done[k]; ok {
		return x, nil
	}
	m, err := Compose(p.trace[0], p.trace[1:k+1])
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
	if p.done == nil {
		p.done = map[int]prefix{}
	}
	p.done[k] = prefix{ls, hosts}
	return p.done[k], nil
}

// presence is whether t is present in each prefix composition from its own source's on, the full
// composition, where it reached no output leaf, last. A value is present where a leaf carries its
// stand-in; a boolean where flipping it changes a leaf.
func (p *prefixes) presence(t traced) ([]bool, error) {
	n := len(p.trace) - 1
	present := make([]bool, n+1)
	for k := t.source; k < n; k++ {
		x, err := p.at(k)
		if err != nil {
			return nil, err
		}
		if t.Kind() != ingest.TraceBoolean {
			present[k] = slices.ContainsFunc(x.leaves, func(l leaf) bool { return l.kind == yaml.ScalarNode && t.Carried(l.value) })
			continue
		}
		fs := slices.Clone(p.trace[:k+1])
		s := p.sources[t.source]
		if fs[t.source], _, _, err = ingest.Trace(s.Text, s.Values, p.first[t.source], t.ID()); err != nil {
			return nil, fidelity()
		}
		fm, err := Compose(fs[0], fs[1:])
		if err != nil {
			return nil, fidelity()
		}
		changed, err := flipped(x.leaves, fm.bytes(), x.hosts)
		if err != nil {
			return nil, err
		}
		present[k] = len(changed) > 0
	}
	return present, nil
}
