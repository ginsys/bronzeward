package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Case is one directory under cases/. It either names an issue-3 case (from), whose fragments
// and embedded declarations it reuses unchanged, or carries its own fragments in issue 3's
// canonical form (`!ref <name>`). It always gives its own values: synthetic, BWSYNTH- prefixed
// strings, so that every value is a leak-scan pattern.
type Case struct {
	Description    string               `yaml:"description"`
	Transformation string               `yaml:"transformation"`
	From           string               `yaml:"from"`
	Fragments      []Fragment           `yaml:"fragments"`
	Embedded       []Embedded           `yaml:"embedded"`
	Values         map[string]yaml.Node `yaml:"values"`
	Modifiers      map[string]string    `yaml:"modifiers"`
	Versions       map[string]string    `yaml:"versions"`
	Native         string               `yaml:"native"`
	Expect         Expect               `yaml:"expect"`
}

// Fragment is one ordered patch, as in issue 3.
type Fragment struct {
	File   string `yaml:"file"`
	Marked bool   `yaml:"marked"`
}

// Expect is what the case expects, written before the run: which references end up effective,
// overridden or unresolved, what each step's message becomes under the composed-path redaction,
// which representations leak one of the case's values (leak or clean), and, per candidate, the
// premise (issue 3's early outcome: parity with the reference-free composition, unless stated).
type Expect struct {
	Effective  []string          `yaml:"effective"`
	Overridden []string          `yaml:"overridden"`
	Unresolved []string          `yaml:"unresolved"`
	Messages   map[string]string `yaml:"messages"`
	Leaks      map[string]string `yaml:"leaks"`
	Premise    map[string]string `yaml:"premise"`
}

// issue3Case is the part of issue 3's case schema this experiment reads.
type issue3Case struct {
	Description string                       `yaml:"description"`
	Fragments   []Fragment                   `yaml:"fragments"`
	Embedded    []Embedded                   `yaml:"embedded"`
	Values      map[string]yaml.Node         `yaml:"values"`
	Native      string                       `yaml:"native"`
	Expect      map[string]map[string]string `yaml:"expect"`
}

// bwrefCase is the case.yaml issue 3's bwref reads, written for the real, trace and flip passes.
type bwrefCase struct {
	Description string                       `yaml:"description"`
	Native      string                       `yaml:"native"`
	Fragments   []Fragment                   `yaml:"fragments"`
	Embedded    []Embedded                   `yaml:"embedded,omitempty"`
	Values      map[string]*yaml.Node        `yaml:"values"`
	Expect      map[string]map[string]string `yaml:"expect"`
}

var candidates = []string{"tag", "marked", "binding"}

