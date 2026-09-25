package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cellBase = `version: v1alpha1
machine:
    token: BWSYNTH-base-machine-token
    install:
        wipe: false
`

// A cellSpec varies the hand-built cell: the registry host the reference lands under, its value
// and its version.
type cellSpec struct{ host, value, version string }

var defaultCell = cellSpec{"r.test", "BWSYNTH-registry-password", ""}

func (s cellSpec) caseText() string {
	c := "description: one string reference\ntransformation: resolution\nfragments:\n  - file: f1.yaml\n    marked: true\n" +
		"values:\n  reg-pass: " + s.value + "\n"
	if s.version != "" {
		c += "versions:\n  reg-pass: \"" + s.version + "\"\n"
	}
	return c + "native: pass\nexpect:\n  effective: [reg-pass]\n  messages: {patch: none, validate: verbatim}\n" +
		"  leaks: {none: leak, value: clean, resolution-path: clean, schema: clean, composed-path: clean, path+schema: clean, path+schema+value: clean}\n"
}

func (s cellSpec) fragment() string {
	return "machine:\n  registries:\n    config:\n      " + s.host + ":\n        auth:\n          username: e2sp-user\n          password: !ref reg-pass\n"
}

func (s cellSpec) registries(pass string) string {
	return "    registries:\n        config:\n            " + s.host + ":\n                auth:\n" +
		"                    username: e2sp-user\n                    password: " + pass + "\n"
}

// buildCell lays out what run/all leaves for one cell, with compositions written by hand.
func buildCell(t *testing.T) (cell, secrets, artifacts string) {
	t.Helper()
	dir := t.TempDir()
	cell = buildCellSpec(t, dir, "cell", defaultCell)
	secrets = filepath.Join(dir, "base-secrets.tsv")
	writeFile(t, secrets, "token:\tBWSYNTH-base-machine-token\n")
	return cell, secrets, filepath.Join(dir, "artifacts")
}

func buildCellSpec(t *testing.T, dir, name string, s cellSpec) string {
	t.Helper()
	cell := filepath.Join(dir, name)
	caseDir := filepath.Join(dir, name+"-case")
	writeFile(t, filepath.Join(caseDir, "case.yaml"), s.caseText())
	writeFile(t, filepath.Join(caseDir, "f1.yaml"), s.fragment())
	if err := derive(caseDir, dir, filepath.Join(cell, "derived")); err != nil {
		t.Fatal(err)
	}
	var tracers []Tracer
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(cell, "derived", "tracers.json"))), &tracers); err != nil {
		t.Fatal(err)
	}
	tr := tracers[0].Value
	writeFile(t, filepath.Join(cell, "base.yaml"), cellBase)
	realOut := cellBase + s.registries(s.value)
	traceOut := cellBase + s.registries(tr)
	frag := "machine:\n    registries:\n        config:\n            " + s.host + ":\n                auth:\n" +
		"                    username: e2sp-user\n                    password: "
	rec := "\tv1alpha1\tmachine/registries/config/" + s.host + "/auth/password\n"
	for _, form := range []string{"literal", "tag", "marked", "binding"} {
		d := filepath.Join(cell, "real", form)
		writeFile(t, filepath.Join(d, "frag", "f1.yaml"), frag+s.value+"\n")
		writeFile(t, filepath.Join(d, "out.yaml"), realOut)
		writeFile(t, filepath.Join(d, "patch.err"), "")
		writeFile(t, filepath.Join(d, "patch.rc"), "0\n")
		if form != "literal" {
			writeFile(t, filepath.Join(d, "res", "f1.yaml.tsv"), "reg-pass"+rec)
		}
	}
	d := filepath.Join(cell, "trace", "tag")
	writeFile(t, filepath.Join(d, "frag", "f1.yaml"), frag+tr+"\n")
	writeFile(t, filepath.Join(d, "out.yaml"), traceOut)
	writeFile(t, filepath.Join(d, "patch.err"), "")
	writeFile(t, filepath.Join(d, "patch.rc"), "0\n")
	writeFile(t, filepath.Join(d, "res", "f1.yaml.tsv"), "reg-pass~0"+rec)
	for _, v := range []string{"real/tag", "real/literal", "trace/tag"} {
		writeFile(t, filepath.Join(cell, v, "validate.txt"), "out.yaml is valid for metal mode\n")
		writeFile(t, filepath.Join(cell, v, "validate.rc"), "0\n")
	}
	return cell
}

