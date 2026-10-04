package compile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The machinery gate's SP half (compilation.md §10.1, §15): SP's matrix re-run with composition,
// provenance and redaction through this package. experiments/c10-machinery-gate/run/all prepares
// C10_OUT/sp (each SP case's real pass as SP derives it, its literal and tag forms, and talosctl's
// composition and verdict of the literal form on each base) and runs this test; without C10_OUT it
// is skipped.
//
// Per case and base, two cells:
//
//	fragments  the case as SP ran it: references in fragments. Compile on the tag form must reach
//	           talosctl's outcome (parity with its bytes), SP's effective and overridden references
//	           at the case's versions, and SP's message outcomes.
//	base-ref   the case's fragments up to the first holding a case reference moved into the import
//	           base: their composition with the base, ingested with a mark at each output path
//	           provenance gave their references (a mapping marked whole; the way a reference reaches
//	           an import base, §2.3), then the remaining fragments. It must reach the fragments cell's outcome and bytes, unless a later
//	           fragment overrides what moved into the base: that is refused (§6 step 7, choice §16.20).
//
// In every cell, SP's oracle must find none of the case's values and none of the base bundle's
// secrets in anything the compiler renders: the redacted configuration, the error (as the server
// log prints it, %v, and under %+v and %#v), the compiled value under every verb, and the
// provenance, effective and reproduction records under %+v and as JSON.

// spCases is SP's case directory, from this package's directory.
const spCases = "../../experiments/e2-sensitivity-provenance/cases"

type spCase struct {
	Values    map[string]yaml.Node `yaml:"values"`
	Modifiers map[string]string    `yaml:"modifiers"`
	Versions  map[string]string    `yaml:"versions"`
	Native    string               `yaml:"native"`
	Expect    struct {
		Effective  []string          `yaml:"effective"`
		Overridden []string          `yaml:"overridden"`
		Unresolved []string          `yaml:"unresolved"`
		Messages   map[string]string `yaml:"messages"`
	} `yaml:"expect"`
}

// spByContract are the SP cases whose outcome bronzeward's contract fixes otherwise than SP's
// premise, with the stage and rule it must come from.
var spByContract = map[string]struct {
	stage string
	rule  string
}{
	// A reference look-alike in a literal and a reference inside unidentified embedded text are
	// reserved text (compilation.md §5.5, choice §16.19).
	"collision":             {"authoring", string(ingest.RuleReservedText)},
	"unidentified-embedded": {"authoring", string(ingest.RuleReservedText)},
	// A value also written as a literal is an exact copy (§6 step 7, choice §16.21): SP's
	// duplicate-literal case is the evidence that only value matching sees it.
	"duplicate-literal": {"output", string(RuleCopy)},
	// A literal overriding one member of a mapping reference writes that member's key, a value the
	// provider holds (§4.2), in plaintext beside no attributed member: a copy too. The remedy is to
	// override the whole mapping.
	"map-partial": {"output", string(RuleCopy)},
}

type spCell struct {
	base, name, cell                     string
	talosctl, machinery, bytes           string
	expected, observed, stage, rule      string
	wantStage, wantRule                  string
	effective, wantEffective             string
	overridden, wantOverridden, versions string
	patch, wantPatch                     string
	validate, wantValidate               string
	findings, note                       string
	nativeMismatch, machineryDisagrees   bool
	notApplicable                        bool
	out                                  []byte   // the compiler's composition, parity cells only
	outputs                              []string // the case references' output paths, sorted
	records                              []Record // the case references' provenance records
}

func (c spCell) unexpected() bool {
	if c.notApplicable {
		return false
	}
	return c.talosctl != c.machinery || c.bytes == "differs" || c.machineryDisagrees || c.nativeMismatch ||
		c.expected != c.observed || c.wantRule != "" && (c.stage != c.wantStage || c.rule != c.wantRule) ||
		c.effective != c.wantEffective || c.overridden != c.wantOverridden || c.versions == "differs" ||
		!messageMeets(c.wantPatch, c.patch) || !messageMeets(c.wantValidate, c.validate) || c.findings != ""
}

func (c spCell) cells() []string {
	s := []string{c.base, c.name, c.cell, c.talosctl, c.machinery, c.bytes, c.expected, c.observed, c.stage, c.rule,
		c.effective, c.wantEffective, c.overridden, c.wantOverridden, c.versions, c.patch, c.wantPatch,
		c.validate, c.wantValidate, c.findings, c.note}
	for i, v := range s {
		if v == "" {
			s[i] = "-"
		}
	}
	return s
}

