package compile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/validation"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The machinery gate (compilation.md §10.1): SR's matrix re-run with composition through this
// package. experiments/c10-machinery-gate/run/all prepares C10_OUT (the two bases, SR's
// generated fragment forms, and talosctl's own composition and verdict of each case's literal
// form) and runs this test there; without C10_OUT it is skipped.

// srCases is SR's case directory, from this package's directory.
const srCases = "../../experiments/e2-structural-references/cases"

// authoringRefused are the SR cases bronzeward refuses at authoring by contract: a reference
// look-alike in a literal and a reference inside unidentified embedded text are reserved text
// (compilation.md §5.5, choice §16.19).
var authoringRefused = map[string]bool{"collision": true, "unidentified-embedded": true}

type srCase struct {
	Fragments []struct {
		File string `yaml:"file"`
	} `yaml:"fragments"`
	Embedded []struct {
		Doc        string `yaml:"doc"`
		Path       string `yaml:"path"`
		Format     string `yaml:"format"`
		Identified bool   `yaml:"identified"`
	} `yaml:"embedded"`
	Values map[string]yaml.Node         `yaml:"values"`
	Native string                       `yaml:"native"`
	Expect map[string]map[string]string `yaml:"expect"`
}

var gateBases = []struct {
	name string
	mode Mode
}{{"fixture", ModeContainer}, {"generated", ModeMetal}}

