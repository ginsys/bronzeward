package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const deriveCase = `description: test
transformation: test
fragments:
  - file: f1.yaml
    marked: true
embedded:
  - doc: v1alpha1
    path: cluster/inlineManifests[name=opaque]/contents
    format: yaml
    identified: false
values:
  reg-pass: BWSYNTH-registry-password
  extra-ca: BWSYNTH-extra-ca
  reg-auth:
    username: BWSYNTH-user
    password: BWSYNTH-map-password
  wipe: true
  port: 7446
  hidden: BWSYNTH-hidden
modifiers:
  extra-ca: base64
versions:
  reg-pass: "2"
native: pass
expect:
  effective: [reg-pass]
  messages: {patch: none, validate: verbatim}
  leaks: {none: leak}
`

const deriveFragment = `machine:
  registries:
    config:
      a.test:
        auth:
          password: &p !ref reg-pass
      b.test:
        auth:
          password: *p
      c.test:
        auth: !ref reg-auth
  acceptedCAs:
    - crt: !ref extra-ca
  install:
    wipe: !ref wipe
  features:
    kubePrism:
      port: !ref port
cluster:
  inlineManifests:
    - name: opaque
      contents: |
        password: !ref hidden
`

func deriveFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case", "case.yaml"), deriveCase)
	writeFile(t, filepath.Join(dir, "case", "f1.yaml"), deriveFragment)
	if err := derive(filepath.Join(dir, "case"), filepath.Join(dir, "none"), filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "out")
}

func TestDeriveRenamesEachResolvableOccurrence(t *testing.T) {
	out := deriveFixture(t)
	real := readFile(t, filepath.Join(out, "real", "case", "f1.yaml"))
	if real != deriveFragment {
		t.Fatalf("the real fragment is not the canonical one:\n%s", real)
	}
	trace := readFile(t, filepath.Join(out, "trace", "case", "f1.yaml"))
	for _, want := range []string{"!ref reg-pass~0", "!ref reg-auth~1", "!ref extra-ca~2", "!ref wipe~3", "!ref port~4", "!ref hidden\n"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace fragment lacks %q:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "BWSYNTH") {
		t.Errorf("trace fragment holds a real value:\n%s", trace)
	}
}

func TestDeriveGivesEachLeafATracerOfItsKind(t *testing.T) {
	out := deriveFixture(t)
	var tracers []Tracer
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(out, "tracers.json"))), &tracers); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, tr := range tracers {
		kinds[tr.Ref+"#"+tr.Leaf] = tr.Kind
		if tr.Kind == "bytes" {
			if tr.Value != base64.StdEncoding.EncodeToString([]byte(tr.Raw)) {
				t.Errorf("bytes tracer %d is not its raw form's base64", tr.ID)
			}
		}
	}
	want := map[string]string{"reg-pass#": "str", "reg-auth#username": "str", "reg-auth#password": "str",
		"extra-ca#": "bytes", "wipe#": "bool", "port#": "int"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s: kind %q, want %q (all: %v)", k, kinds[k], v, kinds)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "flip-4", "case", "case.yaml")); err != nil {
		t.Errorf("no flip case for the boolean tracer: %v", err)
	}
	realCase := readFile(t, filepath.Join(out, "real", "case", "case.yaml"))
	if !strings.Contains(realCase, base64.StdEncoding.EncodeToString([]byte("BWSYNTH-extra-ca"))) {
		t.Errorf("the real case does not place the modifier's output:\n%s", realCase)
	}
}

func TestDeriveRecordsOccurrencesAndSecrets(t *testing.T) {
	out := deriveFixture(t)
	occ := readFile(t, filepath.Join(out, "occurrences.tsv"))
	if !strings.Contains(occ, "0\treg-pass\t2\t-\tf1.yaml\t") || !strings.Contains(occ, "\thidden\t1\t-\tf1.yaml\t") ||
		!strings.Contains(occ, "cluster/inlineManifests[name=opaque]/contents|yaml/password\tno\n") {
		t.Fatalf("occurrences:\n%s", occ)
	}
	var secrets []Secret
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(out, "secrets.json"))), &secrets); err != nil {
		t.Fatal(err)
	}
	ids := map[string]Secret{}
	for _, s := range secrets {
		ids[s.ID] = s
	}
	if ids["reg-pass@2"].Value != "BWSYNTH-registry-password" || ids["port@1"].Key != "port" ||
		ids["reg-auth@1#password"].Value != "BWSYNTH-map-password" || ids["extra-ca@1"].Placed == "" {
		t.Fatalf("secrets: %v", ids)
	}
}

func TestDeriveReusesAnIssue3Case(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case", "case.yaml"), "description: d\ntransformation: alias\nfrom: alias\n"+
		"values:\n  reg-secret: BWSYNTH-shared-secret\nnative: pass\nexpect:\n  effective: [reg-secret]\n")
	if err := derive(filepath.Join(dir, "case"), "../e2-structural-references/cases", filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	trace := readFile(t, filepath.Join(dir, "out", "trace", "case", "f1.yaml"))
	if !strings.Contains(trace, "&secret !ref reg-secret~0") || !strings.Contains(trace, "*secret") {
		t.Fatalf("trace:\n%s", trace)
	}
	info := readFile(t, filepath.Join(dir, "out", "info.tsv"))
	if !strings.Contains(info, "premise\ttag\tparity\n") || !strings.Contains(info, "fragment\tf1.yaml\n") {
		t.Fatalf("info:\n%s", info)
	}
}