func decodeStrict(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// An occurrence is one `!ref` node of the canonical fragments.
type occurrence struct {
	index      int
	ref        string
	fragment   string
	doc, path  string
	identified bool
}

var refText = regexp.MustCompile(`!ref ([A-Za-z0-9_.-]+)`)

// allIdentified is emb with every declaration parsed: the canonical fragments are walked through
// every embedded document, as issue 3's Generate does; whether a resolver may is decided apart.
func allIdentified(emb []Embedded) []Embedded {
	out := make([]Embedded, len(emb))
	for i, e := range emb {
		e.Identified = true
		out[i] = e
	}
	return out
}

// refNodes lists the distinct `!ref` nodes of a fragment in document order.
func refNodes(text []byte, emb []Embedded) ([]Leaf, error) {
	t, err := parseTree(text, allIdentified(emb))
	if err != nil {
		return nil, err
	}
	seen := map[*yaml.Node]bool{}
	var out []Leaf
	for _, l := range t.Leaves {
		if l.Node.Tag == "!ref" && !seen[l.Node] {
			seen[l.Node] = true
			out = append(out, l)
		}
	}
	return out, nil
}

// resolvable reports whether a candidate may reach a path: every embedded document on the way
// must be identified.
func resolvable(emb []Embedded, doc, path string) bool {
	for i := 0; i < len(path); i++ {
		if path[i] != '|' {
			continue
		}
		e, ok := embeddedAt(emb, doc, path[:i])
		if !ok || !e.Identified {
			return false
		}
	}
	return true
}

// derive writes, for one case, everything the run needs besides talosctl: the real, trace and flip
// bwref case directories, the tracers, the secrets the oracle looks for, the occurrences (the
// provenance superset, with fragment digests) and info.tsv (fragments, expectations).
func derive(caseDir, issue3Dir, out string) error {
	var c Case
	if err := decodeStrict(filepath.Join(caseDir, "case.yaml"), &c); err != nil {
		return err
	}
	srcDir := caseDir
	premise := map[string]string{}
	if c.From != "" {
		srcDir = filepath.Join(issue3Dir, c.From)
		var src issue3Case
		if err := decodeStrict(filepath.Join(srcDir, "case.yaml"), &src); err != nil {
			return err
		}
		if len(c.Fragments) > 0 || len(c.Embedded) > 0 {
			return fmt.Errorf("%s: a case with from takes its fragments and embedded documents from issue 3", caseDir)
		}
		c.Fragments, c.Embedded = src.Fragments, src.Embedded
		for _, cand := range candidates {
			premise[cand] = src.Expect[cand]["early"]
		}
	}
	for _, cand := range candidates {
		if p, ok := c.Expect.Premise[cand]; ok {
			premise[cand] = p
		}
		if premise[cand] == "" {
			premise[cand] = "parity"
		}
	}
	if len(c.Fragments) == 0 || c.Native == "" || c.Transformation == "" {
		return fmt.Errorf("%s: a case needs fragments, native and transformation", caseDir)
	}
	version := func(ref string) string {
		if v := c.Versions[ref]; v != "" {
			return v
		}
		return "1"
	}
	modifier := func(ref string) string {
		if m := c.Modifiers[ref]; m != "" {
			return m
		}
		return "-"
	}
	for ref, m := range c.Modifiers {
		if m != "base64" {
			return fmt.Errorf("%s: modifier %q of %s: only base64 is defined", caseDir, m, ref)
		}
	}

	// Occurrences, and the renamed trace fragments.
	var occs []occurrence
	canonical := map[string][]byte{}
	renamed := map[string][]byte{}
	for _, f := range c.Fragments {
		text, err := os.ReadFile(filepath.Join(srcDir, f.File))
		if err != nil {
			return err
		}
		canonical[f.File] = text
		nodes, err := refNodes(text, c.Embedded)
		if err != nil {
			return fmt.Errorf("%s: %w", f.File, err)
		}
		matches := refText.FindAllSubmatchIndex(text, -1)
		if len(matches) != len(nodes) {
			return fmt.Errorf("%s: %d `!ref` texts but %d reference nodes; cannot rename them in order", f.File, len(matches), len(nodes))
		}
		var b strings.Builder
		last := 0
		var fragOccs []occurrence
		for k, m := range matches {
			name := string(text[m[2]:m[3]])
			if name != nodes[k].Node.Value {
				return fmt.Errorf("%s: reference %d is %q in the text but %q in the tree", f.File, k, name, nodes[k].Node.Value)
			}
			o := occurrence{index: len(occs) + len(fragOccs), ref: name, fragment: f.File, doc: nodes[k].Doc,
				path: nodes[k].Path, identified: resolvable(c.Embedded, nodes[k].Doc, nodes[k].Path)}
			b.Write(text[last:m[3]])
			if o.identified {
				fmt.Fprintf(&b, "~%d", o.index)
			}
			last = m[3]
			fragOccs = append(fragOccs, o)
		}
		b.Write(text[last:])
		renamed[f.File] = []byte(b.String())
		// The renamed text must name the same nodes, in the same places.
		check, err := refNodes(renamed[f.File], c.Embedded)
		if err != nil || len(check) != len(fragOccs) {
			return fmt.Errorf("%s: the renamed fragment does not parse to the same references: %v", f.File, err)
		}
		for k, o := range fragOccs {
			want := o.ref
			if o.identified {
				want = fmt.Sprintf("%s~%d", o.ref, o.index)
			}
			if check[k].Node.Value != want || check[k].Path != o.path {
				return fmt.Errorf("%s: renamed reference %d is %s at %s, want %s at %s", f.File, k, check[k].Node.Value, check[k].Path, want, o.path)
			}
		}
		occs = append(occs, fragOccs...)
	}

	// Values: the real (placed) values, the tracers, and the secrets.
	realValues := map[string]*yaml.Node{}
	traceValues := map[string]*yaml.Node{}
	placeholder := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "zq-unresolved"}
	var secrets []Secret
	refs := make([]string, 0, len(c.Values))
	for r := range c.Values {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, r := range refs {
		v := c.Values[r]
		p, err := placed(&v, modifier(r))
		if err != nil {
			return fmt.Errorf("%s: value %s: %w", caseDir, r, err)
		}
		realValues[r] = p
		traceValues[r] = placeholder
	}
	for _, o := range occs {
		if _, ok := c.Values[o.ref]; !ok {
			return fmt.Errorf("%s: %s %s: no value for reference %q", caseDir, o.fragment, o.path, o.ref)
		}
	}
	for _, r := range refs {
		v := c.Values[r]
		key := ""
		for _, o := range occs {
			if o.ref == r {
				key = lastKey(o.path)
				break
			}
		}
		s, err := secretsOf(&v, r, version(r), modifier(r), key)
		if err != nil {
			return fmt.Errorf("%s: value %s: %w", caseDir, r, err)
		}
		secrets = append(secrets, s...)
	}
	var tracers []Tracer
	for _, o := range occs {
		if !o.identified {
			continue
		}
		v := c.Values[o.ref]
		n, ts, err := traceValue(&v, o, version(o.ref), modifier(o.ref), len(tracers), "")
		if err != nil {
			return fmt.Errorf("%s: value %s: %w", caseDir, o.ref, err)
		}
		traceValues[fmt.Sprintf("%s~%d", o.ref, o.index)] = n
		tracers = append(tracers, ts...)
	}

	expect := map[string]map[string]string{}
	for _, cand := range candidates {
		expect[cand] = map[string]string{"early": premise[cand], "late": "unmeasured"}
	}
	writeCase := func(dir string, frags map[string][]byte, values map[string]*yaml.Node) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for name, text := range frags {
			if err := os.WriteFile(filepath.Join(dir, name), text, 0o644); err != nil {
				return err
			}
		}
		b, err := yaml.Marshal(bwrefCase{Description: c.Description, Native: c.Native, Fragments: c.Fragments,
			Embedded: c.Embedded, Values: values, Expect: expect})
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "case.yaml"), b, 0o644)
	}
	if err := writeCase(filepath.Join(out, "real", "case"), canonical, realValues); err != nil {
		return err
	}
	if err := writeCase(filepath.Join(out, "trace", "case"), renamed, traceValues); err != nil {
		return err
	}
	var flips []int
	for _, t := range tracers {
		if t.Kind != "bool" {
			continue
		}
		flips = append(flips, t.ID)
		fv := map[string]*yaml.Node{}
		for k, v := range traceValues {
			fv[k] = v
		}
		for _, o := range occs {
			if o.index != t.Occ {
				continue
			}
			v := c.Values[o.ref]
			n, _, err := traceValue(&v, o, version(o.ref), modifier(o.ref), tracersBefore(tracers, o.index), fmt.Sprint(t.ID))
			if err != nil {
				return err
			}
			fv[fmt.Sprintf("%s~%d", o.ref, o.index)] = n
		}
		if err := writeCase(filepath.Join(out, fmt.Sprintf("flip-%d", t.ID), "case"), renamed, fv); err != nil {
			return err
		}
	}

	if err := writeJSON(filepath.Join(out, "tracers.json"), tracers); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(out, "secrets.json"), secrets); err != nil {
		return err
	}
	var ob strings.Builder
	ob.WriteString("occurrence\tref\tversion\tmodifier\tfragment\tfragment-sha256\tdoc\tpath\tresolvable\n")
	for _, o := range occs {
		sum := sha256.Sum256(canonical[o.fragment])
		r := "yes"
		if !o.identified {
			r = "no"
		}
		fmt.Fprintf(&ob, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", o.index, o.ref, version(o.ref), modifier(o.ref), o.fragment,
			hex.EncodeToString(sum[:]), o.doc, o.path, r)
	}
	if err := os.WriteFile(filepath.Join(out, "occurrences.tsv"), []byte(ob.String()), 0o644); err != nil {
		return err
	}
	var ib strings.Builder
	for _, f := range c.Fragments {
		fmt.Fprintf(&ib, "fragment\t%s\n", f.File)
	}
	fmt.Fprintf(&ib, "native\t%s\ntransformation\t%s\n", c.Native, c.Transformation)
	for _, id := range flips {
		fmt.Fprintf(&ib, "flip\t%d\n", id)
	}
	for _, cand := range candidates {
		fmt.Fprintf(&ib, "premise\t%s\t%s\n", cand, premise[cand])
	}
	list := func(s []string) string {
		if len(s) == 0 {
			return "-"
		}
		t := append([]string(nil), s...)
		sort.Strings(t)
		return strings.Join(t, ",")
	}
	fmt.Fprintf(&ib, "expect\teffective\t%s\nexpect\toverridden\t%s\nexpect\tunresolved\t%s\n",
		list(c.Expect.Effective), list(c.Expect.Overridden), list(c.Expect.Unresolved))
	for _, k := range sortedKeys(c.Expect.Messages) {
		fmt.Fprintf(&ib, "expect\tmessage.%s\t%s\n", k, c.Expect.Messages[k])
	}
	for _, k := range sortedKeys(c.Expect.Leaks) {
		fmt.Fprintf(&ib, "expect\tleak.%s\t%s\n", k, c.Expect.Leaks[k])
	}
	return os.WriteFile(filepath.Join(out, "info.tsv"), []byte(ib.String()), 0o644)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func tracersBefore(tracers []Tracer, occ int) int {
	for i, t := range tracers {
		if t.Occ == occ {
			return i
		}
	}
	return len(tracers)
}

