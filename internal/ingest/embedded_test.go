package ingest

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const manifestPath = "doc[0]/cluster/inlineManifests/0/contents"

func manifestStream(contents string) string {
	var b strings.Builder
	b.WriteString("cluster:\n  inlineManifests:\n    - name: m\n      contents: |\n")
	for _, line := range strings.Split(strings.TrimSuffix(contents, "\n"), "\n") {
		b.WriteString("        " + line + "\n")
	}
	return b.String()
}

const secretManifest = "apiVersion: v1\nkind: Secret\nstringData:\n    password: " + secretText + "\n    user: admin\n"

func embeddedRequest(t *testing.T, text, format string, marks ...string) Request {
	t.Helper()
	req := request(t, text, marks...)
	req.Declarations.Embedded = []Embedded{{Path: manifestPath, Format: format}}
	return req
}

// innerDocument is the embedded document of the sanitized stream, parsed.
func innerText(t *testing.T, docs []byte) string {
	t.Helper()
	parsed, err := parseStream(docs)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := ParsePath(manifestPath)
	n, ok := resolve(parsed, 0, p.Pointer)
	if !ok {
		t.Fatal("no embedded document")
	}
	return n.Value
}

func TestExtractInsideEmbeddedYAML(t *testing.T) {
	c, err := Extract(embeddedRequest(t, manifestStream(secretManifest), "yaml", manifestPath+"|yaml/stringData/password"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	out := string(s.Documents())
	if strings.Contains(out, secretText) || len(calls) != 1 {
		t.Fatalf("%d creates; output:\n%s", len(calls), out)
	}
	inner := innerText(t, s.Documents())
	want := "apiVersion: v1\nkind: Secret\nstringData:\n  password: " + refTag + " " + calls[0].name + "\n  user: admin\n"
	if inner != want {
		t.Errorf("the embedded document was re-serialized as\n%s\nwant\n%s", inner, want)
	}
	if !slices.Equal(s.Declarations().Embedded, []Embedded{{Path: manifestPath, Format: "yaml"}}) {
		t.Errorf("embedded declarations %v", s.Declarations().Embedded)
	}
}

// TestExtractInsideEmbeddedKeyWithBar: an embedded document's key may hold "|"; the mark's inner
// pointer addresses it.
func TestExtractInsideEmbeddedKeyWithBar(t *testing.T) {
	manifest := "kind: Secret\nstringData:\n  a|b: " + secretText + "\n"
	c, err := Extract(embeddedRequest(t, manifestStream(manifest), "yaml", manifestPath+"|yaml/stringData/a|b"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	if out := string(s.Documents()); strings.Contains(out, secretText) || len(calls) != 1 {
		t.Fatalf("%d creates; output:\n%s", len(calls), out)
	}
}

func TestExtractInsideEmbeddedJSONWritesYAML(t *testing.T) {
	text := "cluster:\n  inlineManifests:\n    - name: m\n      contents: '{\"kind\": \"Secret\", \"stringData\": {\"password\": \"" + secretText + "\"}}'\n"
	c, err := Extract(embeddedRequest(t, text, "json", manifestPath+"|json/stringData/password"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	inner := innerText(t, s.Documents())
	want := "kind: Secret\nstringData:\n  password: " + refTag + " " + calls[0].name + "\n"
	if inner != want {
		t.Errorf("the embedded document was re-serialized as\n%s\nwant\n%s", inner, want)
	}
}

func TestEmbeddedRefusals(t *testing.T) {
	text := manifestStream(secretManifest)
	for _, tc := range []struct {
		name   string
		format string // "" declares nothing
		mark   string
		rule   Rule
	}{
		{"undeclared embedded document", "", manifestPath + "|yaml/stringData/password", RuleBadPath},
		{"declared in another format", "json", manifestPath + "|yaml/stringData/password", RuleBadPath},
		{"inner mark addressing nothing", "yaml", manifestPath + "|yaml/stringData/nope", RuleMarkUnaddressed},
		{"inner mark on a mapping of mappings", "yaml", manifestPath + "|yaml", RuleMarkKind},
		{"whole embedded scalar marked while declared", "yaml", manifestPath, RuleEmbedded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(t, text, tc.mark)
			if tc.format != "" {
				req.Declarations.Embedded = []Embedded{{Path: manifestPath, Format: tc.format}}
			}
			_, err := Extract(req)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tc.rule {
				t.Fatalf("got %v, want a %s refusal", err, tc.rule)
			}
			if strings.Contains(err.Error(), secretText) {
				t.Errorf("the refusal quotes the input: %v", err)
			}
		})
	}
}

// TestGuardInsideEmbedded: a copy of the inner value elsewhere in the same embedded document, or
// in the outer document, is found by value; the control shows the unguarded candidate holds it.
func TestGuardInsideEmbedded(t *testing.T) {
	for _, tc := range []struct {
		name, text, path string
	}{
		{"copy inside the embedded document",
			manifestStream("kind: Secret\nstringData:\n    password: " + secretText + "\n    user: " + secretText + "\n"),
			manifestPath + "|yaml/stringData/user"},
		{"copy in the outer document",
			"machine:\n  nodeLabels:\n    copy: " + secretText + "\n" + manifestStream(secretManifest),
			"doc[0]/machine/nodeLabels/copy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := embeddedRequest(t, tc.text, "yaml", manifestPath+"|yaml/stringData/password")
			_, err := Extract(req)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != RuleGuardValue || !slices.Contains(r.Paths, tc.path) {
				t.Fatalf("got %v, want a %s refusal at %s", err, RuleGuardValue, tc.path)
			}
			control, err := extract(req, false)
			if err != nil || !strings.Contains(string(control.docs), secretText) {
				t.Fatalf("control: %v", err)
			}
		})
	}
}
