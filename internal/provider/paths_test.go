package provider

import (
	"os"
	"regexp"
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