func TestAnalyseRedactsFromProvenance(t *testing.T) {
	cell, secrets, artifacts := buildCell(t)
	if err := analyse(cell, secrets, artifacts, "gen", "string"); err != nil {
		t.Fatal(err)
	}
	prov := readFile(t, filepath.Join(cell, "analysis", "provenance.tsv"))
	if !strings.Contains(prov, "\teffective\tv1alpha1\tmachine/registries/config/r.test/auth/password\t") {
		t.Fatalf("provenance:\n%s", prov)
	}
	combined := filepath.Join(artifacts, "path+schema+value", "string", "gen")
	for _, a := range []string{"diff.txt", "errors.txt", "log.txt", "support.txt"} {
		s := readFile(t, filepath.Join(combined, a))
		if strings.Contains(s, "BWSYNTH") {
			t.Errorf("%s holds a synthetic secret:\n%s", a, s)
		}
	}
	diff := readFile(t, filepath.Join(combined, "diff.txt"))
	if !strings.Contains(diff, "+                    password: <redacted:reg-pass@1>") {
		t.Errorf("the diff does not name the reference:\n%s", diff)
	}
	support := readFile(t, filepath.Join(combined, "support.txt"))
	if !strings.Contains(support, "token: <redacted:schema>") {
		t.Errorf("the base secret is not schema-redacted:\n%s", support)
	}
	none := readFile(t, filepath.Join(artifacts, "none", "string", "gen", "support.txt"))
	if !strings.Contains(none, "BWSYNTH-registry-password") {
		t.Errorf("the unredacted support data does not hold the value, so the control proves nothing")
	}
	exp := readFile(t, filepath.Join(cell, "analysis", "expectations.tsv"))
	if strings.Contains(exp, "\tno\n") {
		t.Errorf("an expectation failed:\n%s", exp)
	}
	for _, want := range []string{"leak.none\tleak\tleak\tyes", "leak.composed-path\tclean\tclean\tyes", "effective\treg-pass\treg-pass\tyes",
		"base-leak.composed-path\tleak\tleak\tyes", "base-leak.path+schema\tclean\tclean\tyes"} {
		if !strings.Contains(exp, want) {
			t.Errorf("expectations lack %q:\n%s", want, exp)
		}
	}
	ctl := readFile(t, filepath.Join(cell, "analysis", "controls.tsv"))
	for _, want := range []string{"fidelity-check-fires\tfired", "trace-holds-no-secret\tpass", "render-identity\tpass", "resolution-records-match\tpass"} {
		if !strings.Contains(ctl, want) {
			t.Errorf("controls lack %q:\n%s", want, ctl)
		}
	}
}

