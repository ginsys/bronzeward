package discover

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestEndpointsListsTheContextsEndpointsInOrder: the chosen context's endpoints, as the file lists them and in
// its order; the current context when no name is given, the named one otherwise.
func TestEndpointsListsTheContextsEndpointsInOrder(t *testing.T) {
	c := newCluster(t)
	// A bracketed address is quoted, as Talos writes it, because a bare one is a list inside a list in YAML.
	tc := twoContexts(c.p, c.p, `10.0.0.1, cp.example.test:50000, "[2001:db8::1]:50000"`, "10.1.0.1")
	for name, want := range map[string][]string{
		"":  {"10.0.0.1", "cp.example.test:50000", "[2001:db8::1]:50000"},
		"a": {"10.0.0.1", "cp.example.test:50000", "[2001:db8::1]:50000"},
		"b": {"10.1.0.1"},
	} {
		got, err := Endpoints(tc, name)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("context %q: %q, %v, want %q", name, got, err, want)
		}
	}
}

// TestEndpointsOfAContextThatListsNone: no endpoint is an empty list and no error, so that the caller says
// which flag to give; it is not this function's refusal.
func TestEndpointsOfAContextThatListsNone(t *testing.T) {
	c := newCluster(t)
	got, err := Endpoints(twoContexts(c.p, c.p, "", "10.1.0.1"), "")
	if err != nil || len(got) != 0 {
		t.Errorf("a context with no endpoint: %q, %v, want none and no error", got, err)
	}
}

// TestEndpointsRefusals: a name the file does not hold is ErrUnknownContext, and what is no talosconfig is
// an error that quotes none of it.
func TestEndpointsRefusals(t *testing.T) {
	c := newCluster(t)
	tc := twoContexts(c.p, c.p, "10.0.0.1", "10.1.0.1")
	if _, err := Endpoints(tc, "c"); !errors.Is(err, ErrUnknownContext) {
		t.Errorf("an unknown context: %v, want ErrUnknownContext", err)
	}
	const canary = "BWSYNTH-endpoints-canary"
	_, err := Endpoints([]byte("hello: "+canary), "")
	if err == nil {
		t.Fatal("a document that is no talosconfig gave no error")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the error quotes the document: %q", err.Error())
	}
}
