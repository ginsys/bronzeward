package compile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The typed CA-key reference through the compiler (compilation.md §6, §8.3, §16.40;
// persistence-api.md §16). Every key here is generated for the test.

// caKeyVersion is the version the publication pins the reference to.
const caKeyVersion = 3

// caBases is a generated control plane base twice: with machine.ca.key as the typed reference, and
// as the control with the key written there literally, which nothing then looks for. It also gives
// the key's two forms.
func caBases(t *testing.T) (typed, literal Source, pem, encoded string) {
	t.Helper()
	s := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	var doc yaml.Node
	if err := yaml.Unmarshal(s.Text.Documents(), &doc); err != nil {
		t.Fatal(err)
	}
	at := member(t, member(t, member(t, doc.Content[0], "machine"), "ca"), "key")
	name := at.Value
	x, err := s.Values[name].Decode()
	if err != nil {
		t.Fatal(err)
	}
	encoded = x.(string)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	pem = string(raw)
	tag := "!bwref " + name
	text := string(s.Text.Documents())
	if strings.Count(text, tag) != 1 {
		t.Fatalf("the base holds the key's reference %d times", strings.Count(text, tag))
	}

	with := func(replacement string, add map[string]ingest.Reference, values map[string]provider.Value) Source {
		decl := s.Text.Declarations()
		delete(decl.References, name)
		for n, r := range add {
			decl.References[n] = r
		}
		next := strings.Replace(text, tag, replacement, 1)
		st, err := ingest.Stored(next, decl)
		if err != nil {
			t.Fatal(err)
		}
		all := map[string]provider.Value{}
		for n, v := range s.Values {
			if n != name {
				all[n] = v
			}
		}
		for n, v := range values {
			all[n] = v
		}
		return Source{Revision: s.Revision, Digest: s.Digest, Text: st, Values: all}
	}
	typed = with("!bwref "+ingest.CAKeyName,
		map[string]ingest.Reference{ingest.CAKeyName: {Kind: provider.KindCAKey, Pinned: caKeyVersion}},
		map[string]provider.Value{ingest.CAKeyName: value(t, provider.KindString, pem)})
	literal = with(encoded, nil, nil)
	return typed, literal, pem, encoded
}

func member(t *testing.T, n *yaml.Node, key string) *yaml.Node {
	t.Helper()
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	t.Fatalf("no member %q", key)
	return nil
}

// stored is a fragment as the database holds one, without ingestion's own loading of it.
func stored(t *testing.T, text string) Source {
	t.Helper()
	st, err := ingest.Stored(text, ingest.Declarations{})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(text))
	return Source{Revision: "rev-" + hex.EncodeToString(sum[:4]), Digest: hex.EncodeToString(sum[:]), Text: st}
}

func noKey(t *testing.T, what, s, pem, encoded string) {
	t.Helper()
	body := strings.Split(strings.TrimSpace(pem), "\n")[1]
	for _, form := range []string{pem, encoded, body} {
		if strings.Contains(s, form) {
			t.Fatalf("%s holds the CA key", what)
		}
	}
}

func TestCompileRendersTheCAKey(t *testing.T) {
	typed, _, pem, encoded := caBases(t)
	c := compiled(t, Input{Base: typed, Mode: ModeMetal})
	if !strings.Contains(string(c.Materialized().bytes()), "key: "+encoded) {
		t.Fatal("the rendered configuration does not hold the key's base64 form at its place")
	}
	if !slices.Contains(c.Effective(), Dependency{Reference: ingest.CAKeyName, Version: caKeyVersion}) {
		t.Fatalf("effective dependencies %v lack the CA key at version %d", c.Effective(), caKeyVersion)
	}
	var records int
	for _, r := range c.Provenance() {
		if r.Reference == ingest.CAKeyName {
			records++
			if r.Version != caKeyVersion || r.Kind != string(provider.KindCAKey) || r.Encoding != "" || r.Output == "" {
				t.Fatalf("provenance %+v", r)
			}
		}
	}
	if records != 1 {
		t.Fatalf("%d provenance records of the CA key", records)
	}
	redacted, err := c.Redacted()
	if err != nil {
		t.Fatal(err)
	}
	noKey(t, "the redacted configuration", redacted, pem, encoded)
	if !strings.Contains(redacted, "<redacted:"+ingest.CAKeyName+"@"+strconv.Itoa(caKeyVersion)+">") {
		t.Fatal("the redacted configuration has no token of the CA key")
	}
}

