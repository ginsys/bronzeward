package ingest

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// Compilation §2.3 step 2a, the legacy key: a machine.network.hostname equal to the machine's name is
// removed as a key alone, where the stream holds no HostnameConfig document and the stable-hostname guard
// holds, and is recorded as filled and not the fill's own. The names here are synthetic.

const (
	keyBase     = "machine:\n  type: worker\n  network:\n    hostname: node-a\n"
	keyBaseOnly = "machine:\n  network:\n    hostname: node-a\n"
	keySibling  = "machine:\n  type: worker\n  network:\n    nameservers:\n      - 192.0.2.53\n    hostname: node-a\n"
	keyStable   = "machine:\n  features:\n    stableHostname: false\n  network:\n    hostname: node-a\n"
	keyOther    = "---\napiVersion: v1alpha1\nkind: KubeSpanConfig\nenabled: true\nmtu: 1380\n"
)

// keyRefs declares the references the stays-cases write: a string, a boolean and a mapping.
var keyRefs = Declarations{References: map[string]Reference{
	"s-name":  {Kind: provider.KindString, Version: 1},
	"s-stab":  {Kind: provider.KindBoolean, Version: 1},
	"s-feats": {Kind: provider.KindMapping, Version: 1},
}}

// extractKey is extractNamed with the declarations the reference cases need.
func extractKey(t *testing.T, name, text string, marks ...string) (string, Filled, error) {
	t.Helper()
	req := request(t, text, marks...)
	req.Name = name
	// Only the references the text uses: a declaration nothing uses is itself a refusal.
	req.Declarations = Declarations{References: map[string]Reference{}}
	for name, ref := range keyRefs.References {
		if strings.Contains(text, " "+name+"\n") {
			req.Declarations.References[name] = ref
		}
	}
	c, err := Extract(req)
	if err != nil {
		return "", Filled{}, err
	}
	s, _ := commitAll(t, c)
	return string(s.Documents()), c.Filled(), nil
}

