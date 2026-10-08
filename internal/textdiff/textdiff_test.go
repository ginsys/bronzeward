package textdiff

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	for name, c := range map[string]struct{ a, b, want string }{
		"equal":           {"a\nb\n", "a\nb\n", ""},
		"both empty":      {"", "", ""},
		"from empty":      {"", "a\nb\n", "@@ -0,0 +1,2 @@\n+a\n+b\n"},
		"to empty":        {"a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b\n"},
		"one changed":     {"1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n", "@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8\n"},
		"insert at start": {"a\nb\n", "z\na\nb\n", "@@ -1,2 +1,3 @@\n+z\n a\n b\n"},
		"delete at end":   {"a\nb\nc\n", "a\nb\n", "@@ -1,3 +1,2 @@\n a\n b\n-c\n"},
		"two hunks": {"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "X\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\nY\n",
			"@@ -1,4 +1,4 @@\n-1\n+X\n 2\n 3\n 4\n@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+Y\n"},
		"near changes merge": {"1\n2\n3\n4\n5\n6\n7\n8\n", "X\n2\n3\n4\n5\n6\n7\nY\n",
			"@@ -1,8 +1,8 @@\n-1\n+X\n 2\n 3\n 4\n 5\n 6\n 7\n-8\n+Y\n"},
		"newline at end added":   {"a\nb", "a\nb\n", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"},
		"newline at end removed": {"a\n", "a", "@@ -1 +1 @@\n-a\n+a\n\\ No newline at end of file\n"},
	} {
		if got := Unified(c.a, c.b); got != c.want {
			t.Errorf("%s:\ngot\n%s\nwant\n%s", name, got, c.want)
		}
	}
}

// Every edit script reproduces b from a, and Myers' is a shortest one: no longer than the edit
// distance a quadratic longest-common-subsequence table gives.
func TestEditsShortestAndFaithful(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 2000 {
		a, b := randomLines(r), randomLines(r)
		es := edits(a, b, len(a)+len(b))
		if from, to := sides(es); !equal(from, a) || !equal(to, b) {
			t.Fatalf("case %d: %q -> %q read %q, wrote %q", i, a, b, from, to)
		}
		if n, want := changes(es), len(a)+len(b)-2*lcs(a, b); n != want {
			t.Fatalf("case %d: %q -> %q: %d changes, shortest %d", i, a, b, n, want)
		}
	}
}

// Past the edit bound the script is the whole of a removed and the whole of b added: still
// faithful, never a partial guess.
func TestEditBound(t *testing.T) {
	a, b := []string{"a\n", "b\n", "c\n"}, []string{"x\n", "b\n", "y\n"}
	es := edits(a, b, 1)
	if from, to := sides(es); !equal(from, a) || !equal(to, b) {
		t.Fatalf("read %q, wrote %q", from, to)
	}
	if changes(es) != len(a)+len(b) {
		t.Fatalf("fallback kept lines: %+v", es)
	}
	if es := edits(a, b, 4); changes(es) != 4 {
		t.Fatalf("within the bound: %+v", es)
	}
}

func randomLines(r *rand.Rand) []string {
	out := make([]string, r.IntN(12))
	for i := range out {
		out[i] = string(rune('a'+r.IntN(4))) + "\n"
	}
	return out
}

// sides returns the texts an edit script reads (kept and removed lines) and writes (kept and added).
func sides(es []edit) (from, to []string) {
	for _, e := range es {
		if e.op != '+' {
			from = append(from, e.line)
		}
		if e.op != '-' {
			to = append(to, e.line)
		}
	}
	return from, to
}

func changes(es []edit) int {
	n := 0
	for _, e := range es {
		if e.op != ' ' {
			n++
		}
	}
	return n
}

func equal(a, b []string) bool { return strings.Join(a, "") == strings.Join(b, "") && len(a) == len(b) }

func lcs(a, b []string) int {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	return t[0][0]
}