func TestAnalyseRedactsARejectedCompositionsMessage(t *testing.T) {
	cell, secrets, artifacts := buildCell(t)
	var tracers []Tracer
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(cell, "derived", "tracers.json"))), &tracers); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"real", "trace"} {
		val := "BWSYNTH"
		if v == "trace" {
			val = tracers[0].Value[:7]
		}
		d := filepath.Join(cell, v, "tag")
		for _, f := range []string{"out.yaml", "validate.txt", "validate.rc"} {
			if err := os.Remove(filepath.Join(d, f)); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(d, "patch.err"), "line 4: cannot construct !!str `"+val+"...` into int\n")
		writeFile(t, filepath.Join(d, "patch.rc"), "1\n")
	}
	if err := analyse(cell, secrets, artifacts, "gen", "string"); err != nil {
		t.Fatal(err)
	}
	errs := readFile(t, filepath.Join(artifacts, "composed-path", "string", "gen", "errors.txt"))
	if !strings.Contains(errs, "`<redacted:reg-pass@1>...` into int") {
		t.Fatalf("errors:\n%s", errs)
	}
	value := readFile(t, filepath.Join(artifacts, "value", "string", "gen", "errors.txt"))
	if !strings.Contains(value, "BWSYNTH...") {
		t.Fatalf("value matching removed a prefix it cannot know:\n%s", value)
	}
	leaks := readFile(t, filepath.Join(cell, "analysis", "leaks.tsv"))
	if !strings.Contains(leaks, "value\terrors\treg-pass@1\tref\tprefix\t1") {
		t.Fatalf("the oracle did not report the prefix under value matching:\n%s", leaks)
	}
	// With no composition there is no configuration in the support data, so no base secret either.
	exp := readFile(t, filepath.Join(cell, "analysis", "expectations.tsv"))
	if !strings.Contains(exp, "base-leak.none\tclean\tclean\tyes") {
		t.Fatalf("expectations:\n%s", exp)
	}
}

// A fragment the machinery cannot load has no schema redaction; the note that says so must not
// quote the machinery's error, which quotes the value.
func TestAnalyseSchemaNoteQuotesNoValue(t *testing.T) {
	cell, secrets, artifacts := buildCell(t)
	var tracers []Tracer
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(cell, "derived", "tracers.json"))), &tracers); err != nil {
		t.Fatal(err)
	}
	for v, val := range map[string]string{"real": "BWSYNTH-registry-password", "trace": tracers[0].Value} {
		p := filepath.Join(cell, v, "tag", "frag", "f1.yaml")
		writeFile(t, p, readFile(t, p)+"    features:\n        kubePrism:\n            port: "+val+"\n")
	}
	if err := analyse(cell, secrets, artifacts, "gen", "string"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"schema", "path+schema", "path+schema+value"} {
		s := readFile(t, filepath.Join(artifacts, r, "string", "gen", "support.txt"))
		if !strings.Contains(s, "schema redaction unavailable") {
			t.Errorf("%s: no note that the schema could not be applied:\n%s", r, s)
		}
		if r != "schema" && strings.Contains(s, "BWSYNTH") {
			t.Errorf("%s support leaks:\n%s", r, s)
		}
	}
}

// A value the author also wrote as a literal: the literal copy is not provenance's (it leaks under
// composed-path), the oracle does not count the authored text around it, and the trace pass holding
// the authored literal is not a trace-pass leak.
func TestAnalyseReportsALiteralCopyOfAReferencedValue(t *testing.T) {
	cell, secrets, artifacts := buildCell(t)
	copyLine := "    nodeAnnotations:\n        e2sp.example.test/pasted: BWSYNTH-registry-password\n"
	for _, v := range []string{"real/tag", "trace/tag", "real/literal", "real/marked", "real/binding"} {
		for _, f := range []string{"out.yaml", "frag/f1.yaml"} {
			p := filepath.Join(cell, v, f)
			writeFile(t, p, strings.Replace(readFile(t, p), "    registries:\n", copyLine+"    registries:\n", 1))
		}
	}
	canon := filepath.Join(cell, "derived", "real", "case", "f1.yaml")
	writeFile(t, canon, strings.Replace(readFile(t, canon), "  registries:\n", "  nodeAnnotations:\n    e2sp.example.test/pasted: BWSYNTH-registry-password\n  registries:\n", 1))
	if err := analyse(cell, secrets, artifacts, "gen", "string"); err != nil {
		t.Fatal(err)
	}
	ctl := readFile(t, filepath.Join(cell, "analysis", "controls.tsv"))
	if !strings.Contains(ctl, "trace-holds-no-secret\tpass") {
		t.Errorf("controls:\n%s", ctl)
	}
	leaks := readFile(t, filepath.Join(cell, "analysis", "leaks.tsv"))
	if !strings.Contains(leaks, "composed-path\tsupport\treg-pass@1\tref\texact") {
		t.Errorf("the literal copy is not reported under composed-path:\n%s", leaks)
	}
	if strings.Contains(leaks, "path+schema+value\t") {
		t.Errorf("value matching left the literal copy:\n%s", leaks)
	}
}

