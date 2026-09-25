package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// analysePair diffs two cells' compositions, as a change from one configuration revision (a) to
// the next (b) would show, under every representation. It also runs the stale-provenance controls:
// b redacted with a's composed paths (a moved value escapes them), b redacted with a's values (a
// rotated value escapes them), and whether a's paths name b's version. It writes <out>/ and
// <artifacts>/<rep>/<pair>/<base>/diff.txt.
func analysePair(dirA, dirB, baseSecretsPath, artifacts, out, pairName, baseName string) error {
	ca, err := loadCell(dirA)
	if err != nil {
		return err
	}
	cb, err := loadCell(dirB)
	if err != nil {
		return err
	}
	baseSecrets, err := readBaseSecrets(baseSecretsPath)
	if err != nil {
		return err
	}
	baseText, _ := ca.read("base.yaml")
	baseText += "\n" + ca.canonical() + cb.canonical() // the oracle's public text, as in analyse
	ea, eb := ca.caseDef.Embedded, cb.caseDef.Embedded
	recA, err := ca.records("real/tag")
	if err != nil {
		return err
	}
	recB, err := cb.records("real/tag")
	if err != nil {
		return err
	}
	oa, err := ca.output(ea, recA)
	if err != nil {
		return err
	}
	ob, err := cb.output(eb, recB)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var all, refB []Secret
	for _, s := range append(append(append([]Secret(nil), ca.secrets...), cb.secrets...), baseSecrets...) {
		if k := s.ID + "\x00" + s.Value; !seen[k] {
			seen[k] = true
			all = append(all, s)
		}
	}
	refB = append(refB, cb.secrets...)
	matcher := func(secrets ...[]Secret) *valueMatcher {
		var v []string
		for _, ss := range secrets {
			for _, s := range ss {
				if s.Kind == "str" || s.Kind == "bytes" {
					v = append(v, s.Value)
				}
			}
		}
		return newValueMatcher(v)
	}
	vm := matcher(all)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	var controls, expectations, leaks table
	control := func(name, result, detail string) { controls.row(baseName, pairName, name, result, detail) }
	expect := func(item, want, got string) {
		m := "yes"
		if want != got {
			m = "no"
		}
		expectations.row(baseName, pairName, item, want, got, m)
	}
	composedBoth := oa.view != nil && ob.view != nil
	faithful := len(oa.fidelity) == 0 && len(ob.fidelity) == 0
	observed := map[string]string{}
	for _, r := range reps {
		var text string
		switch {
		case !composedBoth:
			text = "(no diff: a revision did not compose)\n"
		case r.composed && !faithful:
			text = "<withheld: the tracer pass did not reproduce a composition>\n"
		default:
			ta, tb := oa.view.tokens(r), ob.view.tokens(r)
			if r.paths() {
				pair(oa.view, ob.view, ta, tb)
			}
			sa, _, err := show(oa.view, r, ta, ea, vm)
			if err != nil {
				return err
			}
			sb, _, err := show(ob.view, r, tb, eb, vm)
			if err != nil {
				return err
			}
			if text, err = udiff(filepath.Join(out, "render", r.name), "revision-a", "revision-b", sa, sb); err != nil {
				return err
			}
		}
		adir := filepath.Join(artifacts, r.name, pairName, baseName)
		if err := os.MkdirAll(adir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(adir, "diff.txt"), []byte(text), 0o644); err != nil {
			return err
		}
		observed[r.name] = "clean"
		for _, f := range scan(text, all, baseText) {
			leaks.row(baseName, pairName, r.name, "diff", f.Secret, f.Class, f.Form, strconv.Itoa(f.Count))
			observed[r.name] = "leak"
		}
	}
	for _, r := range []string{"none", "path+schema+value"} {
		want := map[string]string{"none": "leak", "path+schema+value": "clean"}[r]
		expect("pair-leak."+r, want, observed[r])
	}
	fid := "ok"
	if !faithful {
		fid = "fail"
	}
	expect("fidelity", "ok", fid)

	// Stale provenance: b under a's composed paths, by display path, and under a's values.
	if !composedBoth || !faithful {
		control("stale-paths-leak", "inconclusive", "a revision did not compose or was not reproduced")
		control("stale-values-leak", "inconclusive", "a revision did not compose or was not reproduced")
		return writeTables(out, controls, expectations, leaks)
	}
	byPath := map[string]string{}
	for _, l := range oa.view.tree.Leaves {
		if tok, ok := oa.view.comp[l.Pos]; ok {
			byPath[l.Doc+" "+l.Path] = tok
		}
	}
	stale := map[string]string{}
	wrong := 0
	for _, l := range ob.view.tree.Leaves {
		if tok, ok := byPath[l.Doc+" "+l.Path]; ok {
			stale[l.Pos] = tok
			if tok != ob.view.comp[l.Pos] {
				wrong++
			}
		}
	}
	sb, _, err := show(ob.view, reps[4], stale, eb, vm)
	if err != nil {
		return err
	}
	n := len(scan(sb, refB, baseText))
	control("stale-paths-leak", firedIf(n > 0), fmt.Sprintf("%d findings of revision b's values under revision a's composed paths", n))
	control("stale-paths-version", firedIf(wrong > 0), fmt.Sprintf("%d leaves where revision a's token names another reference or version than b's own", wrong))
	sv, _ := matcher(ca.secrets, baseSecrets).redact(ob.view.text)
	n = len(scan(sv, refB, baseText))
	control("stale-values-leak", firedIf(n > 0), fmt.Sprintf("%d findings of revision b's values under revision a's values", n))
	return writeTables(out, controls, expectations, leaks)
}

func firedIf(b bool) string {
	if b {
		return "fired"
	}
	return "did-not-fire"
}

func writeTables(out string, controls, expectations, leaks table) error {
	for name, t := range map[string]*table{"controls.tsv": &controls, "expectations.tsv": &expectations, "leaks.tsv": &leaks} {
		if err := os.WriteFile(filepath.Join(out, name), []byte(t.b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}
