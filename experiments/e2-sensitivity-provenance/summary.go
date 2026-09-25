package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mustFire are the controls that show a negative claim's check can fire: each must have fired
// wherever it ran.
var mustFire = []string{"fidelity-check-fires", "template-check-fires-", "per-side-diff-exposes-base-value"}

func isMustFire(name string) bool {
	for _, p := range mustFire {
		if name == p || (strings.HasSuffix(p, "-") && strings.HasPrefix(name, p)) {
			return true
		}
	}
	return false
}

// summarise merges every listed cell's and pair's analysis into <results>/ and decides whether
// the run is complete: every listed unit analysed, every fidelity expectation met, no control
// failed or inconclusive, every must-fire control fired, and every pair's named controls fired.
// Any other expectation that is not met is a result, counted as unexpected. The list's
// lines are `cell|pair <tab> base <tab> name <tab> analysis-dir [<tab> controls a pair requires]`.
func summarise(listPath, results string) (bool, error) {
	units, err := readTSV(listPath)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(results, 0o755); err != nil {
		return false, err
	}
	out := map[string]*strings.Builder{}
	add := func(name, header string) *strings.Builder {
		if out[name] == nil {
			out[name] = &strings.Builder{}
			out[name].WriteString(header)
		}
		return out[name]
	}
	var problems, unexpected []string
	cells, pairs := 0, 0
	type count struct{ ref, base map[string]bool }
	perRep := map[string]*count{}
	for _, r := range reps {
		perRep[r.name] = &count{map[string]bool{}, map[string]bool{}}
	}
	controlCounts := map[string]map[string]int{}
	expected, mismatched := 0, 0
	for _, u := range units {
		if len(u) < 4 {
			return false, fmt.Errorf("%s: malformed line %v", listPath, u)
		}
		kind, base, name, dir := u[0], u[1], u[2], u[3]
		if kind == "cell" {
			cells++
		} else {
			pairs++
		}
		unit := kind + " " + base + "/" + name
		read := func(f string) ([][]string, bool) {
			rows, err := readTSV(filepath.Join(dir, f))
			if err != nil {
				return nil, false
			}
			return rows, true
		}
		exp, ok1 := read("expectations.tsv")
		ctl, ok2 := read("controls.tsv")
		lk, ok3 := read("leaks.tsv")
		if !ok1 || !ok2 || !ok3 {
			problems = append(problems, unit+": not analysed")
			continue
		}
		b := add("expectations.tsv", "base\tcase\titem\texpected\tobserved\tmatch\n")
		for _, r := range exp {
			b.WriteString(strings.Join(r, "\t") + "\n")
			expected++
			switch {
			case len(r) < 6:
				problems = append(problems, unit+": malformed expectation")
			case r[5] == "yes":
			case r[2] == "fidelity":
				problems = append(problems, unit+": the tracer pass did not reproduce the composition")
			default:
				mismatched++
				unexpected = append(unexpected, fmt.Sprintf("%s/%s %s expected %s observed %s", base, name, r[2], r[3], r[4]))
			}
		}
		fired := map[string]bool{}
		b = add("controls.tsv", "base\tcase\tcontrol\tresult\tdetail\n")
		for _, r := range ctl {
			b.WriteString(strings.Join(r, "\t") + "\n")
			if len(r) < 4 {
				problems = append(problems, unit+": malformed control")
				continue
			}
			c, res := r[2], r[3]
			if controlCounts[c] == nil {
				controlCounts[c] = map[string]int{}
			}
			controlCounts[c][res]++
			if res == "fired" {
				fired[c] = true
			}
			if res == "fail" || res == "inconclusive" || (isMustFire(c) && res == "did-not-fire") {
				problems = append(problems, unit+": control "+c+" "+res)
			}
		}
		if kind == "pair" && len(u) > 4 && u[4] != "-" {
			for _, c := range strings.Split(u[4], ",") {
				if !fired[c] {
					problems = append(problems, unit+": required control "+c+" did not fire")
				}
			}
		}
		b = add(kind+"-leaks.tsv", "base\tcase\trep\tartifact\tsecret\tclass\tform\tcount\n")
		if kind == "cell" {
			b = add("leaks.tsv", "base\tcase\trep\tartifact\tsecret\tclass\tform\tcount\n")
		}
		for _, r := range lk {
			b.WriteString(strings.Join(r, "\t") + "\n")
			if kind == "cell" && len(r) >= 6 && perRep[r[2]] != nil {
				if r[5] == "ref" {
					perRep[r[2]].ref[base+"/"+name] = true
				} else {
					perRep[r[2]].base[base+"/"+name] = true
				}
			}
		}
		if kind != "cell" {
			continue
		}
		if rows, ok := read("cell.tsv"); ok && len(rows) == 2 {
			add("cells.tsv", strings.Join(rows[0], "\t")+"\n").WriteString(strings.Join(rows[1], "\t") + "\n")
		} else {
			problems = append(problems, unit+": no cell measurements")
		}
		for _, f := range []string{"provenance.tsv", "dependencies.tsv"} {
			rows, ok := read(f)
			if !ok || len(rows) == 0 {
				problems = append(problems, unit+": no "+f)
				continue
			}
			b := add(f, "base\tcase\t"+strings.Join(rows[0], "\t")+"\n")
			for _, r := range rows[1:] {
				b.WriteString(base + "\t" + name + "\t" + strings.Join(r, "\t") + "\n")
			}
		}
	}
	complete := len(problems) == 0
	var s strings.Builder
	fmt.Fprintf(&s, "cells\t%d\npairs\t%d\nexpectations\t%d\nunexpected\t%d\n", cells, pairs, expected, mismatched)
	for _, u := range unexpected {
		s.WriteString("  " + u + "\n")
	}
	s.WriteString("\nrep\tcells-leaking-a-reference-value\tcells-leaking-a-base-secret\n")
	for _, r := range reps {
		fmt.Fprintf(&s, "%s\t%d\t%d\n", r.name, len(perRep[r.name].ref), len(perRep[r.name].base))
	}
	s.WriteString("\ncontrol\tresults\n")
	var names []string
	for c := range controlCounts {
		names = append(names, c)
	}
	sort.Strings(names)
	for _, c := range names {
		var rs []string
		for res, n := range controlCounts[c] {
			rs = append(rs, fmt.Sprintf("%s=%d", res, n))
		}
		sort.Strings(rs)
		fmt.Fprintf(&s, "%s\t%s\n", c, strings.Join(rs, " "))
	}
	if len(problems) > 0 {
		s.WriteString("\nproblems\n")
		for _, p := range problems {
			s.WriteString(p + "\n")
		}
	}
	verdict := "complete"
	if !complete {
		verdict = "incomplete"
	}
	fmt.Fprintf(&s, "\nverdict\t%s\n", verdict)
	out["summary.txt"] = &s
	for name, b := range out {
		if err := os.WriteFile(filepath.Join(results, name), []byte(b.String()), 0o644); err != nil {
			return false, err
		}
	}
	return complete, nil
}
