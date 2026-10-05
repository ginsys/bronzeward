package ingest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// Compilation §6 step 1 reads each source revision as it was stored: the sanitized text and the
// declarations its reference rows and embedded identifications hold. Stored rebuilds the
// sanitized value with the authoring checks a resume runs (§3.1), so a row that no longer holds
// a well-formed source never reaches the compiler.
func TestStored(t *testing.T) {
	decl := Declarations{
		References: map[string]Reference{
			"s-a": {Kind: provider.KindString, Version: 2},
			"s-b": {Kind: provider.KindString, Version: 1, Encoding: "base64"},
		},
		Embedded: []Embedded{{Path: "doc[0]/machine/files/0/content", Format: "yaml"}},
	}
	doc := "machine:\n  token: !bwref s-a\n  files:\n    - content: |\n        key: !bwref s-b\n"
	s, err := Stored(doc, decl)
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Documents()) != doc || !reflect.DeepEqual(s.Declarations(), decl) {
		t.Fatalf("stored %q %+v, got %q %+v", doc, decl, s.Documents(), s.Declarations())
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	// A source with no reference stores no declarations; only its text decides.
	if _, err := Stored("machine:\n  type: worker\n", Declarations{}); err != nil {
		t.Fatalf("a source with no reference: %v", err)
	}

	for name, c := range map[string]struct {
		doc  string
		decl Declarations
	}{
		"no text":                  {"", decl},
		"text that does not parse": {"machine: [" + secretText, decl},
		"text that does not parse, no declarations": {"machine: [" + secretText, Declarations{}},
		"an undeclared reference":                   {doc + "---\nmachine:\n  extra: !bwref s-z\n", decl},
		"an unused declaration":                     {"machine:\n  token: !bwref s-a\n  files:\n    - content: |\n        key: " + secretText + "\n", decl},
		"reserved text in a string":                 {doc + "---\nmachine:\n  note: \"!bwref s-a " + secretText + "\"\n", decl},
		"a local tag":                               {doc + "---\nmachine:\n  note: !secret " + secretText + "\n", decl},
		"a declaration of no kind": {doc, Declarations{References: map[string]Reference{
			"s-a": {Version: 2}, "s-b": {Kind: provider.KindString, Version: 1, Encoding: "base64"},
		}, Embedded: decl.Embedded}},
		"an embedded document that does not parse": {"machine:\n  token: !bwref s-a\n  files:\n    - content: \"key: [" + secretText + "\"\n",
			Declarations{References: map[string]Reference{"s-a": decl.References["s-a"]}, Embedded: decl.Embedded}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Stored(c.doc, c.decl)
			if err == nil {
				t.Fatal("rebuilt")
			}
			if strings.Contains(fmt.Sprintf("%v %+v", err, err), secretText) {
				t.Errorf("the refusal quotes the stored text: %v", err)
			}
		})
	}
}