func lastKey(path string) string {
	i := strings.LastIndexAny(path, "/|")
	return path[i+1:]
}

// placed is a value as the configuration receives it: a base64 modifier encodes a string.
func placed(v *yaml.Node, modifier string) (*yaml.Node, error) {
	n := cloneNode(v)
	if modifier == "base64" {
		if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
			return nil, fmt.Errorf("base64 needs a string value")
		}
		n.Value = base64.StdEncoding.EncodeToString([]byte(n.Value))
	}
	normalizeStyle(n)
	return n, nil
}

// normalizeStyle leaves the quoting of a value to the encoder, as for any value talosctl writes, and
// writes a multi-line string as a literal block: the real and the trace value are then written
// alike, whatever style the case file used.
func normalizeStyle(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode {
		n.Style = 0
		if strings.Contains(n.Value, "\n") {
			n.Style = yaml.LiteralStyle
		}
	}
	for _, c := range n.Content {
		normalizeStyle(c)
	}
}

func cloneNode(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, k := range n.Content {
		c.Content[i] = cloneNode(k)
	}
	return &c
}

// scalarLeaves calls f for every scalar under a value, with its key path inside the value.
func scalarLeaves(n *yaml.Node, at string, f func(n *yaml.Node, leaf string) error) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return f(n, at)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := scalarLeaves(n.Content[i+1], join(at, n.Content[i].Value), f); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("a %v value is not supported", n.Kind)
}

