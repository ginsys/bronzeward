package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParsePathRoundTrip(t *testing.T) {
	for _, s := range []string{
		"machine/registries/config/registry.example.test/auth/password",
		"cluster/inlineManifests[name=e2-secret]/contents|yaml/stringData/password",
		"machine/files[path=/var/etc/e2/config.json]/content|json/token",
		"machine/network/interfaces[3]/mtu",
	} {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if got := p.String(); got != s {
			t.Errorf("round trip: %s became %s", s, got)
		}
	}
	for _, bad := range []string{"", "a[b", "a|toml", "a[x]"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// writeCase lays out a case directory: case.yaml and its fragments.
func writeCase(t *testing.T, caseYAML string, frags map[string]string) *Case {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "case.yaml"), []byte(caseYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range frags {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const aliasCase = `
description: test
native: pass
fragments:
  - file: f1.yaml
    marked: true
embedded:
  - doc: v1alpha1
    path: cluster/inlineManifests[name=e2]/contents
    format: yaml
    identified: true
values:
  user: e2-user
  pass: e2-pass
expect: {}
`

const aliasFrag = `machine:
  registries:
    config:
      registry.example.test:
        auth:
          username: &u !ref user
          password: *u
cluster:
  inlineManifests:
    - name: e2
      contents: |
        stringData:
          password: !ref pass
`

func encode(t *testing.T, docs []*yaml.Node) string {
	t.Helper()
	b, err := encodeDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGenerateForms(t *testing.T) {
	c := writeCase(t, aliasCase, map[string]string{"f1.yaml": aliasFrag})

	lit, _, occs, err := Generate(c, "literal")
	if err != nil {
		t.Fatal(err)
	}
	s := encode(t, lit[0])
	for _, want := range []string{"username: &u e2-user", "password: *u", "password: e2-pass"} {
		if !strings.Contains(s, want) {
			t.Errorf("literal form lacks %q:\n%s", want, s)
		}
	}
	// The superset holds the anchor, the alias and the embedded reference.
	var paths []string
	for _, o := range occs {
		paths = append(paths, o.Ref+" "+o.Path.String())
	}
	got := strings.Join(paths, "\n")
	for _, want := range []string{
		"user machine/registries/config/registry.example.test/auth/username",
		"user machine/registries/config/registry.example.test/auth/password",
		"pass cluster/inlineManifests[name=e2]/contents|yaml/stringData/password",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("superset lacks %q:\n%s", want, got)
		}
	}

	tag, _, _, err := Generate(c, "tag")
	if err != nil {
		t.Fatal(err)
	}
	if s := encode(t, tag[0]); !strings.Contains(s, "&u !bwref user") || !strings.Contains(s, "password: !bwref pass") {
		t.Errorf("tag form:\n%s", s)
	}

	marked, _, _, err := Generate(c, "marked")
	if err != nil {
		t.Fatal(err)
	}
	if s := encode(t, marked[0]); !strings.Contains(s, `&u "bwref:user"`) || !strings.Contains(s, `password: "bwref:pass"`) {
		t.Errorf("marked form:\n%s", s)
	}

	bound, bindings, _, err := Generate(c, "binding")
	if err != nil {
		t.Fatal(err)
	}
	// An author using bindings writes neither the bound keys nor the mappings only they filled.
	if s := encode(t, bound[0]); strings.Contains(s, "username") || strings.Contains(s, "password") ||
		strings.Contains(s, "machine:") || strings.Contains(s, "stringData") {
		t.Errorf("binding form still holds the bound keys or their emptied parents:\n%s", s)
	}
	if len(bindings) != 3 {
		t.Errorf("want 3 bindings (anchor, alias, embedded), got %d: %+v", len(bindings), bindings)
	}
}

func values(c *Case) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	for r := range c.Values {
		v := c.Values[r]
		out[r] = &v
	}
	return out
}

// Resolving each candidate's form early must give the literal form back, for every candidate.
func TestEarlyResolutionMatchesLiteral(t *testing.T) {
	c := writeCase(t, aliasCase, map[string]string{"f1.yaml": aliasFrag})
	lit, _, _, err := Generate(c, "literal")
	if err != nil {
		t.Fatal(err)
	}
	want := normalize(t, lit[0])
	for _, cand := range []string{"tag", "marked", "binding"} {
		frags, bindings, _, err := Generate(c, cand)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Resolve(c, cand, "f1.yaml", frags[0], bindings, values(c))
		if err != nil {
			t.Fatalf("%s: %v", cand, err)
		}
		if len(res) != 3 {
			t.Errorf("%s: want 3 resolutions (anchor, alias, embedded), got %+v", cand, res)
		}
		if got := normalize(t, frags[0]); got != want {
			t.Errorf("%s: early resolution differs from the literal form:\n%s\nwant\n%s", cand, got, want)
		}
	}
}

// normalize compares documents by value: aliases expanded, styles dropped.
func normalize(t *testing.T, docs []*yaml.Node) string {
	t.Helper()
	var b strings.Builder
	for _, d := range docs {
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(out)
	}
	return b.String()
}

func TestMarkedOptIn(t *testing.T) {
	c := writeCase(t, strings.Replace(aliasCase, "marked: true", "marked: false", 1), map[string]string{"f1.yaml": aliasFrag})
	frags, _, _, err := Generate(c, "marked")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(c, "marked", "f1.yaml", frags[0], nil, values(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("a fragment not opted in was resolved: %+v", res)
	}
	// Late, the opt-in is gone: the same strings are references.
	res, err = Resolve(c, "marked", "", frags[0], nil, values(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Errorf("late marked resolution: want 3, got %+v", res)
	}
}

func TestResolveRefusals(t *testing.T) {
	c := writeCase(t, aliasCase, map[string]string{"f1.yaml": aliasFrag})
	frags, _, _, err := Generate(c, "tag")
	if err != nil {
		t.Fatal(err)
	}
	v := values(c)
	delete(v, "pass")
	if _, err := Resolve(c, "tag", "f1.yaml", frags[0], nil, v); err == nil || !strings.Contains(err.Error(), `unknown reference "pass"`) {
		t.Errorf("unknown reference: %v", err)
	}

	unidentified := strings.Replace(aliasCase, "identified: true", "identified: false", 1)
	c = writeCase(t, unidentified, map[string]string{"f1.yaml": aliasFrag})
	frags, bindings, _, err := Generate(c, "binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(c, "binding", "f1.yaml", frags[0], bindings, values(c)); err == nil || !strings.Contains(err.Error(), "not an identified embedded document") {
		t.Errorf("binding into an unidentified embedded document: %v", err)
	}
	// The tag candidate leaves the unidentified document's text alone.
	frags, _, _, err = Generate(c, "tag")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(c, "tag", "f1.yaml", frags[0], nil, values(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !strings.Contains(encode(t, frags[0]), "!bwref pass") {
		t.Errorf("unidentified embedded document was resolved: %+v\n%s", res, encode(t, frags[0]))
	}
}

func TestBindingCreatesMissingKeys(t *testing.T) {
	c := writeCase(t, aliasCase, map[string]string{"f1.yaml": aliasFrag})
	docs, err := parseDocs([]byte("machine:\n  type: controlplane\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := ParsePath("machine/registries/config/registry.example.test/auth/password")
	res, err := Resolve(c, "binding", "", docs, []Binding{{Fragment: "f1.yaml", Doc: "v1alpha1", Path: p, Ref: "pass"}}, values(c))
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if s := encode(t, docs); !strings.Contains(s, "password: e2-pass") {
		t.Errorf("binding did not create the path:\n%s", s)
	}
}

// The leak-refusal patterns must include a bootstrap token (23 characters: six, a dot, sixteen)
// and each line of a multi-line key, and nothing short enough to match ordinary text.
func TestPatterns(t *testing.T) {
	docs, err := parseDocs([]byte("trustdinfo:\n  token: abcdef.0123456789abcdef\nkey: |\n  first-line-of-a-long-key-0123456789\n  second-line-of-a-long-key-0123456789\nname: e2-short\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(Patterns(docs), "\n")
	for _, want := range []string{"abcdef.0123456789abcdef", "first-line-of-a-long-key-0123456789", "second-line-of-a-long-key-0123456789"} {
		if !strings.Contains(got, want) {
			t.Errorf("patterns lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "e2-short") {
		t.Errorf("patterns hold a short value:\n%s", got)
	}
}

func TestSentinelsFound(t *testing.T) {
	c := writeCase(t, aliasCase+"\n", map[string]string{"f1.yaml": aliasFrag})
	c.Values["flag"] = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	c.Values["mtu"] = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1450"}
	s, unidentifiable := Sentinels(c)
	if _, ok := unidentifiable["flag"]; !ok || len(unidentifiable) != 1 {
		t.Errorf("unidentifiable: %v", unidentifiable)
	}
	frags, _, _, err := Generate(c, "tag")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(c, "tag", "f1.yaml", frags[0], nil, s); err != nil {
		t.Fatal(err)
	}
	found, err := FindSentinels(c, frags[0], s)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]int{}
	for _, f := range found {
		refs[f.Ref]++
	}
	// The alias expands to a second copy of user's sentinel only once parsed; in the node tree it
	// is the same node, found through both paths.
	if refs["user"] != 2 || refs["pass"] != 1 || refs["mtu"] != 0 {
		t.Errorf("sentinels found: %v", refs)
	}
}
