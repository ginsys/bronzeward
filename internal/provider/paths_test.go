package provider

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// generationCheck is the generation column's CHECK regex, read from the migration rather than
// copied, so a path this package builds and the column it is stored in cannot drift apart.
func generationCheck(t *testing.T) *regexp.Regexp {
	t.Helper()
	b, err := os.ReadFile("../migrate/migrations/0004_adoption.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`generation ~ '([^']+)'`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no generation CHECK in 0004_adoption.sql")
	}
	return regexp.MustCompile(m[1])
}

func TestGenerationPathMatchesMigration(t *testing.T) {
	re := generationCheck(t)
	cluster, claim := id.New(id.Cluster), id.New(id.Ingestion)
	seen := map[string]bool{}
	for range 1000 {
		v := NewValueID()
		if len(v) != 26 || strings.Trim(v, "abcdefghijklmnopqrstuvwxyz234567") != "" {
			t.Fatalf("NewValueID() = %q, want 26 characters of [a-z2-7]", v)
		}
		if seen[v] {
			t.Fatalf("NewValueID repeated %q", v)
		}
		seen[v] = true
		p, err := NewGenerationPath(cluster, claim, v)
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(p.String()) {
			t.Fatalf("%q does not match the migration's CHECK %s", p, re)
		}
	}
	// Control: the regex read from the migration refuses a value id holding '/', so a match above
	// is evidence, not a regex that accepts everything.
	if bad := "gen/" + cluster + "/" + claim + "/a/b"; re.MatchString(bad) {
		t.Fatalf("the CHECK accepted %q; the test proves nothing", bad)
	}
}

func TestGenerationPathRefuses(t *testing.T) {
	cluster, claim := id.New(id.Cluster), id.New(id.Ingestion)
	if _, err := NewGenerationPath(cluster, claim, "ok_Value-1"); err != nil {
		t.Fatalf("a valid path was refused: %v", err)
	}
	for _, c := range []struct{ cluster, claim, value string }{
		{"", claim, "v"},
		{id.New(id.Machine), claim, "v"},
		{"cl_x", claim, "v"},
		{cluster, "", "v"},
		{cluster, id.New(id.Cluster), "v"},
		{cluster, claim, ""},
		{cluster, claim, strings.Repeat("a", 129)},
		{cluster, claim, "a/b"},
		{cluster, claim, "."},
		{cluster, claim, ".."},
		{cluster, claim, "a%2F"},
		{cluster, claim, "a#b"},
		{cluster, claim, "a?b"},
		{cluster, claim, "a b"},
		{cluster, claim, "a\tb"},
		{cluster, claim, "a\nb"},
	} {
		if p, err := NewGenerationPath(c.cluster, c.claim, c.value); err == nil {
			t.Errorf("NewGenerationPath(%q, %q, %q) = %q, want a refusal", c.cluster, c.claim, c.value, p)
		}
	}
	if _, err := NewGenerationPath(cluster, claim, strings.Repeat("a", 128)); err != nil {
		t.Errorf("a 128-character value id was refused: %v", err)
	}
}

// E1's transit-path cases, plus the zero value: a key name is one URL path segment.
func TestTransitPathRefusesSlash(t *testing.T) {
	if got, err := transitPath("encrypt", "bw-baseline"); err != nil || got != "/v1/transit/encrypt/bw-baseline" {
		t.Fatalf("transitPath(bw-baseline) = %q, %v", got, err)
	}
	for _, name := range []string{"", "k#x", "k?x", "k%2F", "k x", "a/b", "../sys", ".", "..", "k\\x", "k\x00"} {
		if got, err := transitPath("encrypt", name); err == nil {
			t.Errorf("transitPath accepted %q as %q", name, got)
		}
	}
}

// A digest's key reference is stored in import_base_revision.baseline_digest_key, whose bound is
// read from the migration rather than copied. A key name that fits makes a reference that fits at
// the largest version Digest accepts; one byte more is refused before any provider call, so no
// generation is created for an ingestion whose record the column would then refuse.
func TestKeyNameFitsTheKeyReference(t *testing.T) {
	b, err := os.ReadFile("../migrate/migrations/0004_adoption.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`octet_length\(baseline_digest_key\) <= (\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no baseline_digest_key bound in 0004_adoption.sql")
	}
	limit, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	fits := strings.Repeat("k", maxKeyName)
	if err := CheckKeyName(fits); err != nil {
		t.Fatalf("a %d-byte key name was refused: %v", len(fits), err)
	}
	if ref := (Digest{Key: fits, Version: math.MaxInt}).KeyRef(); len(ref) != limit {
		t.Fatalf("the longest key name at the largest version makes a %d-byte reference; the column holds %d", len(ref), limit)
	}
	keys := testKeys
	keys.Digest = fits
	if _, err := NewIngestion("http://127.0.0.1:8200", tokenOf(testToken), keys); err != nil {
		t.Fatalf("NewIngestion refused a %d-byte digest key: %v", len(fits), err)
	}
	// One byte over, in ASCII and with a two-byte character: the column counts bytes, not runes.
	for _, over := range []string{fits + "k", strings.Repeat("k", maxKeyName-1) + "é"} {
		if err := CheckKeyName(over); err == nil {
			t.Errorf("a %d-byte key name was accepted", len(over))
		}
		keys.Digest = over
		if _, err := NewIngestion("http://127.0.0.1:8200", tokenOf(testToken), keys); err == nil {
			t.Errorf("NewIngestion accepted a %d-byte digest key", len(over))
		}
	}
}
