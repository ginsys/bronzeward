package ingest

import (
	"errors"
	"strings"
	"testing"
)

// Compilation §2.3 step 2a: a HostnameConfig whose hostname equals the machine's name is removed from
// the stored stream, whole, and recorded. The names here are synthetic.

const (
	hostDocFill  = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: node-a\n"
	hostDocBare  = "apiVersion: v1alpha1\nkind: HostnameConfig\nhostname: node-a\n"
	hostDocOther = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: node-b\n"
	hostMachine  = "machine:\n  type: worker\n"
	hostLabels   = "machine:\n  nodeLabels:\n    k: " + labelSecret + "\n"
)

// extractNamed runs Extract on text for a machine named name and returns the sanitized documents.
func extractNamed(t *testing.T, name, text string, marks ...string) (string, Filled) {
	t.Helper()
	req := request(t, text, marks...)
	req.Name = name
	c, err := Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := commitAll(t, c)
	return string(s.Documents()), c.Filled()
}

func TestFilledHostnameRemoved(t *testing.T) {
	for _, tc := range []struct {
		label string
		name  string
		text  string
		equal bool
	}{
		{"the hostname equals the name", "node-a", hostMachine + "---\n" + hostDocFill, true},
		{"a bare document has no auto: removed, not equal to the fill", "node-a", hostMachine + "---\n" + hostDocBare, false},
		{"`auto: null` leaves Talos's pointer unset: removed, not equal to the fill", "node-a",
			hostMachine + "---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: null\nhostname: node-a\n", false},
		{"a full hostname equal to a full name", "node-a.example.org",
			hostMachine + "---\n" + strings.Replace(hostDocFill, "node-a", "node-a.example.org", 1), true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			docs, filled := extractNamed(t, tc.name, tc.text)
			if strings.Contains(docs, "HostnameConfig") {
				t.Errorf("the document stays:\n%s", docs)
			}
			if !strings.Contains(docs, "type: worker") {
				t.Errorf("another document is gone:\n%s", docs)
			}
			if filled != (Filled{Name: tc.name, Equal: tc.equal}) {
				t.Errorf("recorded %+v, want name %q equal %v", filled, tc.name, tc.equal)
			}
		})
	}
}

func TestFilledHostnameStays(t *testing.T) {
	for _, tc := range []struct {
		label string
		name  string
		text  string
	}{
		{"a different hostname", "node-a", hostMachine + "---\n" + hostDocOther},
		{"a different case", "Node-A", hostMachine + "---\n" + hostDocFill},
		{"a full hostname against a short name", "node-a", hostMachine + "---\n" + strings.Replace(hostDocFill, "node-a", "node-a.example.org", 1)},
		{"a machine with no name", "", hostMachine + "---\n" + hostDocFill},
		{"no HostnameConfig", "node-a", hostMachine},
	} {
		t.Run(tc.label, func(t *testing.T) {
			docs, filled := extractNamed(t, tc.name, tc.text)
			if strings.Contains(tc.text, "HostnameConfig") != strings.Contains(docs, "HostnameConfig") {
				t.Errorf("the document was changed:\n%s", docs)
			}
			if filled != (Filled{}) {
				t.Errorf("recorded %+v, want nothing", filled)
			}
		})
	}
}

// The stream's positions are those of the input: a mark on a document after the removed one still names
// that document, and a mark on the removed one is refused as a mark that addresses no node.
func TestFilledHostnameKeepsPositions(t *testing.T) {
	text := hostMachine + "---\n" + hostDocFill + "---\n" + hostLabels
	docs, filled := extractNamed(t, "node-a", text, "doc[2]/machine/nodeLabels/k")
	if strings.Contains(docs, labelSecret) || strings.Contains(docs, "HostnameConfig") || filled.Name != "node-a" {
		t.Errorf("filled %+v, documents:\n%s", filled, docs)
	}
	req := request(t, text, "doc[1]/hostname")
	req.Name = "node-a"
	_, err := Extract(req)
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != RuleMarkUnaddressed {
		t.Errorf("a mark on the removed document: got %v, want a %s refusal", err, RuleMarkUnaddressed)
	}
}

// A document that is the stream's only one is never removed: nothing would be left to store.
func TestFilledHostnameKeepsALoneDocument(t *testing.T) {
	docs, filled := extractNamed(t, "node-a", hostDocFill)
	if !strings.Contains(docs, "HostnameConfig") || filled != (Filled{}) {
		t.Errorf("filled %+v, documents:\n%s", filled, docs)
	}
}

// A declaration of an embedded document after the removed one moves with its document: the stored stream
// has one document fewer, so the stored declaration names it one position lower, and the import is not
// refused for a declaration that names no document. The mark keeps the position of the input.
func TestFilledHostnameShiftsLaterEmbeddedDeclarations(t *testing.T) {
	text := hostDocFill + "---\n" + manifestStream("secret: 314159\n")
	input := "doc[1]/cluster/inlineManifests/0/contents"
	req := request(t, text, input+"|yaml/secret")
	req.Name = "node-a"
	req.Declarations = Declarations{Embedded: []Embedded{{Path: input, Format: "yaml"}}}
	c, err := Extract(req)
	if err != nil {
		t.Fatalf("an embedded declaration after the removed document: %v", err)
	}
	want := []Embedded{{Path: manifestPath, Format: "yaml"}}
	if got := c.decl.Embedded; len(got) != 1 || got[0] != want[0] {
		t.Errorf("stored declarations %+v, want %+v", got, want)
	}
	if s, _ := commitAll(t, c); strings.Contains(string(s.Documents()), "HostnameConfig") {
		t.Error("the filled document stays")
	}
}

// shiftEmbedded drops a declaration inside the removed document and leaves one before it as written.
func TestShiftEmbedded(t *testing.T) {
	list := []Embedded{
		{Path: "doc[0]/a", Format: "yaml"}, {Path: "doc[1]/b", Format: "json"}, {Path: "doc[3]/c", Format: "yaml"},
	}
	got, set := shiftEmbedded(list, 1)
	want := []Embedded{{Path: "doc[0]/a", Format: "yaml"}, {Path: "doc[2]/c", Format: "yaml"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("shifted %+v, want %+v", got, want)
	}
	if len(set) != 2 || set["doc[0]/a"] != "yaml" || set["doc[2]/c"] != "yaml" {
		t.Errorf("set %v", set)
	}
}