func kindOf(n *yaml.Node, modifier string) (string, error) {
	switch n.ShortTag() {
	case "!!str":
		if modifier == "base64" {
			return "bytes", nil
		}
		return "str", nil
	case "!!int":
		return "int", nil
	case "!!bool":
		return "bool", nil
	}
	return "", fmt.Errorf("a %s value is not supported", n.ShortTag())
}

// traceValue is an occurrence's trace value: a tracer per leaf, ids from first on. With flip set,
// the boolean tracer of that id is negated (its flip pass) and every other leaf is as in the trace.
func traceValue(v *yaml.Node, o occurrence, version, modifier string, first int, flip string) (*yaml.Node, []Tracer, error) {
	n := cloneNode(v)
	var ts []Tracer
	id := first
	err := scalarLeaves(n, "", func(s *yaml.Node, leaf string) error {
		kind, err := kindOf(s, modifier)
		if err != nil {
			return err
		}
		t := Tracer{ID: id, Occ: o.index, Ref: o.ref, Version: version, Leaf: leaf, Kind: kind}
		switch kind {
		case "str":
			t.Value = shapeTracer(s.Value, id)
			s.Value = t.Value
		case "bytes":
			t.Raw = shapeTracer(s.Value, id)
			t.Value = base64.StdEncoding.EncodeToString([]byte(t.Raw))
			s.Value = t.Value
		case "int":
			t.Value = intTracer(id)
			s.Value = t.Value
		case "bool":
			t.Value = s.Value
			if flip == fmt.Sprint(id) {
				if s.Value == "true" {
					s.Value = "false"
				} else {
					s.Value = "true"
				}
			}
		}
		ts = append(ts, t)
		id++
		return nil
	})
	normalizeStyle(n)
	return n, ts, err
}

// secretsOf lists what the oracle looks for in one reference's value: each leaf, as returned and as
// placed. key is the mapping key the reference sits under, for a scalar that is not a string.
func secretsOf(v *yaml.Node, ref, version, modifier, key string) ([]Secret, error) {
	var out []Secret
	err := scalarLeaves(v, "", func(s *yaml.Node, leaf string) error {
		kind, err := kindOf(s, modifier)
		if err != nil {
			return err
		}
		id := ref + "@" + version
		k := key
		if leaf != "" {
			id += "#" + leaf
			k = lastKey(leaf)
		}
		sec := Secret{ID: id, Class: "ref", Kind: kind, Value: s.Value}
		if kind == "bytes" {
			sec.Placed = base64.StdEncoding.EncodeToString([]byte(s.Value))
		}
		if kind == "int" || kind == "bool" {
			sec.Key = k
		}
		out = append(out, sec)
		return nil
	})
	return out, err
}