// pairCells builds two cells and a base-secrets file under one directory.
func pairCells(t *testing.T, a, b cellSpec) (dir, cellA, cellB, secrets string) {
	t.Helper()
	dir = t.TempDir()
	cellA = buildCellSpec(t, dir, "a", a)
	cellB = buildCellSpec(t, dir, "b", b)
	secrets = filepath.Join(dir, "base-secrets.tsv")
	writeFile(t, secrets, "token:\tBWSYNTH-base-machine-token\n")
	return dir, cellA, cellB, secrets
}

func TestPairDiffsTwoCompositionsAndShowsStalePathsLeak(t *testing.T) {
	dir, a, b, secrets := pairCells(t, defaultCell, cellSpec{"m.test", "BWSYNTH-registry-password", ""})
	artifacts, out := filepath.Join(dir, "artifacts"), filepath.Join(dir, "pair")
	if err := analysePair(a, b, secrets, artifacts, out, "moved", "gen"); err != nil {
		t.Fatal(err)
	}
	diff := readFile(t, filepath.Join(artifacts, "path+schema+value", "moved", "gen", "diff.txt"))
	if strings.Contains(diff, "BWSYNTH") {
		t.Errorf("the combined pair diff leaks:\n%s", diff)
	}
	for _, want := range []string{"-            r.test:", "+            m.test:", "                     password: <redacted:reg-pass@1>"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	if none := readFile(t, filepath.Join(artifacts, "none", "moved", "gen", "diff.txt")); !strings.Contains(none, "BWSYNTH-registry-password") {
		t.Errorf("the unredacted pair diff does not hold the value:\n%s", none)
	}
	ctl := readFile(t, filepath.Join(out, "controls.tsv"))
	for _, want := range []string{"stale-paths-leak\tfired", "stale-values-leak\tdid-not-fire"} {
		if !strings.Contains(ctl, want) {
			t.Errorf("controls lack %q:\n%s", want, ctl)
		}
	}
	exp := readFile(t, filepath.Join(out, "expectations.tsv"))
	for _, want := range []string{"pair-leak.none\tleak\tleak\tyes", "pair-leak.path+schema+value\tclean\tclean\tyes"} {
		if !strings.Contains(exp, want) {
			t.Errorf("expectations lack %q:\n%s", want, exp)
		}
	}
}

func TestPairShowsARotatedValueEscapesStaleValues(t *testing.T) {
	dir, a, b, secrets := pairCells(t, defaultCell, cellSpec{"r.test", "BWSYNTH-rotated-to-another", "2"})
	artifacts, out := filepath.Join(dir, "artifacts"), filepath.Join(dir, "pair")
	if err := analysePair(a, b, secrets, artifacts, out, "rotate", "gen"); err != nil {
		t.Fatal(err)
	}
	diff := readFile(t, filepath.Join(artifacts, "composed-path", "rotate", "gen", "diff.txt"))
	for _, want := range []string{"-                    password: <redacted:reg-pass@1>", "+                    password: <redacted:reg-pass@2>"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	ctl := readFile(t, filepath.Join(out, "controls.tsv"))
	for _, want := range []string{"stale-values-leak\tfired", "stale-paths-leak\tdid-not-fire", "stale-paths-version\tfired"} {
		if !strings.Contains(ctl, want) {
			t.Errorf("controls lack %q:\n%s", want, ctl)
		}
	}
}
