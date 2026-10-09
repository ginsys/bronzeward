package ingest

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

const otherSecret = "ingest-test-other-4b1e"

// stagedFrom extracts req, commits it and wraps the result as an envelope would hold it, each
// created value at a synthetic generation path.
func stagedFrom(t *testing.T, req Request) Staged {
	t.Helper()
	c, err := Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	gens := map[string]string{}
	for _, c := range calls {
		gens[c.name] = "gen/c1/claim/" + c.name
	}
	return Staged{Sanitized: s, Generations: gens,
		Baseline: Baseline{Ciphertext: "vault:v1:baseline", DigestKey: "digest/1"}}
}

func paths(t *testing.T, marks ...string) []Path {
	t.Helper()
	var out []Path
	for _, m := range marks {
		p, err := ParsePath(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func wantRule(t *testing.T, err error, rule Rule) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != rule {
		t.Fatalf("got %v, want a %s refusal", err, rule)
	}
}

// Compilation §3.6 item 3: a mark on a paused claim re-enters §2.3 at step 3 on the staged
// sanitized document. It extracts only what it marks, keeps every earlier reference, and changes
// no other text.
func TestRemarkExtractsNewMark(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  network:\n    hostname: "+otherSecret+"\n", "doc[0]/machine/token"))
	before := string(st.Sanitized.Documents())
	c, err := Remark(st, paths(t, "doc[0]/machine/network/hostname"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	out := string(s.Documents())
	if len(calls) != 1 || calls[0].kind != provider.KindString || strings.Contains(out, otherSecret) {
		t.Fatalf("%d creates; output:\n%s", len(calls), out)
	}
	want := strings.Replace(before, otherSecret, refTag+" "+calls[0].name, 1)
	if out != want {
		t.Fatalf("the remark changed other text:\n%s\nwant\n%s", out, want)
	}
	if refs := s.Declarations().References; len(refs) != 2 {
		t.Fatalf("declarations %v", refs)
	}
}

// Compilation §3.6 item 3: a mapping holding a reference has no kind, and the reference's earlier
// value is in the provider: the mark is refused mark-kind.
func TestRemarkMappingWithReference(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    a: "+otherSecret+"\n    b: admin\n", "doc[0]/machine/nodeLabels/a"))
	_, err := Remark(st, paths(t, "doc[0]/machine/nodeLabels"))
	wantRule(t, err, RuleMarkKind)
}

// Compilation §3.6 item 3: the string of an identified embedded document holding a reference
// anywhere inside it is refused mark-kind, alone or as a member of a marked mapping.
func TestRemarkEmbeddedWithReference(t *testing.T) {
	st := stagedFrom(t, embeddedRequest(t, manifestStream(secretManifest), "yaml", manifestPath+"|yaml/stringData/password"))
	for _, mark := range []string{manifestPath, "doc[0]/cluster/inlineManifests/0"} {
		_, err := Remark(st, paths(t, mark))
		wantRule(t, err, RuleMarkKind)
	}
}

// Compilation §3.6 item 3: a mark inside an embedded document already written back in the
// encoder's form changes only the node it replaces.
func TestRemarkInsideCanonicalEmbedded(t *testing.T) {
	st := stagedFrom(t, embeddedRequest(t, manifestStream(secretManifest+"    note: "+otherSecret+"\n"), "yaml", manifestPath+"|yaml/stringData/password"))
	before := innerText(t, st.Sanitized.Documents())
	c, err := Remark(st, paths(t, manifestPath+"|yaml/stringData/note"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	if got, want := innerText(t, s.Documents()), strings.Replace(before, otherSecret, refTag+" "+calls[0].name, 1); got != want {
		t.Fatalf("embedded document\n%s\nwant\n%s", got, want)
	}
}

// Compilation §3.6 item 3: an embedded document still in its author's form would be re-encoded by
// the mark, changing text the run cannot guard: refused mark-rewrites-text. Control: the same mark
// at the start of an ingestion is accepted.
func TestRemarkRewritesEmbeddedText(t *testing.T) {
	text := strings.Replace(manifestStream(secretManifest), "name: m\n", "name: manifest-5d2a\n", 1)
	st := stagedFrom(t, embeddedRequest(t, text, "yaml"))
	if !strings.Contains(innerText(t, st.Sanitized.Documents()), "    password") {
		t.Fatal("control: the staged embedded document is not in its author's form")
	}
	inner := manifestPath + "|yaml/stringData/password"
	_, err := Remark(st, paths(t, "doc[0]/cluster/inlineManifests/0/name", inner))
	wantRule(t, err, RuleMarkRewritesText)
	if r := err.(*Refusal); len(r.Paths) != 1 || r.Paths[0] != inner {
		t.Errorf("the refusal names %v, not the mark inside the rewritten document", r.Paths)
	}
	if _, err := Extract(embeddedRequest(t, text, "yaml", manifestPath+"|yaml/stringData/password")); err != nil {
		t.Fatalf("control: at the start the mark is accepted: %v", err)
	}
}

// Persistence §16: re-encoding a staged JSON document as YAML would assemble `public: visible`
// from separate scalars, text no guard of this run searched: refused mark-rewrites-text.
func TestRemarkRewritesEmbeddedJSON(t *testing.T) {
	text := "cluster:\n  inlineManifests:\n    - name: m\n      contents: '{\"public\": \"visible\", \"password\": \"" + secretText + "\"}'\n"
	st := stagedFrom(t, embeddedRequest(t, text, "json"))
	_, err := Remark(st, paths(t, manifestPath+"|json/password"))
	wantRule(t, err, RuleMarkRewritesText)
}

// Compilation §3.6 item 3: a staged document the encoder would write differently is refused
// mark-rewrites-text, whatever the mark addresses.
func TestRemarkRewritesOuterText(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    host: "+otherSecret+"\n"))
	wide := strings.ReplaceAll(string(st.Sanitized.Documents()), "\n  ", "\n    ")
	st.Sanitized = newSanitized([]byte(wide), st.Sanitized.Declarations())
	if err := st.Sanitized.Check(); err != nil {
		t.Fatalf("control: the re-indented document is a sanitized document: %v", err)
	}
	_, err := Remark(st, paths(t, "doc[0]/machine/nodeLabels/host"))
	wantRule(t, err, RuleMarkRewritesText)
}

// Compilation §3.6 item 3: step 5 guards the values this mark extracts.
func TestRemarkGuardsNewValues(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    host: "+otherSecret+"\n    note: "+otherSecret+"\n", "doc[0]/machine/token"))
	_, err := Remark(st, paths(t, "doc[0]/machine/nodeLabels/host"))
	wantRule(t, err, RuleGuardValue)
}

// Compilation §3.6 item 4: a refused mark is named by its position in the request, whichever
// rule refuses it, a guard hit's included (the guard knows only the node it hit). When several
// marks are at fault, the first in the request is named.
func TestRemarkRefusalPosition(t *testing.T) {
	const a, b = "remark-alpha-7c1e", "remark-beta-2d9f"
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    host: "+otherSecret+"\n    note: "+otherSecret+
		"\n    a: "+a+"\n    b: "+b+"\n    c: "+b+"\n", "doc[0]/machine/token"))
	two := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    a: "+a+
		"\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: node-1\n", "doc[0]/machine/token"))
	embedded := stagedFrom(t, embeddedRequest(t, strings.Replace(manifestStream(secretManifest), "name: m\n", "name: manifest-5d2a\n", 1), "yaml"))
	// a's value is the prefix of every minted name: the guard skips this run's references, and so
	// must its attribution, or b's reference is blamed on a.
	prefix := stagedFrom(t, request(t, "machine:\n  nodeLabels:\n    a: s-\n    b: "+b+"\n    c: "+b+"\n"))
	// Embedded marks are identified after outer ones and cross-document loading is checked in
	// document order: neither order may name a later mark when an earlier one is at fault too.
	mixed := stagedFrom(t, embeddedRequest(t, manifestStream(secretManifest+"    note: "+a+"\n")+
		"machine:\n  nodeLabels:\n    x: "+b+"\n    y: "+b+"\n    z: "+a+"\n", "yaml", manifestPath+"|yaml/stringData/password"))
	// a remains only inside z, b exactly in y: the guard-value refusal is b's mark alone.
	mixedRule := stagedFrom(t, embeddedRequest(t, manifestStream(secretManifest+"    note: "+a+"\n")+
		"machine:\n  nodeLabels:\n    x: "+b+"\n    y: "+b+"\n    z: pre-"+a+"-post\n", "yaml", manifestPath+"|yaml/stringData/password"))
	three := stagedFrom(t, request(t, "machine:\n  token: "+secretText+
		"\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: node-1\n---\n"+wireguardDoc, "doc[0]/machine/token"))
	for _, tc := range []struct {
		name  string
		st    Staged
		marks []string
		rule  Rule
		want  int
	}{
		{"unaddressed second", st, []string{"doc[0]/machine/nodeLabels/a", "doc[0]/machine/missing"}, RuleMarkUnaddressed, 1},
		{"unaddressed first", st, []string{"doc[0]/machine/missing", "doc[0]/machine/nodeLabels/a"}, RuleMarkUnaddressed, 0},
		{"kind third", st, []string{"doc[0]/machine/nodeLabels/a", "doc[0]/machine/nodeLabels/b", "doc[0]/machine"}, RuleMarkKind, 2},
		{"guard second", st, []string{"doc[0]/machine/nodeLabels/a", "doc[0]/machine/nodeLabels/host", "doc[0]/machine/nodeLabels/b"},
			RuleGuardValue, 1},
		{"unloadable second", two, []string{"doc[0]/machine/nodeLabels/a", "doc[1]/kind", "doc[1]/apiVersion"}, RuleSchemaUnloadable, 1},
		{"unloadable in a marked document", two, []string{"doc[1]/hostname", "doc[1]/kind"}, RuleSchemaUnloadable, 1},
		{"guard beside a minted prefix", prefix, []string{"doc[0]/machine/nodeLabels/a", "doc[0]/machine/nodeLabels/b"}, RuleGuardValue, 1},
		{"guard embedded first", mixed, []string{manifestPath + "|yaml/stringData/note", "doc[0]/machine/nodeLabels/x"}, RuleGuardValue, 0},
		{"guard rule's own mark", mixedRule, []string{manifestPath + "|yaml/stringData/note", "doc[0]/machine/nodeLabels/x"}, RuleGuardValue, 1},
		{"unloadable later document first", three, []string{"doc[2]/kind", "doc[1]/kind"}, RuleSchemaUnloadable, 0},
		{"bad path second", st, []string{"doc[0]/machine/nodeLabels/a", "doc[0]/machine/nodeLabels/b|yaml/x"}, RuleBadPath, 1},
		{"guard first", st, []string{"doc[0]/machine/nodeLabels/host", "doc[0]/machine/nodeLabels/a"}, RuleGuardValue, 0},
		// Substitution puts a reference at that document's root, which validation refuses after
		// the marks are gone from the stream: the refusal is still the second mark's.
		{"root of a later scalar-only mapping second", two, []string{"doc[0]/machine/nodeLabels/a", "doc[1]"}, RuleTagPlacement, 1},
		{"rewrites second", embedded, []string{"doc[0]/cluster/inlineManifests/0/name", manifestPath + "|yaml/stringData/password"},
			RuleMarkRewritesText, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Remark(tc.st, paths(t, tc.marks...))
			wantRule(t, err, tc.rule)
			if r := err.(*Refusal); r.Position != tc.want {
				t.Errorf("position %d, want %d", r.Position, tc.want)
			}
		})
	}
}

// A remark carries at least one mark: without one it would re-stage the same document.
func TestRemarkNeedsAMark(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n"))
	if c, err := Remark(st, nil); err == nil || c != nil {
		t.Fatalf("got %v, %v; want an error and no candidate", c, err)
	}
}

// changedBesides accepts only the minted references and an embedded document in the encoder's
// form that differs from its re-encoding by them; every other difference names a node.
func TestChangedBesides(t *testing.T) {
	canonical := "x: |\n  a: b\n  c: d\n"
	cases := []struct {
		name, old, next string
		same            bool
	}{
		{"equal", "a: x\n", "a: x\n", true},
		{"minted reference", "a: x\n", "a: !bwref s-new\n", true},
		{"another reference", "a: x\n", "a: !bwref s-old\n", false},
		{"value", "a: x\n", "a: y\n", false},
		{"style", "a: x\n", "a: 'x'\n", false},
		{"comment", "a: x\n", "a: x # c\n", false},
		{"members", "a: x\n", "a: x\nb: y\n", false},
		{"anchor", "a: x\n", "a: &n x\n", false},
		{"embedded minted", canonical, "x: |\n  a: !bwref s-new\n  c: d\n", true},
		{"embedded other", canonical, "x: |\n  a: b\n  c: e\n", false},
		{"embedded not canonical", "x: |\n  a:   b\n  c: d\n", "x: |\n  a: !bwref s-new\n  c: d\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old, err1 := parseStream([]byte(tc.old))
			next, err2 := parseStream([]byte(tc.next))
			if err1 != nil || err2 != nil {
				t.Fatal(err1, err2)
			}
			bad := changedBesides(old[0], next[0], map[string]bool{"s-new": true})
			if (bad == nil) != tc.same {
				t.Errorf("changedBesides = %v, want same %v", bad, tc.same)
			}
		})
	}
}

// Compilation §3.6 item 3: the new envelope holds every earlier generation and the new ones, and
// carries the baseline unchanged; it seals and opens.
func TestRemarkedEnvelope(t *testing.T) {
	st := stagedFrom(t, request(t, "machine:\n  token: "+secretText+"\n  nodeLabels:\n    host: "+otherSecret+"\n", "doc[0]/machine/token"))
	c, err := Remark(st, paths(t, "doc[0]/machine/nodeLabels/host"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	next := st.Remarked(s, map[string]string{calls[0].name: "gen/c1/claim/" + calls[0].name})
	if len(next.Generations) != 2 || next.Baseline != st.Baseline {
		t.Fatalf("envelope %+v", next)
	}
	plain, sum, err := next.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(plain, sum); err != nil {
		t.Fatal(err)
	}
	if len(st.Generations) != 1 {
		t.Fatalf("the earlier envelope changed: %v", st.Generations)
	}
}