// messageMeets: SP's expectation for a step's message is met by the observed outcome. SP's
// redacted is met by a withheld message as well: compilation.md §8.3 withholds what the trace
// pass cannot redact, and neither shows a value.
func messageMeets(want, got string) bool {
	return want == got || want == "redacted" && got == "withheld"
}

func TestSensitivityGate(t *testing.T) {
	out := os.Getenv("C10_OUT")
	if out == "" {
		t.Skip("set by experiments/c10-machinery-gate/run/all")
	}
	entries, err := os.ReadDir(spCases)
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
	matrix := [][]string{{"base", "case", "cell", "talosctl", "machinery", "bytes", "expected", "observed", "stage", "rule",
		"effective", "want-effective", "overridden", "want-overridden", "versions", "patch", "want-patch",
		"validate", "want-validate", "findings", "note"}}
	var controls [][]string
	unexpected, applicable, notApplicable := 0, 0, 0
	for _, b := range gateBases {
		base, err := os.ReadFile(filepath.Join(out, "base", b.name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		bundle := baseSecrets(t, filepath.Join(out, "base", b.name+".secrets.tsv"))
		baseText, baseValues, err := gateIngest(base, nil, ingest.Declarations{}, nil)
		if err != nil {
			t.Fatalf("control: the %s base does not ingest: %v", b.name, err)
		}
		baseSrc := gateSource("base", base, baseText, baseValues)
		cells := map[string]spCell{}
		for _, name := range names {
			fc, bc := spRun(t, out, b.name, b.mode, base, baseSrc, bundle, name)
			cells[name] = fc
			for _, c := range []spCell{fc, bc} {
				if c.unexpected() {
					unexpected++
				}
				if c.cell == "base-ref" {
					if c.notApplicable {
						notApplicable++
					} else {
						applicable++
					}
				}
				matrix = append(matrix, c.cells())
			}
		}
		// The revision pairs: rotation moves the effective version on (each cell holds its
		// version to the case's), a move moves the output path.
		for _, p := range []struct{ name, a, b string }{{"moved", "moved-r1", "moved-r2"}, {"rotate", "rotate-r1", "rotate-r2"}} {
			r := "pass"
			switch {
			case cells[p.a].unexpected() || cells[p.b].unexpected():
				r = "fail"
			case p.name == "moved" && slices.Equal(cells[p.a].outputs, cells[p.b].outputs):
				r = "fail"
			case p.name == "rotate" && cells[p.a].versions == cells[p.b].versions:
				r = "fail"
			}
			if r != "pass" {
				unexpected++
			}
			controls = append(controls, []string{"pair", b.name, p.name, r})
		}
		// The oracle is not vacuous: a value planted in a scanned text is found.
		planted := false
		for _, name := range names {
			if c := cells[name]; c.observed == "parity" {
				s := spSecrets(t, name, nil)
				planted = len(oracleScan(string(c.out)+s[0].value, s, "")) > 0
				break
			}
		}
		controls = append(controls, []string{"control", b.name, "oracle-finds-planted-value", map[bool]string{true: "pass", false: "fail"}[planted]})
		if !planted {
			unexpected++
		}
	}
	rows := len(matrix) - 1
	if rows != 2*len(names)*len(gateBases) {
		t.Fatalf("matrix has %d rows for %d cases on %d bases", rows, len(names), len(gateBases))
	}
	summary := [][]string{
		{"cases", strconv.Itoa(len(names))},
		{"bases", "fixture generated"},
		{"rows", strconv.Itoa(rows)},
		{"base-ref-applicable", strconv.Itoa(applicable)},
		{"base-ref-not-applicable", strconv.Itoa(notApplicable)},
		{"unexpected", strconv.Itoa(unexpected)},
	}
	for _, f := range []struct {
		name string
		rows [][]string
	}{{"sp-matrix.tsv", matrix}, {"sp-controls.tsv", controls}, {"sp-summary.txt", summary}} {
		if err := os.WriteFile(filepath.Join(out, f.name), []byte(tsv(f.rows)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if unexpected > 0 {
		t.Errorf("%d unexpected rows; see %s", unexpected, filepath.Join(out, "sp-matrix.tsv"))
	}
}

// spRun runs one SP case on one base: its fragments cell and its base-ref cell.
func spRun(t *testing.T, out, baseName string, mode Mode, base []byte, baseSrc Source, bundle []secret, name string) (spCell, spCell) {
	t.Helper()
	var c spCase
	readYAML(t, filepath.Join(spCases, name, "case.yaml"), &c)
	dir := filepath.Join(out, "sp", name)
	var real srCase // the real pass as SP derived it: fragments and embedded documents
	readYAML(t, filepath.Join(dir, "derived", "real", "case", "case.yaml"), &real)

	fc := spCell{base: baseName, name: name, cell: "fragments"}
	bc := spCell{base: baseName, name: name, cell: "base-ref"}

	// talosctl's composition and verdict, as run/all recorded them, and the machinery's own.
	nat := filepath.Join(dir, baseName, "native")
	outcome, err := os.ReadFile(filepath.Join(nat, "outcome"))
	if err != nil {
		t.Fatal(err)
	}
	fc.talosctl = strings.TrimSpace(string(outcome))
	fc.nativeMismatch = c.Native != fc.talosctl
	var talosOut []byte
	if fc.talosctl != "rejected" {
		if talosOut, err = os.ReadFile(filepath.Join(nat, "out.yaml")); err != nil {
			t.Fatal(err)
		}
	}
	var literal [][]byte
	public := string(base)
	for _, f := range real.Fragments {
		literal = append(literal, readFile(t, filepath.Join(dir, "gen", "literal", f.File)))
		public += "\n" + string(readFile(t, filepath.Join(dir, "derived", "real", "case", f.File)))
	}
	machineryOut, ok := nativeCompose(base, literal)
	switch {
	case !ok:
		fc.machinery = "rejected"
	case nativeValid(machineryOut, mode):
		fc.machinery = "pass"
	default:
		fc.machinery = "invalid"
	}
	if ok && talosOut != nil {
		fc.bytes = map[bool]string{true: "same", false: "differs"}[string(machineryOut) == string(talosOut)]
	}
	fc.machineryDisagrees = ok && talosOut != nil && (fc.machinery == "pass") != (fc.talosctl == "pass")
	nativePatch, nativeValidate := nativeMessages(base, literal, mode)

	// Expectations.
	fc.expected = map[string]string{"pass": "parity", "invalid": "invalid", "rejected": "rejected"}[fc.talosctl]
	fc.wantPatch = c.Expect.Messages["patch"]
	fc.wantValidate = c.Expect.Messages["validate"]
	if fc.wantValidate == "verbatim" && fc.talosctl == "pass" {
		fc.wantValidate = "none" // a configuration that validates has no message to show
	}
	if bc, ok := spByContract[name]; ok {
		// A refusal before composition runs neither step.
		fc.expected, fc.wantStage, fc.wantRule = "refused", bc.stage, bc.rule
		fc.wantPatch, fc.wantValidate = "skipped", "skipped"
	}

	// The fragments, ingested as bronzeward ingests them.
	values := map[string]provider.Value{}
	for ref, n := range c.Values {
		k, v := caseValue(t, n)
		values[ref] = value(t, k, v)
	}
	var frags []Source
	for i, f := range real.Fragments {
		text := readFile(t, filepath.Join(dir, "gen", "tag", f.File))
		decl := ingest.Declarations{References: map[string]ingest.Reference{}}
		for ref := range c.Values {
			if referenced(text, ref) {
				decl.References[ref] = ingest.Reference{Kind: values[ref].Kind(), Version: spVersion(c, ref), Encoding: c.Modifiers[ref]}
			}
		}
		for _, e := range real.Embedded {
			if !e.Identified {
				continue
			}
			if p, ok := embeddedPath(t, text, e.Doc, e.Path); ok {
				decl.Embedded = append(decl.Embedded, ingest.Embedded{Path: p, Format: e.Format})
			}
		}
		s, all, err := gateIngest(text, nil, decl, values)
		if err != nil {
			fc.observed, fc.stage, fc.rule = "refused", "authoring", fmt.Sprintf("fragment[%d] error", i)
			var refusal *ingest.Refusal
			if errors.As(err, &refusal) {
				fc.rule = string(refusal.Rule)
			}
			fc.patch, fc.validate = "skipped", "skipped"
			fc.findings = spFindings(spSecrets(t, name, bundle), public, spErrorTexts(err))
			bc.notApplicable, bc.note = true, "the fragments cell is refused at authoring"
			return fc, bc
		}
		frags = append(frags, gateSource(f.File, text, s, all))
	}

	secrets := spSecrets(t, name, bundle)
	compiled, err := Compile(Input{Base: baseSrc, Fragments: frags, Mode: mode})
	fc.classify(compiled, err, talosOut, nativePatch, nativeValidate)
	fc.findings = spFindings(secrets, public, spTexts(t, compiled, err))
	if err == nil {
		fc.provenance(c, compiled)
	}
	bc = spBaseRef(t, bc, fc, real, mode, baseSrc, frags, secrets, public)
	return fc, bc
}

// classify sets the cell's observed outcome, stage, rule and message outcomes from Compile.
func (c *spCell) classify(compiled Compiled, err error, talosOut []byte, nativePatch, nativeValidate string) {
	c.patch, c.validate = "none", "none"
	var e *Error
	switch {
	case err == nil && talosOut != nil && string(compiled.m.bytes()) == string(talosOut):
		c.observed, c.out = "parity", compiled.m.bytes()
	case err == nil:
		c.observed = "differs"
	case errors.As(err, &e) && e.Rule == RuleRejected:
		c.observed, c.stage, c.rule = "rejected", "compose", e.Input
		c.patch, c.validate = spMessage(e.Message, "patch", nativePatch), "skipped"
	case errors.As(err, &e) && e.Rule == RuleInvalid:
		c.observed, c.stage, c.rule = "invalid", "validate", string(e.Rule)
		c.validate = spMessage(e.Message, "validate", nativeValidate)
	case errors.As(err, &e):
		c.observed, c.stage, c.rule = "refused", "output", string(e.Rule)
		c.patch, c.validate = "skipped", "skipped"
	default:
		c.observed, c.stage, c.rule = "refused", "compile", "error"
		c.patch, c.validate = "skipped", "skipped"
	}
}

// spMessage names what a step's message became: withheld, the machinery's own (verbatim),
// redacted, or something else, which no case expects.
func spMessage(msg, step, native string) string {
	switch {
	case msg == "":
		return "empty"
	case msg == withheldNotice(step):
		return "withheld"
	case msg == native:
		return "verbatim"
	case strings.Contains(msg, "<redacted"):
		return "redacted"
	}
	return "other"
}

// provenance sets the case references' effective, overridden and output paths, and whether every
// effective version is the case's.
func (c *spCell) provenance(sc spCase, compiled Compiled) {
	var eff, over, outs []string
	c.versions = "same"
	for _, d := range compiled.Effective() {
		if _, ok := sc.Values[d.Reference]; !ok {
			continue
		}
		eff = append(eff, d.Reference)
		if d.Version != spVersion(sc, d.Reference) {
			c.versions = "differs"
		}
	}
	for _, r := range compiled.Provenance() {
		if _, ok := sc.Values[r.Reference]; !ok {
			continue
		}
		c.records = append(c.records, r)
		if r.OverriddenBy != nil {
			over = append(over, r.Reference)
		} else {
			outs = append(outs, r.Output)
		}
	}
	c.effective, c.overridden = setString(eff), setString(over)
	c.wantEffective, c.wantOverridden = setString(sc.Expect.Effective), setString(sc.Expect.Overridden)
	slices.Sort(outs)
	c.outputs = outs
	if c.versions == "same" && len(eff) > 0 {
		vs := []string{}
		for _, d := range compiled.Effective() {
			if _, ok := sc.Values[d.Reference]; ok {
				vs = append(vs, fmt.Sprintf("%s@%d", d.Reference, d.Version))
			}
		}
		c.versions = setString(vs)
	}
}

// spBaseRef is the base-ref cell: the fragments up to the first one holding a case reference,
// composed and moved into the import base by marks, then the remaining fragments.
func spBaseRef(t *testing.T, bc, fc spCell, real srCase, mode Mode, baseSrc Source, frags []Source, secrets []secret, public string) spCell {
	t.Helper()
	bc.talosctl, bc.machinery, bc.bytes = fc.talosctl, fc.machinery, fc.bytes
	bc.patch, bc.validate, bc.wantPatch, bc.wantValidate = "-", "-", "-", "-"
	if fc.observed != "parity" {
		bc.notApplicable, bc.note = true, "the fragments cell does not compose"
		return bc
	}
	k := -1 // the first fragment holding a case reference
	for _, r := range fc.records {
		if k < 0 || r.Source.Fragment < k {
			k = r.Source.Fragment
		}
	}
	if k < 0 {
		bc.notApplicable, bc.note = true, "no fragment holds a case reference"
		return bc
	}
	// What moves into the base and a later fragment overrides is refused as a base override.
	overrides := 0
	for _, r := range fc.records {
		if r.Source.Fragment <= k && r.OverriddenBy != nil && r.OverriddenBy.Fragment > k {
			overrides++
		}
	}
	prefix, err := Compile(Input{Base: baseSrc, Fragments: frags[:k+1], Mode: mode})
	if err != nil {
		bc.notApplicable, bc.note = true, "the moved fragments alone do not compile: "+errorRule(err)
		bc.findings = spFindings(secrets, public, spTexts(t, prefix, err))
		return bc
	}
	// A mark names each output leaf a case reference reached; a mapping member's path ends in its
	// key, which reads <redacted>, so a mapping is marked whole, at its parent.
	var marks []ingest.Path
	marked := map[string]bool{}
	for _, r := range prefix.Provenance() {
		if r.Source.Base || r.Output == "" || !slices.ContainsFunc(fc.records, func(f Record) bool { return f.Reference == r.Reference }) {
			continue
		}
		at := r.Output
		if r.Member >= 0 {
			at = at[:strings.LastIndex(at, "/")]
		}
		if marked[at] {
			continue
		}
		marked[at] = true
		p, err := ingest.ParsePath(at)
		if err != nil {
			bc.notApplicable, bc.note = true, "an output path marks cannot name"
			return bc
		}
		marks = append(marks, p)
	}
	if len(marks) == 0 {
		bc.notApplicable, bc.note = true, "the moved references reach no output leaf"
		return bc
	}
	moved := prefix.m.bytes()
	var decl ingest.Declarations
	for _, e := range real.Embedded {
		if p, ok := embeddedPath(t, moved, e.Doc, e.Path); ok && e.Identified {
			decl.Embedded = append(decl.Embedded, ingest.Embedded{Path: p, Format: e.Format})
		}
	}
	text, values, err := gateIngest(moved, marks, decl, nil)
	if err != nil {
		bc.notApplicable, bc.note = true, "the marked base does not ingest: "+errorRule(err)
		return bc
	}
	bc.note = fmt.Sprintf("%d fragments moved, %d marks", k+1, len(marks))
	compiled, err := Compile(Input{Base: gateSource("base+"+frags[k].Revision, moved, text, values), Fragments: frags[k+1:], Mode: mode})
	bc.expected = "parity"
	if overrides > 0 {
		bc.expected, bc.wantStage, bc.wantRule = "refused", "output", string(RuleBaseOverride)
	}
	var e *Error
	switch {
	case err == nil && string(compiled.m.bytes()) == string(fc.out):
		bc.observed = "parity"
	case err == nil:
		bc.observed = "differs"
	case errors.As(err, &e):
		bc.observed, bc.stage, bc.rule = "refused", "output", string(e.Rule)
		if e.Rule == RuleRejected || e.Rule == RuleInvalid {
			bc.observed, bc.stage = string(e.Rule), "compose"
		}
	default:
		bc.observed, bc.stage, bc.rule = "refused", "compile", "error"
	}
	bc.findings = spFindings(secrets, public, spTexts(t, compiled, err))
	return bc
}

// spSecrets are the oracle's secrets for case name: each value (each member of a mapping) at its
// version, as placed, and the base bundle's.
func spSecrets(t *testing.T, name string, bundle []secret) []secret {
	t.Helper()
	var c spCase
	readYAML(t, filepath.Join(spCases, name, "case.yaml"), &c)
	refs := make([]string, 0, len(c.Values))
	for r := range c.Values {
		refs = append(refs, r)
	}
	slices.Sort(refs)
	var out []secret
	for _, r := range refs {
		id := r + "@" + strconv.FormatInt(spVersion(c, r), 10)
		n := c.Values[r]
		leaves := []struct {
			id, key string
			node    *yaml.Node
		}{{id, spKey(t, name, r), &n}}
		if n.Kind == yaml.MappingNode {
			leaves = leaves[:0]
			for i := 0; i+1 < len(n.Content); i += 2 {
				leaves = append(leaves, struct {
					id, key string
					node    *yaml.Node
				}{id + "#" + n.Content[i].Value, n.Content[i].Value, n.Content[i+1]})
			}
		}
		for _, l := range leaves {
			s := secret{id: l.id, class: "ref", value: l.node.Value}
			switch l.node.ShortTag() {
			case "!!int":
				s.kind, s.key = "int", l.key
			case "!!bool":
				s.kind, s.key = "bool", l.key
			default:
				s.kind = "str"
				if c.Modifiers[r] == "base64" {
					s.kind, s.placed = "bytes", base64.StdEncoding.EncodeToString([]byte(l.node.Value))
				}
			}
			out = append(out, s)
		}
	}
	return append(out, bundle...)
}

// spKey is the mapping key a reference stands under in case name's authored fragments, for the
// keyed form of a number or a boolean.
func spKey(t *testing.T, name, ref string) string {
	t.Helper()
	re := regexp.MustCompile(`([A-Za-z0-9_.-]+):[ \t]*!ref[ \t]+` + regexp.QuoteMeta(ref) + `([^A-Za-z0-9._-]|$)`)
	dir := filepath.Join(spCases, name)
	var c struct {
		From string `yaml:"from"`
	}
	readYAML(t, filepath.Join(dir, "case.yaml"), &c)
	if c.From != "" {
		dir = filepath.Join(srCases, c.From)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) == "case.yaml" {
			continue
		}
		if m := re.FindSubmatch(readFile(t, f)); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// spTexts is everything the compiler renders of one outcome.
func spTexts(t *testing.T, c Compiled, err error) []string {
	t.Helper()
	if err != nil {
		return spErrorTexts(err)
	}
	texts := []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%s", c)}
	r, rerr := c.Redacted()
	texts = append(texts, r)
	if rerr != nil {
		texts = append(texts, spErrorTexts(rerr)...)
	}
	for _, v := range []any{c.Provenance(), c.Effective(), c.Reproduction()} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, fmt.Sprintf("%+v", v), string(b))
	}
	return texts
}

func spErrorTexts(err error) []string {
	return []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)}
}

// spFindings is the oracle's findings over texts, as one cell: secret/form, sorted, or "".
func spFindings(secrets []secret, public string, texts []string) string {
	seen := map[string]bool{}
	for _, text := range texts {
		for _, f := range oracleScan(text, secrets, public) {
			seen[f.secret+"/"+f.form] = true
		}
	}
	return setString(slices.Collect(func(yield func(string) bool) {
		for k := range seen {
			if !yield(k) {
				return
			}
		}
	}))
}

// nativeMessages is the machinery's own message for the literal form: its composition's, or its
// validation's when it composes. Each quotes values; the gate compares, and never keeps them.
func nativeMessages(base []byte, fragments [][]byte, mode Mode) (patch, validate string) {
	var patches []configpatcher.Patch
	for _, f := range fragments {
		p, err := configpatcher.LoadPatch(f)
		if err != nil {
			return err.Error(), ""
		}
		patches = append(patches, p)
	}
	o, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		return err.Error(), ""
	}
	cfg, err := o.Config()
	if err != nil {
		return err.Error(), ""
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		return err.Error(), ""
	}
	s := string(b)
	msg, _ := Materialized{s: &s}.validate(mode)
	return "", msg
}

func gateSource(revision string, text []byte, s ingest.Sanitized, values map[string]provider.Value) Source {
	sum := sha256.Sum256(text)
	return Source{Revision: revision, Digest: hex.EncodeToString(sum[:]), Text: s, Values: values}
}

// baseSecrets reads a base's bundle secrets, as run/all extracted them: field, then value.
func baseSecrets(t *testing.T, path string) []secret {
	t.Helper()
	var out []secret
	for i, line := range strings.Split(strings.TrimSpace(string(readFile(t, path))), "\n") {
		_, v, ok := strings.Cut(line, "\t")
		if !ok || v == "" {
			t.Fatalf("%s line %d: no value", path, i+1)
		}
		out = append(out, secret{id: fmt.Sprintf("bundle[%d]", i), class: "base", kind: "str", value: v})
	}
	return out
}

func spVersion(c spCase, ref string) int64 {
	if v, ok := c.Versions[ref]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n
		}
	}
	return 1
}

func errorRule(err error) string {
	var e *Error
	var r *ingest.Refusal
	switch {
	case errors.As(err, &e):
		return string(e.Rule)
	case errors.As(err, &r):
		return string(r.Rule)
	}
	return "error"
}

func setString(s []string) string {
	s = slices.Clone(s)
	slices.Sort(s)
	return strings.Join(slices.Compact(s), ",")
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readYAML(t *testing.T, path string, v any) {
	t.Helper()
	if err := yaml.Unmarshal(readFile(t, path), v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
