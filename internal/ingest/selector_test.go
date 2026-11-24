package ingest

import (
	"strings"
	"testing"
)

// Compilation §2.3 step 3: a document whose root is a delete selector (apiVersion, kind and `$patch: delete`,
// each a plain string, nothing else) is accepted: the machinery reads every member to choose the document it
// deletes, so none is stored unloaded. Every other directive mapping that holds anything beside the
// directive stays refused.

const selectorDelete = "apiVersion: v1alpha1\nkind: HostnameConfig\n$patch: delete\n"

func TestDocumentSelectorAccepted(t *testing.T) {
	for _, tc := range []struct{ label, text string }{
		{"alone", selectorDelete},
		{"after a document", "machine:\n  type: worker\n---\n" + selectorDelete},
		{"before a document", selectorDelete + "---\nmachine:\n  type: worker\n"},
		{"members in another order", "$patch: delete\nkind: HostnameConfig\napiVersion: v1alpha1\n"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			c, err := Extract(request(t, tc.text))
			if err != nil {
				t.Fatalf("the selector is refused: %v", err)
			}
			s, _ := commitAll(t, c)
			if !strings.Contains(string(s.Documents()), "$patch: delete") || !strings.Contains(string(s.Documents()), "kind: HostnameConfig") {
				t.Errorf("the selector is not stored:\n%s", s.Documents())
			}
		})
	}
}

func TestDocumentSelectorRefused(t *testing.T) {
	for _, tc := range []struct{ label, text string }{
		{"another member", selectorDelete + "hostname: node-a\n"},
		{"a name", selectorDelete + "name: eth0\n"},
		{"a secret beside the directive", selectorDelete + "token: " + secretText + "\n"},
		{"no kind", "apiVersion: v1alpha1\n$patch: delete\n"},
		{"no apiVersion", "kind: HostnameConfig\n$patch: delete\n"},
		{"an unknown kind", "apiVersion: v1alpha1\nkind: NoSuchKind\n$patch: delete\n"},
		{"another apiVersion", "apiVersion: v9\nkind: HostnameConfig\n$patch: delete\n"},
		{"a known kind that is not the hostname's", "apiVersion: v1alpha1\nkind: ResolverConfig\n$patch: delete\n"},
		{"the kind a mapping", "apiVersion: v1alpha1\nkind:\n  x: y\n$patch: delete\n"},
		{"the apiVersion a number", "apiVersion: 1\nkind: HostnameConfig\n$patch: delete\n"},
		{"the kind null", "apiVersion: v1alpha1\nkind:\n$patch: delete\n"},
		{"an anchor on the kind", "apiVersion: v1alpha1\nkind: &k HostnameConfig\n$patch: delete\n"},
		{"another directive", "apiVersion: v1alpha1\nkind: HostnameConfig\n$patch: replace\n"},
		{"a nested selector", "machine:\n  network:\n    sel:\n      apiVersion: v1alpha1\n      kind: HostnameConfig\n      $patch: delete\n"},
		{"a list entry", "machine:\n  network:\n    interfaces:\n      - apiVersion: v1alpha1\n        kind: HostnameConfig\n        $patch: delete\n"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := Extract(request(t, tc.text))
			if err == nil {
				t.Fatal("the input is accepted")
			}
			if strings.Contains(err.Error(), secretText) {
				t.Errorf("the refusal quotes the input: %v", err)
			}
		})
	}
}

// A refusal of a selector that holds another member names the rule the directive rules always named.
func TestDocumentSelectorWithAMemberIsUnloadable(t *testing.T) {
	_, err := Extract(request(t, selectorDelete+"hostname: node-a\n"))
	wantRule(t, err, RuleSchemaUnloadable)
}
