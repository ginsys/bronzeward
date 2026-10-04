package id

import (
	"regexp"
	"testing"
)

var shape = regexp.MustCompile(`^[a-z]+_[a-z2-7]{26}$`)

func TestAllIsACopy(t *testing.T) {
	a := All()
	if len(a) != 24 {
		t.Fatalf("All() has %d prefixes, want 24 (persistence-api.md §2)", len(a))
	}
	a[0] = "zzz"
	if _, err := Parse(New(Cluster)); err != nil {
		t.Fatalf("mutating All()'s result changed validation: %v", err)
	}
	if All()[0] != Cluster {
		t.Fatalf("mutating All()'s result changed the table: %q", All()[0])
	}
}

func TestNewShape(t *testing.T) {
	for _, p := range All() {
		s := New(p)
		if !shape.MatchString(s) {
			t.Fatalf("%s: %q does not match %s", p, s, shape)
		}
		got, err := Parse(s)
		if err != nil || got != p {
			t.Fatalf("Parse(%q) = %q, %v; want %q", s, got, err, p)
		}
	}
}

func TestNewIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		s := New(Release)
		if seen[s] {
			t.Fatalf("duplicate %q", s)
		}
		seen[s] = true
	}
}

func TestParseRefuses(t *testing.T) {
	for _, s := range []string{
		"",
		"rel",
		"rel_",
		"rel_fgqvcvz3ck7h7234ljgdbzsj6",   // 25 chars
		"rel_fgqvcvz3ck7h7234ljgdbzsj6mm", // 27 chars
		"rel_FGQVCVZ3CK7H7234LJGDBZSJ6M",  // upper case
		"rel_fgqvcvz3ck7h7234ljgdbzsj6=",  // padding
		"rel_fgqvcvz3ck7h7234ljgdbzsj61",  // '1' is not base32
		"zzz_fgqvcvz3ck7h7234ljgdbzsj6m",  // unknown prefix
		"rel_fgqvcvz3ck7h7234ljgdbzsj6n",  // non-zero trailing bits ('n' = 01101)
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted", s)
		}
	}
}

func TestSpecExampleParses(t *testing.T) {
	if p, err := Parse("rel_fgqvcvz3ck7h7234ljgdbzsj6m"); err != nil || p != Release {
		t.Fatalf("PA §2 example: %q, %v", p, err)
	}
}

func TestNewRefusesUnknownPrefix(t *testing.T) {
	for _, p := range []Prefix{"", "zzz", "REL"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%q) did not panic", p)
				}
			}()
			New(p)
		}()
	}
}

func TestMustHave(t *testing.T) {
	if err := MustHave(New(Plan), Plan); err != nil {
		t.Fatal(err)
	}
	if err := MustHave(New(Plan), Approval); err == nil {
		t.Fatal("wrong prefix accepted")
	}
}
