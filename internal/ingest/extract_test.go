package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

const labelSecret = "label-secret-91be"

// multiDoc holds a schema secret in document 0 (machine.token), a marked label whose key holds
// "/", "~" and ".", and a WireguardConfig at doc[1] with two schema secrets.
const multiDoc = "machine:\n  token: " + secretText + "\n  nodeLabels:\n    a/b~c.d: " + labelSecret + "\n---\n" + wireguardDoc

var mintedName = regexp.MustCompile(`^s-[a-z2-7]{26}$`)

func request(t *testing.T, text string, marks ...string) Request {
	t.Helper()
	u, err := Read(strings.NewReader(text), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Input: u}
	for _, m := range marks {
		p, err := ParsePath(m)
		if err != nil {
			t.Fatal(err)
		}
		req.Marks = append(req.Marks, p)
	}
	return req
}

type created struct {
	name string
	kind provider.Kind
}

func commitAll(t *testing.T, c *Candidate) (Sanitized, []created) {
	t.Helper()
	var calls []created
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		calls = append(calls, created{name, v.Kind()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, calls
}

func TestSubstituteValues(t *testing.T) {
	docs, err := parseStream([]byte(multiDoc))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := ParsePath("doc[0]/machine/nodeLabels/a~1b~0c.d")
	targets, err := identify(docs, []Path{p})
	if err != nil {
		t.Fatal(err)
	}
	exs, err := substitute(targets, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, ex := range exs {
		if !mintedName.MatchString(ex.name) {
			t.Errorf("name %q", ex.name)
		}
		got[ex.paths[0].String()] = ex.plain
		n, ok := resolve(docs, ex.paths[0].Doc, ex.paths[0].Pointer)
		if !ok || n.Tag != refTag || n.Value != ex.name {
			t.Errorf("%s was not replaced by its reference", ex.paths[0])
		}
	}
	want := map[string]any{
		"doc[0]/machine/token":                secretText,
		"doc[0]/machine/nodeLabels/a~1b~0c.d": labelSecret,
		"doc[1]/privateKey":                   wgPrivate,
		"doc[1]/peers/0/presharedKey":         wgPreshared,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extracted %v", got)
	}
}

func TestExtractAndCommit(t *testing.T) {
	c, err := Extract(request(t, multiDoc, "doc[0]/machine/nodeLabels/a~1b~0c.d"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	out := string(s.Documents())
	for _, secret := range []string{secretText, labelSecret, wgPrivate, wgPreshared} {
		if strings.Contains(out, secret) {
			t.Errorf("the sanitized stream holds an extracted value:\n%s", out)
		}
	}
	docs, err := parseStream(s.Documents())
	if err != nil || len(docs) != 2 {
		t.Fatalf("the sanitized stream does not re-parse as 2 documents: %v", err)
	}
	decl := s.Declarations()
	if len(calls) != 4 || len(decl.References) != 4 {
		t.Fatalf("%d creates, %d declarations", len(calls), len(decl.References))
	}
	for _, call := range calls {
		r, ok := decl.References[call.name]
		if !ok || r != (Reference{Kind: provider.KindString, Version: 1}) || call.kind != provider.KindString {
			t.Errorf("%s: declared %+v, created as %s", call.name, r, call.kind)
		}
	}
	if err := validate(docs, decl); err != nil {
		t.Errorf("the sanitized stream fails authoring validation: %v", err)
	}
}

func TestExtractAliasOnce(t *testing.T) {
	c, err := Extract(request(t, "machine:\n  token: &t "+secretText+"\ncluster:\n  token: *t\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	out := string(s.Documents())
	if len(calls) != 1 || strings.Count(out, refTag) != 1 || !strings.Contains(out, "*t") || !strings.Contains(out, "&t") {
		t.Errorf("%d creates; output:\n%s", len(calls), out)
	}
}

func TestExtractKinds(t *testing.T) {
	text := "machine:\n  install:\n    wipe: true\n  nodeLabels:\n    a: x\n    b: \"2\"\ncluster:\n  controlPlane:\n    localAPIServerPort: 6443\n"
	c, err := Extract(request(t, text, "doc[0]/machine/install/wipe", "doc[0]/machine/nodeLabels", "doc[0]/cluster/controlPlane/localAPIServerPort"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	var kinds []provider.Kind
	for _, call := range calls {
		kinds = append(kinds, call.kind)
		if s.Declarations().References[call.name].Kind != call.kind {
			t.Errorf("%s declared with another kind", call.name)
		}
	}
	if !reflect.DeepEqual(kinds, []provider.Kind{provider.KindBoolean, provider.KindMapping, provider.KindInteger}) {
		t.Errorf("kinds %v", kinds)
	}
}

func TestExtractRefusalCreatesNothing(t *testing.T) {
	c, err := Extract(request(t, multiDoc, "doc[0]/machine/nope"))
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != RuleMarkUnaddressed || c != nil {
		t.Fatalf("%v, candidate %v", err, c != nil)
	}
}

func TestCommitFailureAndOnce(t *testing.T) {
	c, err := Extract(request(t, multiDoc))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	fail := errors.New("provider refused")
	s, err := c.Commit(context.Background(), func(context.Context, string, provider.Value) error {
		n++
		if n == 2 {
			return fail
		}
		return nil
	})
	if !errors.Is(err, fail) || s.Check() == nil {
		t.Fatalf("a failed create: %v, sanitized %v", err, s.Check())
	}
	if _, err := c.Commit(context.Background(), func(context.Context, string, provider.Value) error {
		n++
		return nil
	}); err == nil || n != 2 {
		t.Fatalf("a second commit: %v after %d creates", err, n)
	}
	var zero *Candidate
	if _, err := zero.Commit(context.Background(), nil); err == nil {
		t.Fatal("a nil candidate committed")
	}
}

// TestCommitErrorQuotesNothing: the create callback's message can hold anything; Commit's error
// names the reference and keeps the cause reachable through errors.Is, but never renders it.
func TestCommitErrorQuotesNothing(t *testing.T) {
	c, err := Extract(request(t, "machine:\n  token: "+secretText+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("provider refused")
	_, err = c.Commit(context.Background(), func(context.Context, string, provider.Value) error {
		return fmt.Errorf("%w: writing %s", cause, secretText)
	})
	if !errors.Is(err, cause) {
		t.Fatalf("the cause is not reachable: %v", err)
	}
	var ce *CreateError
	if !errors.As(err, &ce) || !mintedName.MatchString(ce.Name) {
		t.Fatalf("not a CreateError naming the reference: %v", err)
	}
	for _, text := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%s", err)} {
		if strings.Contains(text, secretText) {
			t.Errorf("the error renders the callback's message: %s", text)
		}
	}
}

// TestReingestAliasedOutput: a sanitized stream whose reference is anchored and aliased is valid
// input again; re-ingesting it with its declarations extracts nothing new.
func TestReingestAliasedOutput(t *testing.T) {
	c, err := Extract(request(t, "machine:\n  token: &t "+secretText+"\ncluster:\n  token: *t\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	if len(calls) != 1 {
		t.Fatalf("%d creates", len(calls))
	}
	u, err := Read(bytes.NewReader(s.Documents()), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Extract(Request{Input: u, Declarations: s.Declarations()})
	if err != nil {
		t.Fatalf("re-ingesting the sanitized stream: %v", err)
	}
	if _, calls := commitAll(t, again); len(calls) != 0 {
		t.Fatalf("re-ingestion created %d values", len(calls))
	}
}
