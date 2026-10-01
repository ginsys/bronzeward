package ingest

import (
	"bytes"
	"context"
	"encoding/hex"
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
	// The mapping's keys are guarded like its members, so they must not occur elsewhere.
	text := "machine:\n  install:\n    wipe: true\n  nodeLabels:\n    lbl-1: x\n    lbl-2: \"2\"\ncluster:\n  controlPlane:\n    localAPIServerPort: 6443\n"
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
	for _, e := range []error{err, errors.Join(err), fmt.Errorf("caller: %w", err)} {
		if !errors.Is(e, cause) {
			t.Errorf("the cause is not reachable through %T", e)
		}
		// A reporter that walks the chain renders every error in it.
		for _, link := range chain(e) {
			if strings.Contains(link.Error(), secretText) {
				t.Errorf("the chain of %T hands out the callback's error: %s", e, link)
			}
		}
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%f", "%t", "%p"} {
			text := fmt.Sprintf(verb, e)
			if strings.Contains(text, secretText) || strings.Contains(strings.ToLower(text), hex.EncodeToString([]byte(secretText))) {
				t.Errorf("%s of %T renders the callback's message: %s", verb, e, text)
			}
		}
	}
}

// chain is e and every error its Unwrap methods return, depth first.
func chain(e error) []error {
	out := []error{e}
	switch u := e.(type) {
	case interface{ Unwrap() error }:
		if next := u.Unwrap(); next != nil {
			out = append(out, chain(next)...)
		}
	case interface{ Unwrap() []error }:
		for _, next := range u.Unwrap() {
			out = append(out, chain(next)...)
		}
	}
	return out
}

// TestExtractCoalescesNestedTargets: a target inside a marked mapping is extracted as part of
// that mapping, once, whether the schema identifies it or a second mark names it. A member that
// is an alias of a target outside the mapping is refused, since the mapping cannot carry it.
func TestExtractCoalescesNestedTargets(t *testing.T) {
	const auth = "doc[0]/machine/registries/config/reg.test/auth"
	text := "machine:\n  token: " + secretText + "\n  registries:\n    config:\n      reg.test:\n        auth:\n          username: user-91be\n          password: " + labelSecret + "\n"
	for _, marks := range [][]string{{auth}, {auth, auth + "/password"}, {auth + "/password", auth}} {
		c, err := Extract(request(t, text, marks...))
		if err != nil {
			t.Fatalf("%v: %v", marks, err)
		}
		s, calls := commitAll(t, c)
		var kinds []provider.Kind
		for _, cl := range calls {
			kinds = append(kinds, cl.kind)
		}
		if len(calls) != 2 || !reflect.DeepEqual(kinds, []provider.Kind{provider.KindString, provider.KindMapping}) {
			t.Errorf("%v: created %v, want the token then the auth mapping", marks, calls)
		}
		if docs := string(s.Documents()); strings.Contains(docs, labelSecret) || strings.Contains(docs, "user-91be") {
			t.Errorf("%v: the sanitized stream holds the mapping:\n%s", marks, docs)
		}
	}
	aliased := "machine:\n  token: &t " + secretText + "\n  nodeLabels:\n    a: *t\n    b: x\n"
	if _, err := Extract(request(t, aliased, "doc[0]/machine/nodeLabels")); !isRule(err, RuleMarkKind) {
		t.Errorf("a member aliasing another target: %v, want a %s refusal", err, RuleMarkKind)
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
