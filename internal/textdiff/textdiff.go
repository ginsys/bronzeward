// Package textdiff writes the unified line diff a plan's evidence shows (execution-recovery.md §2:
// a diff of whole configurations). It uses Myers' algorithm, which finds a shortest edit script;
// past maxEdits changes it gives up searching and shows the whole of one text replaced by the other,
// which is still exact, only less informative.
package textdiff

import (
	"fmt"
	"strings"
)

const (
	context = 3
	// maxEdits bounds the search: its memory grows with the square of the changes found.
	maxEdits = 2000
)

type edit struct {
	op   byte // ' ' kept, '-' removed from the first text, '+' added from the second
	line string
}

// Unified returns the hunks of a unified diff from a to b, without file headers, or "" when the
// texts are equal. A last line without a newline is marked as diff(1) marks it.
func Unified(a, b string) string {
	es := edits(split(a), split(b), maxEdits)
	// at[k] is the number of lines of a, and of b, before edit k.
	at := make([][2]int, len(es)+1)
	var changed []int
	for k, e := range es {
		at[k+1] = at[k]
		if e.op != '+' {
			at[k+1][0]++
		}
		if e.op != '-' {
			at[k+1][1]++
		}
		if e.op != ' ' {
			changed = append(changed, k)
		}
	}
	var out strings.Builder
	for i := 0; i < len(changed); {
		j := i
		for j+1 < len(changed) && changed[j+1]-changed[j]-1 <= 2*context {
			j++
		}
		s, e := max(changed[i]-context, 0), min(changed[j]+context+1, len(es))
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(at[s][0], at[e][0]-at[s][0]), span(at[s][1], at[e][1]-at[s][1]))
		for _, ed := range es[s:e] {
			out.WriteByte(ed.op)
			out.WriteString(ed.line)
			if !strings.HasSuffix(ed.line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = j + 1
	}
	return out.String()
}

// span is a hunk range: its first line, or the line before it when it is empty, and its count
// when that is not one.
func span(before, n int) string {
	switch n {
	case 0:
		return fmt.Sprintf("%d,0", before)
	case 1:
		return fmt.Sprintf("%d", before+1)
	}
	return fmt.Sprintf("%d,%d", before+1, n)
}

// split cuts s into lines, each keeping its newline; the last may have none.
func split(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// edits returns a shortest edit script from a to b, or, when that needs more than bound changes,
// all of a removed and all of b added.
func edits(a, b []string, bound int) []edit {
	n, m := len(a), len(b)
	off := n + m + 1
	v := make([]int, 2*off+1) // v[off+k] is the furthest x reached on diagonal k = x - y
	var trace [][]int         // trace[d] holds v for k in [-d, d] as the search for d began
	for d := 0; d <= min(n+m, bound); d++ {
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b)
			}
		}
	}
	es := make([]edit, 0, n+m)
	for _, l := range a {
		es = append(es, edit{'-', l})
	}
	for _, l := range b {
		es = append(es, edit{'+', l})
	}
	return es
}

func backtrack(trace [][]int, a, b []string) []edit {
	x, y := len(a), len(b)
	var rev []edit
	for d := len(trace) - 1; d > 0; d-- {
		prev := trace[d]
		at := func(k int) int { return prev[k+d] }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			rev = append(rev, edit{' ', a[x-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, edit{'+', b[y-1]})
		} else {
			rev = append(rev, edit{'-', a[x-1]})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, edit{' ', a[x-1]})
		x, y = x-1, y-1
	}
	es := make([]edit, len(rev))
	for i, e := range rev {
		es[len(rev)-1-i] = e
	}
	return es
}
