package compile

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// A draft update that types the CA key is ingested like any other (compilation.md §2.3 step 3a: only
// an import sets the key apart), so the key becomes an ordinary secret reference whether or not the
// operator marks it, and publication refuses that fragment as an override of the import base's typed
// reference (§6 step 7, choice §16.20). Every key here is generated for the test.

// draftCAKey is a fragment as a draft update stores it: the typed key goes through the same
// ingestion (no Import), so machine.ca.key holds a minted reference and the text holds no key.
func draftCAKey(t *testing.T, encoded string, marks ...string) Source {
	t.Helper()
	s := source(t, "machine:\n  ca:\n    key: "+strconv.Quote(encoded)+"\n", ingest.Declarations{}, nil, marks...)
	text := string(s.Text.Documents())
	if strings.Contains(text, encoded) {
		t.Fatal("control: the stored fragment holds the key")
	}
	if strings.Count(text, "!bwref s-") != 1 || strings.Contains(text, ingest.CAKeyName) {
		t.Fatalf("control: the stored fragment holds %d minted references, want 1, and no typed CA key reference", strings.Count(text, "!bwref s-"))
	}
	return s
}

func TestCompileRefusesADraftUpdatedCAKey(t *testing.T) {
	typed, _, pem, encoded := caBases(t)
	refused := func(t *testing.T, fragment Source) {
		t.Helper()
		_, err := Compile(Input{Base: typed, Fragments: []Source{fragment}, Mode: ModeMetal})
		var e *Error
		if !errors.As(err, &e) || e.Rule != RuleBaseOverride || !slices.Equal(e.Paths, []string{"doc[0]/machine/ca/key"}) {
			t.Fatalf("got %v, want %s at machine.ca.key", err, RuleBaseOverride)
		}
		noKey(t, "the refusal", err.Error(), pem, encoded)
	}
	t.Run("unmarked", func(t *testing.T) { refused(t, draftCAKey(t, encoded)) })
	t.Run("marked", func(t *testing.T) { refused(t, draftCAKey(t, encoded, "doc[0]/machine/ca/key")) })
	// Control: the key written literally in a fragment is refused the same way.
	t.Run("literal", func(t *testing.T) {
		refused(t, stored(t, "machine:\n  ca:\n    key: "+encoded+"\n"))
	})
	// Control: a draft update that leaves the key out publishes.
	t.Run("without the key", func(t *testing.T) {
		other := source(t, "machine:\n  nodeLabels:\n    team: draft\n", ingest.Declarations{}, nil)
		compiled(t, Input{Base: typed, Fragments: []Source{other}, Mode: ModeMetal})
	})
}