// A control plane configuration without the key is refused by the pinned machinery: the render
// must resolve the reference.
func TestControlPlaneWithoutCAKeyFailsValidation(t *testing.T) {
	typed, _, _, encoded := caBases(t)
	c := compiled(t, Input{Base: typed, Mode: ModeMetal})
	text := string(c.Materialized().bytes())
	without := strings.Replace(text, "key: "+encoded, `key: ""`, 1)
	if without == text {
		t.Fatal("the key was not found")
	}
	if err := (Materialized{s: &without}).Validate(ModeMetal); err == nil {
		t.Fatal("a control plane configuration without machine.ca.key validated")
	}
}

// A literal copy of either form in a fragment is refused by path alone; the control, a base that
// holds no reference, shows that only the reference's value refuses it.
func TestCompileRefusesACopyOfTheCAKey(t *testing.T) {
	typed, literal, pem, encoded := caBases(t)
	for form, copied := range map[string]string{"base64": encoded, "PEM": pem} {
		t.Run(form, func(t *testing.T) {
			fragment := stored(t, "machine:\n  env:\n    COPY: "+strconv.Quote(copied)+"\n")
			_, err := Compile(Input{Base: typed, Fragments: []Source{fragment}, Mode: ModeMetal})
			var e *Error
			if !errors.As(err, &e) || e.Rule != RuleCopy || !slices.Equal(e.Paths, []string{"doc[0]/machine/env/COPY"}) {
				t.Fatalf("got %v, want %s at the copy's path", err, RuleCopy)
			}
			noKey(t, "the refusal", err.Error(), pem, encoded)
		})
	}
	fragment := stored(t, "machine:\n  env:\n    COPY: "+strconv.Quote(encoded)+"\n")
	c := compiled(t, Input{Base: literal, Fragments: []Source{fragment}, Mode: ModeMetal})
	if redacted, err := c.Redacted(); err != nil || !strings.Contains(redacted, encoded) {
		t.Fatalf("control: without the reference the redacted configuration did not show the copy: %v", err)
	}
}

// The machinery's message for a rejected fragment quotes what it could not load: a copy of the key
// there is replaced, and the control shows it.
func TestCompileRedactsTheCAKeyInAMessage(t *testing.T) {
	typed, literal, pem, encoded := caBases(t)
	fragment := stored(t, "machine:\n  notAField: "+encoded+"\n")
	_, err := Compile(Input{Base: typed, Fragments: []Source{fragment}, Mode: ModeMetal})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleRejected || e.Message == "" {
		t.Fatalf("got %v, want a rejection with a message", err)
	}
	noKey(t, "the message", err.Error(), pem, encoded)
	_, err = Compile(Input{Base: literal, Fragments: []Source{fragment}, Mode: ModeMetal})
	if err == nil || !strings.Contains(err.Error(), encoded) {
		t.Fatal("control: without the reference the message did not quote the copy")
	}
}

// A fragment cannot override the import base's reference (choice §16.20).
func TestCompileRefusesAnOverrideOfTheCAKey(t *testing.T) {
	typed, _, _, encoded := caBases(t)
	// Another synthetic value of the same shape, as an operator's own key would be.
	encoded = base64.StdEncoding.EncodeToString([]byte("another synthetic key, not the base's: " + encoded[:8]))
	fragment := stored(t, "machine:\n  ca:\n    key: "+encoded+"\n")
	_, err := Compile(Input{Base: typed, Fragments: []Source{fragment}, Mode: ModeMetal})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleBaseOverride {
		t.Fatalf("got %v, want %s", err, RuleBaseOverride)
	}
}