// K1: the key goes alone, its parent mapping stays even left empty, no document goes, and the record says
// a key (Equal is false: the node holds a v1alpha1 key and the release sends a document).
func TestFilledHostnameKeyRemoved(t *testing.T) {
	for _, tc := range []struct {
		label, text string
		keeps       []string
	}{
		{"the key alone leaves an empty parent mapping", keyBaseOnly, []string{"network:"}},
		{"a sibling key stays", keySibling, []string{"nameservers:", "192.0.2.53", "type: worker"}},
		{"another document stays", keyBase + keyOther, []string{"type: worker", "kind: KubeSpanConfig", "mtu: 1380"}},
		{"stableHostname false: the key goes, the setting stays", keyStable, []string{"stableHostname: false"}},
		{"a lone document", keyBase, []string{"type: worker"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			docs, filled, err := extractKey(t, "node-a", tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(docs, "hostname:") || strings.Contains(docs, "node-a") {
				t.Errorf("the key stays:\n%s", docs)
			}
			for _, k := range tc.keeps {
				if !strings.Contains(docs, k) {
					t.Errorf("%q is gone:\n%s", k, docs)
				}
			}
			if filled != (Filled{Name: "node-a", Key: true}) {
				t.Errorf("recorded %+v, want the key recorded as not the fill's own", filled)
			}
		})
	}
}

// K1: the stream's documents are those of the input, so a mark after the key's document keeps its position.
func TestFilledHostnameKeyKeepsPositions(t *testing.T) {
	text := keyBase + "---\nmachine:\n  nodeLabels:\n    k: " + labelSecret + "\n"
	docs, filled, err := extractKey(t, "node-a", text, "doc[1]/machine/nodeLabels/k")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(docs, labelSecret) || strings.Contains(docs, "node-a") || !filled.Key {
		t.Errorf("filled %+v, documents:\n%s", filled, docs)
	}
}

// K2: the key stays, and nothing is recorded, in each case the specification lists.
func TestFilledHostnameKeyStays(t *testing.T) {
	doc := func(h string) string {
		return "---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + h + "\n"
	}
	for _, tc := range []struct {
		label, name, text string
	}{
		{"a different hostname", "node-a", strings.Replace(keyBase, "node-a", "node-b", 1)},
		{"a different case", "Node-A", keyBase},
		{"a full hostname against a short name", "node-a", strings.Replace(keyBase, "node-a", "node-a.example.org", 1)},
		{"a machine with no name", "", keyBase},
		{"an empty hostname", "node-a", strings.Replace(keyBase, "node-a", "\"\"", 1)},
		{"a HostnameConfig document with another hostname", "node-a", keyBase + doc("node-b")},
		{"a hostname that is a number", "123", strings.Replace(keyBase, "node-a", "123", 1)},
		{"a hostname that is a reference", "node-a", strings.Replace(keyBase, "hostname: node-a", "hostname: !bwref s-name", 1)},
		{"stableHostname true", "node-a", strings.Replace(keyStable, "false", "true", 1)},
		{"stableHostname a reference", "node-a", strings.Replace(keyStable, "false", "!bwref s-stab", 1)},
		{"features a reference", "node-a", "machine:\n  features: !bwref s-feats\n  network:\n    hostname: node-a\n"},
		{"features null", "node-a", "machine:\n  features:\n  network:\n    hostname: node-a\n"},
		{"two documents hold the key", "node-a", keyBase + "---\n" + keyBase},
		{"the network mapping is shared by an alias", "node-a", "machine:\n  network: &net {hostname: node-a}\n  nodeLabels: *net\n"},
		{"the hostname node carries an anchor", "node-a", strings.Replace(keyBase, "hostname: node-a", "hostname: &h node-a", 1)},
	} {
		t.Run(tc.label, func(t *testing.T) {
			docs, filled, err := extractKey(t, tc.name, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if filled != (Filled{}) {
				t.Errorf("recorded %+v, want nothing", filled)
			}
			if !strings.Contains(docs, "hostname") {
				t.Errorf("the key was removed:\n%s", docs)
			}
		})
	}
}

// K2 for shapes the machinery's loader refuses as a whole, so no import reaches them: the rule itself still
// keeps the key, which this reads straight from the parsed stream. A hostname that is a sequence,
// stableHostname that is not a boolean and features that is a scalar each keep it.
func TestReleaseLegacyKeyKeepsWhatItCannotRead(t *testing.T) {
	for label, text := range map[string]string{
		"a hostname that is a sequence": strings.Replace(keyBase, "hostname: node-a", "hostname: [node-a]", 1),
		"stableHostname not a boolean":  strings.Replace(keyStable, "false", "\"false\"", 1),
		"features a scalar":             "machine:\n  features: x\n  network:\n    hostname: node-a\n",
		"features an alias":             "machine:\n  other: &f {}\n  features: *f\n  network:\n    hostname: node-a\n",
		// Review round 1: removing from a mapping an alias also reaches would empty the other member as well.
		"network anchored and aliased": "machine:\n  network: &net {hostname: node-a}\n  nodeLabels: *net\n",
		"machine anchored and aliased": "machine: &m\n  network:\n    hostname: node-a\nother: *m\n",
		// Review round 2: an anchor on the key node itself is redefined by removing it, so a later alias would read another node.
		"hostname key anchored":               "machine:\n  type: &h worker\n  network:\n    &h hostname: node-a\n  nodeLabels:\n    role: *h\n",
		"kind of a document through an alias": keyBase + "---\napiVersion: v1alpha1\nhostname: &k HostnameConfig\nkind: *k\n",
		"a control: the plain key is read":    keyBase,
	} {
		t.Run(label, func(t *testing.T) {
			docs, err := parseStream([]byte(text))
			if err != nil {
				t.Fatal(err)
			}
			filled, err := releaseLegacyKey(docs, "node-a", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.HasPrefix(label, "a control")
			if (filled == Filled{Name: "node-a", Key: true}) != want {
				t.Errorf("recorded %+v, released = %v", filled, want)
			}
		})
	}
}

// K2, the document wins: with an equal HostnameConfig document present, step 2a removes the document, as
// before, and the key (which Talos refuses beside it) is left as it is.
func TestFilledHostnameKeyStaysBesideAnEqualDocument(t *testing.T) {
	text := keyBase + "---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: node-a\n"
	docs, filled, err := extractKey(t, "node-a", text)
	if err != nil {
		t.Fatal(err)
	}
	if filled != (Filled{Name: "node-a", Equal: true}) || strings.Contains(docs, "HostnameConfig") || !strings.Contains(docs, "hostname: node-a") {
		t.Errorf("filled %+v, documents:\n%s", filled, docs)
	}
}

// K8: a mark at import on stableHostname, or on a mapping that holds it or the key, keeps the key: the
// reference would make step 5a suppress the fill and the hostname would be lost.
func TestFilledHostnameKeyStaysUnderAMark(t *testing.T) {
	for _, tc := range []struct{ label, text, mark string }{
		{"stableHostname itself", keyStable, "doc[0]/machine/features/stableHostname"},
		{"the features mapping", keyStable, "doc[0]/machine/features"},
		{"the network mapping that holds the key", keyBase, "doc[0]/machine/network"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			docs, filled, err := extractKey(t, "node-a", tc.text, tc.mark)
			if err != nil {
				t.Fatal(err)
			}
			if filled != (Filled{}) {
				t.Errorf("recorded %+v, want nothing", filled)
			}
			if tc.mark == "doc[0]/machine/network" {
				return // the mapping, key included, is now a reference
			}
			if !strings.Contains(docs, "hostname: node-a") {
				t.Errorf("the key was removed:\n%s", docs)
			}
		})
	}
}

// K3: a mark that addresses the removed key refuses the input as a mark that addresses no node does, and
// the refusal keeps the input's positions.
func TestFilledHostnameKeyMarkRefused(t *testing.T) {
	req := request(t, keyBase+"---\nmachine:\n  nodeLabels:\n    k: "+labelSecret+"\n", "doc[1]/machine/nodeLabels/k", "doc[0]/machine/network/hostname")
	req.Name = "node-a"
	_, err := Extract(req)
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != RuleMarkUnaddressed || ref.Position != 1 {
		t.Fatalf("got %v, want a %s refusal at the second mark", err, RuleMarkUnaddressed)
	}
	// Control: the same mark with no removal (another name) is an ordinary mark.
	req.Name = "node-b"
	if _, err := Extract(req); err != nil {
		t.Errorf("control: %v", err)
	}
}

// The key is not guarded: a value it holds, or a comment on it, refuses nothing.
func TestFilledHostnameKeyIsNotGuarded(t *testing.T) {
	text := "machine:\n  network:\n    # node-a\n    hostname: node-a\n---\nmachine:\n  nodeLabels:\n    safe: node-a\n"
	req := request(t, text, "doc[1]/machine/nodeLabels/safe")
	req.Name = "node-a"
	if _, err := Extract(req); err != nil {
		t.Errorf("a label equal to the removed key: %v", err)
	}
}

// stagedKey is the envelope a request for a machine named node-a stages, with the removal's record.
func stagedKey(t *testing.T, text string) Staged {
	t.Helper()
	req := request(t, text)
	req.Name = "node-a"
	return stagedFrom(t, req)
}

// K9: where step 2a removed a legacy key, a later mark on stableHostname or on a mapping that holds it is
// refused with its own rule, since the reference would suppress the fill and lose the hostname.
func TestRemarkRefusedWhereAKeyWasRemoved(t *testing.T) {
	st := stagedKey(t, keyStable+keyOther)
	if !st.Filled.Key {
		t.Fatalf("control: the staged record is %+v", st.Filled)
	}
	for _, mark := range []string{
		"doc[0]/machine/features/stableHostname", "doc[0]/machine/features", "doc[0]/machine", "doc[0]",
	} {
		t.Run(mark, func(t *testing.T) {
			_, err := Remark(st, paths(t, mark))
			wantRule(t, err, RuleMarkOnReleasedHostname)
		})
	}
	// A mark that holds neither the setting nor the key's document is an ordinary mark.
	if _, err := Remark(st, paths(t, "doc[1]/mtu")); err != nil {
		t.Errorf("a mark on another document: %v", err)
	}
}

// The control for K9: with no key removed, the same mark is ordinary (the stableHostname setting is the
// operator's to mark), and a document's removal records no key.
func TestRemarkOnStableHostnameWithoutAKeyRemoved(t *testing.T) {
	text := "machine:\n  features:\n    stableHostname: false\n"
	st := stagedKey(t, text)
	if st.Filled != (Filled{}) {
		t.Fatalf("control: the staged record is %+v", st.Filled)
	}
	if _, err := Remark(st, paths(t, "doc[0]/machine/features/stableHostname")); err != nil {
		t.Errorf("a mark with no key removed: %v", err)
	}
}