func TestMachineryGate(t *testing.T) {
	out := os.Getenv("C10_OUT")
	if out == "" {
		t.Skip("set by experiments/c10-machinery-gate/run/all")
	}
	entries, err := os.ReadDir(srCases)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	matrix := [][]string{{"base", "case", "talosctl", "machinery", "bytes", "expected", "observed", "stage", "validate", "native-validate", "rule"}}
	var controls [][]string
	unexpected := 0
	for _, b := range gateBases {
		base, err := os.ReadFile(filepath.Join(out, "base", b.name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		resolvedBase, _, err := gateResolve(base, ingest.Declarations{}, nil)
		if err != nil {
			t.Fatalf("control: the %s base does not ingest and resolve: %v", b.name, err)
		}
		// Controls: the base alone, through ingestion and the compiler, is the base talosctl
		// normalized, and validates in its mode.
		m, err := Compose(resolvedBase, nil)
		if err != nil || !bytes.Equal(m.bytes(), base) {
			t.Fatalf("control: the %s base does not round-trip through the compiler: %v", b.name, err)
		}
		controls = append(controls, []string{"control", b.name, "compiler-round-trip", "identical"})
		if err := m.Validate(b.mode); err != nil {
			t.Fatalf("control: the %s base does not validate in %s mode: %v", b.name, b.mode, err)
		}
		controls = append(controls, []string{"control", b.name, "compiler-validates-" + string(b.mode), "pass"})
		for _, name := range names {
			row := gateCase(t, out, b.name, b.mode, base, resolvedBase, name)
			if row.unexpected() {
				unexpected++
			}
			matrix = append(matrix, row.cells())
		}
	}
	rows := len(matrix) - 1
	if rows != len(names)*len(gateBases) {
		t.Fatalf("matrix has %d rows for %d cases on %d bases", rows, len(names), len(gateBases))
	}
	summary := [][]string{
		{"cases", strconv.Itoa(len(names))},
		{"bases", "fixture generated"},
		{"rows", strconv.Itoa(rows)},
		{"unexpected", strconv.Itoa(unexpected)},
	}
	for _, f := range []struct {
		name string
		rows [][]string
	}{{"matrix.tsv", matrix}, {"controls.tsv", controls}, {"summary.txt", summary}} {
		if err := os.WriteFile(filepath.Join(out, f.name), []byte(tsv(f.rows)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if unexpected > 0 {
		t.Errorf("%d unexpected rows; see %s", unexpected, filepath.Join(out, "matrix.tsv"))
	}
}

type gateRow struct {
	base, name                 string
	talosctl, machinery, bytes string // native composition of the literal form: pass, invalid or rejected; same or differs
	expected, observed, stage  string // the compiler on the tag form
	validate, nativeValidate   string
	rule                       string
	refusal, wantRefusal       ingest.Rule // ingestion's refusal rule, and the one the contract requires
	machineryVerdictDisagrees  bool
}

// unexpected: the machinery's native composition differs from talosctl's in outcome or bytes, or
// the compiler's outcome is not the expected one, or a parity output's verdict differs, or an
// expected authoring refusal came from another stage or rule.
func (r gateRow) unexpected() bool {
	return r.talosctl != r.machinery || r.bytes == "differs" || r.machineryVerdictDisagrees ||
		r.expected != r.observed || r.observed == "parity" && r.validate != r.nativeValidate ||
		r.wantRefusal != "" && (r.stage != "authoring" || r.refusal != r.wantRefusal)
}

func (r gateRow) cells() []string {
	c := []string{r.base, r.name, r.talosctl, r.machinery, r.bytes, r.expected, r.observed, r.stage, r.validate, r.nativeValidate, r.rule}
	for i, s := range c {
		if s == "" {
			c[i] = "-"
		}
	}
	return c
}

func gateCase(t *testing.T, out, baseName string, mode Mode, base []byte, resolvedBase ingest.Resolved, name string) gateRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(srCases, name, "case.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c srCase
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	row := gateRow{base: baseName, name: name, expected: c.Expect["tag"]["early"]}
	if authoringRefused[name] {
		row.expected, row.wantRefusal = "refused", ingest.RuleReservedText
	}

	// talosctl's native composition and verdict, as run/all recorded them.
	dir := filepath.Join(out, "cases", baseName, name, "native")
	outcome, err := os.ReadFile(filepath.Join(dir, "outcome"))
	if err != nil {
		t.Fatal(err)
	}
	row.talosctl = strings.TrimSpace(string(outcome))
	var talosOut []byte
	if row.talosctl != "rejected" {
		if talosOut, err = os.ReadFile(filepath.Join(dir, "out.yaml")); err != nil {
			t.Fatal(err)
		}
		row.nativeValidate = map[string]string{"pass": "pass", "invalid": "fail"}[row.talosctl]
	}

	// The machinery's native composition of the same literal fragments.
	var literal [][]byte
	for _, f := range c.Fragments {
		b, err := os.ReadFile(filepath.Join(out, "gen", name, "literal", f.File))
		if err != nil {
			t.Fatal(err)
		}
		literal = append(literal, b)
	}
	machineryOut, ok := nativeCompose(base, literal)
	switch {
	case !ok:
		row.machinery = "rejected"
	case nativeValid(machineryOut, mode):
		row.machinery = "pass"
	default:
		row.machinery = "invalid"
	}
	if ok && talosOut != nil {
		row.bytes = map[bool]string{true: "same", false: "differs"}[bytes.Equal(machineryOut, talosOut)]
	}

	// The compiler on the tag form: each fragment ingested as bronzeward ingests it (schema
	// secrets extracted, references declared, identified embedded documents declared), then
	// resolved, composed and validated.
	values := map[string]provider.Value{}
	for ref, n := range c.Values {
		k, v := caseValue(t, n)
		values[ref] = value(t, k, v)
	}
	var fragments []ingest.Resolved
	for i, f := range c.Fragments {
		text, err := os.ReadFile(filepath.Join(out, "gen", name, "tag", f.File))
		if err != nil {
			t.Fatal(err)
		}
		decl := ingest.Declarations{References: map[string]ingest.Reference{}}
		for ref := range c.Values {
			if referenced(text, ref) {
				decl.References[ref] = ingest.Reference{Kind: values[ref].Kind(), Version: 1}
			}
		}
		for _, e := range c.Embedded {
			if !e.Identified {
				continue
			}
			if p, ok := embeddedPath(t, text, e.Doc, e.Path); ok {
				decl.Embedded = append(decl.Embedded, ingest.Embedded{Path: p, Format: e.Format})
			}
		}
		r, stage, err := gateResolve(text, decl, values)
		if err != nil {
			row.observed, row.stage, row.rule = "refused", stage, fmt.Sprintf("fragment[%d] error", i)
			var refusal *ingest.Refusal
			if errors.As(err, &refusal) {
				row.refusal, row.rule = refusal.Rule, fmt.Sprintf("fragment[%d] %s", i, refusal.Rule)
			}
			return row
		}
		fragments = append(fragments, r)
	}
	m, err := Compose(resolvedBase, fragments)
	var e *Error
	switch {
	case errors.As(err, &e) && e.Rule == RuleRejected:
		row.observed, row.stage, row.rule = "rejected", "compose", e.Input
		return row
	case errors.As(err, &e):
		row.observed, row.stage, row.rule = "refused", "output", string(e.Rule)
		return row
	case err != nil:
		row.observed, row.stage, row.rule = "refused", "compose", "error"
		return row
	}
	if talosOut != nil && bytes.Equal(m.bytes(), talosOut) {
		row.observed = "parity"
	} else {
		row.observed = "differs"
	}
	row.validate = "pass"
	if err := m.Validate(mode); err != nil {
		row.validate = "fail"
	}
	row.machineryVerdictDisagrees = ok && talosOut != nil && (row.machinery == "pass") != (row.talosctl == "pass")
	return row
}

// gateResolve ingests text and resolves it with values plus whatever ingestion extracts. A failure
// names its stage: authoring (reading, extraction, staging) or resolve.
func gateResolve(text []byte, decl ingest.Declarations, values map[string]provider.Value) (ingest.Resolved, string, error) {
	s, all, err := gateIngest(text, nil, decl, values)
	if err != nil {
		return ingest.Resolved{}, "authoring", err
	}
	r, err := ingest.Resolve(s, all)
	if err != nil {
		return ingest.Resolved{}, "resolve", err
	}
	return r, "", nil
}

// gateIngest ingests text with marks and declarations as bronzeward does (compilation.md §2.3):
// the sanitized text, and values plus every value ingestion extracted, by reference name.
func gateIngest(text []byte, marks []ingest.Path, decl ingest.Declarations, values map[string]provider.Value) (ingest.Sanitized, map[string]provider.Value, error) {
	u, err := ingest.Read(bytes.NewReader(text), 1<<20)
	if err != nil {
		return ingest.Sanitized{}, nil, err
	}
	c, err := ingest.Extract(ingest.Request{Input: u, Marks: marks, Declarations: decl})
	if err != nil {
		return ingest.Sanitized{}, nil, err
	}
	all := maps.Clone(values)
	if all == nil {
		all = map[string]provider.Value{}
	}
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		all[name] = v
		return nil
	})
	if err != nil {
		return ingest.Sanitized{}, nil, err
	}
	return s, all, nil
}

// nativeCompose is talosctl machineconfig patch's composition through the machinery.
func nativeCompose(base []byte, fragments [][]byte) ([]byte, bool) {
	var patches []configpatcher.Patch
	for _, f := range fragments {
		p, err := configpatcher.LoadPatch(f)
		if err != nil {
			return nil, false
		}
		patches = append(patches, p)
	}
	o, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		return nil, false
	}
	cfg, err := o.Config()
	if err != nil {
		return nil, false
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	return b, err == nil
}

func nativeValid(b []byte, mode Mode) bool {
	cfg, err := configloader.NewFromBytes(b)
	if err != nil {
		return false
	}
	_, err = cfg.Validate(mode, validation.WithLocal(), validation.WithStrict())
	return err == nil
}

// referenced: text holds a reference to ref, in the document or inside embedded text.
func referenced(text []byte, ref string) bool {
	return regexp.MustCompile(`!bwref[ \t]+` + regexp.QuoteMeta(ref) + `([^A-Za-z0-9._-]|$)`).Match(text)
}

// caseValue is an SR case value as a provider kind and Go value.
func caseValue(t *testing.T, n yaml.Node) (provider.Kind, any) {
	t.Helper()
	scalar := func(s *yaml.Node) (provider.Kind, any) {
		switch s.ShortTag() {
		case "!!int":
			return provider.KindInteger, json.Number(s.Value)
		case "!!bool":
			return provider.KindBoolean, s.Value == "true"
		case "!!str":
			return provider.KindString, s.Value
		}
		t.Fatalf("an SR value of tag %s", s.ShortTag())
		return "", nil
	}
	if n.Kind != yaml.MappingNode {
		return scalar(&n)
	}
	m := map[string]any{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		_, v := scalar(n.Content[i+1])
		m[n.Content[i].Value] = v
	}
	return provider.KindMapping, m
}

// embeddedPath converts an SR embedded path (a document name, then keys and name[field=value]
// list selectors) into a compilation.md §2.2 path in text, if text holds it.
func embeddedPath(t *testing.T, text []byte, docName, srPath string) (string, bool) {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(text))
	for doc := 0; ; doc++ {
		var d yaml.Node
		if err := dec.Decode(&d); err != nil {
			return "", false
		}
		n := d.Content[0]
		kind := mappingValue(n, "kind")
		if docName == "v1alpha1" && kind != nil || docName != "v1alpha1" && (kind == nil || kind.Value != docName) {
			continue
		}
		p := fmt.Sprintf("doc[%d]", doc)
		for _, seg := range srSegments(t, srPath) {
			if n = mappingValue(n, seg.key); n == nil {
				return "", false
			}
			p += "/" + strings.ReplaceAll(strings.ReplaceAll(seg.key, "~", "~0"), "/", "~1")
			if seg.field == "" {
				continue
			}
			i := slices.IndexFunc(n.Content, func(e *yaml.Node) bool {
				f := mappingValue(e, seg.field)
				return f != nil && f.Value == seg.value
			})
			if n.Kind != yaml.SequenceNode || i < 0 {
				return "", false
			}
			n, p = n.Content[i], p+"/"+strconv.Itoa(i)
		}
		return p, n.Kind == yaml.ScalarNode
	}
}

type srSegment struct{ key, field, value string }

// srSegments splits an SR path; a selector's value may hold a slash.
func srSegments(t *testing.T, s string) []srSegment {
	t.Helper()
	var out []srSegment
	for s != "" {
		end := strings.IndexAny(s, "/[")
		if end < 0 {
			out = append(out, srSegment{key: s})
			break
		}
		seg := srSegment{key: s[:end]}
		if s[end] == '[' {
			closing := strings.IndexByte(s[end:], ']')
			if closing < 0 {
				t.Fatalf("SR path %q", s)
			}
			seg.field, seg.value, _ = strings.Cut(s[end+1:end+closing], "=")
			end += closing + 1
		}
		out = append(out, seg)
		s = strings.TrimPrefix(s[end:], "/")
	}
	return out
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func tsv(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteByte('\n')
	}
	return b.String()
}
