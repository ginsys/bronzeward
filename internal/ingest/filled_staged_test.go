package ingest

import (
	"strings"
	"testing"
)

// The envelope carries step 2a's record as two optional members, so a continuation and a takeover hold
// the name that was compared (compilation.md §2.3 step 2a). The envelope version stays 1: an envelope
// staged before the record decodes as "nothing removed", which is what its run did.
func TestStagedFilledRoundTrip(t *testing.T) {
	for _, f := range []Filled{{}, {Name: "node-a"}, {Name: "node-a", Equal: true}} {
		st, _, _ := sealedImport(t)
		st.Filled = f
		plain, sum, err := st.Seal()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(plain), `"version":1`) {
			t.Errorf("the version moved: %s", plain)
		}
		if (f == Filled{}) && strings.Contains(string(plain), "filled") {
			t.Errorf("an empty record is written: %s", plain)
		}
		back, err := Open(plain, sum)
		if err != nil {
			t.Fatal(err)
		}
		if back.Filled != f {
			t.Errorf("record %+v came back as %+v", f, back.Filled)
		}
	}
}

// An envelope sealed before the record has neither member and still opens.
func TestStagedOldEnvelopeOpensAsNotFilled(t *testing.T) {
	st, _, _ := sealedImport(t)
	plain, sum, err := st.Seal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(plain, sum)
	if err != nil || back.Filled != (Filled{}) {
		t.Errorf("Open() = %+v, %v", back.Filled, err)
	}
}

// A record that says the removed document equalled the fill, with no name, is no record.
func TestStagedFilledEqualNeedsAName(t *testing.T) {
	st, _, _ := sealedImport(t)
	st.Filled = Filled{Equal: true}
	plain, sum, err := st.Seal()
	if err == nil {
		if _, err = Open(plain, sum); err == nil {
			t.Fatal("an equal record with no name was sealed and opened")
		}
	}
}

// A remark carries the record unchanged.
func TestRemarkedCarriesFilled(t *testing.T) {
	st, _, _ := sealedImport(t)
	st.Filled = Filled{Name: "node-a", Equal: true}
	if got := st.Remarked(st.Sanitized, nil).Filled; got != st.Filled {
		t.Errorf("Remarked() record %+v, want %+v", got, st.Filled)
	}
}
