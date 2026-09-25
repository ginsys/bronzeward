package main

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// A Tracer is the stand-in one reference occurrence (or one leaf of a mapping reference) carries
// through the trace pass. Kind is str, int, bytes (Raw is the tracer, Value its base64, the placed
// form) or bool (no stand-in: a boolean is attributed by flipping it, see attributeFlip).
type Tracer struct {
	ID      int    `json:"id"`
	Occ     int    `json:"occurrence"`
	Ref     string `json:"ref"`
	Version string `json:"version"`
	Leaf    string `json:"leaf"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Raw     string `json:"raw"`
}

// An Attribution is one leaf of a composed configuration and the tracers it carries.
type Attribution struct {
	Doc     string
	Path    string
	Pos     string
	Tracers []int
}

// matches reports whether a leaf value carries tracer t.
func (t Tracer) matches(v string) bool {
	switch t.Kind {
	case "str":
		return strings.Contains(v, t.Value)
	case "int":
		return v == t.Value
	case "bytes":
		if v == t.Value {
			return true
		}
		b, err := base64.StdEncoding.DecodeString(v)
		return err == nil && strings.Contains(string(b), t.Raw)
	}
	return false
}

func carried(v string, tracers []Tracer) []int {
	var ids []int
	for _, t := range tracers {
		if t.matches(v) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// sameShape reports, as a fidelity failure, where two trees stop having the same leaves in the same
// places: the tracer pass then did not compose as the real one did and says nothing about it.
func sameShape(a, b *Tree, what string) []string {
	if len(a.Leaves) != len(b.Leaves) {
		return []string{fmt.Sprintf("%s: %d leaves against %d: the compositions differ in structure", what, len(a.Leaves), len(b.Leaves))}
	}
	for i := range a.Leaves {
		if a.Leaves[i].Pos != b.Leaves[i].Pos {
			return []string{fmt.Sprintf("%s: leaf %d is %s against %s: the compositions differ in structure", what, i, a.Leaves[i].Pos, b.Leaves[i].Pos)}
		}
	}
	return nil
}

// attribute walks the real and the trace composition in parallel. Every leaf where they differ
// must carry a tracer, and every leaf that carries one must differ; anything else is a fidelity
// failure, and the caller fails closed on it. Booleans are equal in both by construction and are
// attributed by attributeFlip.
func attribute(real, trace *Tree, tracers []Tracer) ([]Attribution, []string) {
	if f := sameShape(real, trace, "real/trace"); f != nil {
		return nil, f
	}
	var out []Attribution
	var fails []string
	for i, r := range real.Leaves {
		tr := trace.Leaves[i]
		ids := carried(tr.Node.Value, tracers)
		differs := r.Node.Value != tr.Node.Value || r.Node.Tag != tr.Node.Tag
		switch {
		case differs && len(ids) == 0:
			fails = append(fails, fmt.Sprintf("%s %s differs from the real composition and carries no tracer", r.Doc, r.Path))
		case !differs && len(ids) > 0:
			fails = append(fails, fmt.Sprintf("%s %s carries a tracer but equals the real composition", r.Doc, r.Path))
		case differs:
			out = append(out, Attribution{Doc: r.Doc, Path: r.Path, Pos: r.Pos, Tracers: ids})
		}
	}
	return out, fails
}

// attributeFlip attributes a boolean tracer: the trace composition against the same composition
// with that one boolean flipped. The leaves that differ are the boolean's.
func attributeFlip(trace, flip *Tree, t Tracer) ([]Attribution, []string) {
	if f := sameShape(trace, flip, fmt.Sprintf("trace/flip-%d", t.ID)); f != nil {
		return nil, f
	}
	var out []Attribution
	for i, a := range trace.Leaves {
		b := flip.Leaves[i]
		if a.Node.Value != b.Node.Value {
			out = append(out, Attribution{Doc: a.Doc, Path: a.Path, Pos: a.Pos, Tracers: []int{t.ID}})
		}
	}
	return out, nil
}

// findTracers lists, per tracer id, the positional keys of the leaves that carry it.
func findTracers(tr *Tree, tracers []Tracer) map[int][]string {
	out := map[int][]string{}
	for _, l := range tr.Leaves {
		for _, id := range carried(l.Node.Value, tracers) {
			out[id] = append(out[id], l.Pos)
		}
	}
	return out
}
