// Command bwref is the reference resolver prototype of the structural-reference experiment (E2,
// ginsys/bronzeward issue 3). It derives each candidate reference syntax from one canonical case,
// resolves references before composition (early) or after it (late), records where it resolved
// them, and finds sentinel values after composition. It never merges: composition and validation
// are left to the pinned talosctl, which run/ calls.
//
//	bwref gen <case-dir> <out-dir>
//	    writes <out-dir>/{literal,tag,marked,binding}/<fragment>, binding/bindings.tsv and
//	    superset.tsv (every canonical reference: fragment, ref, document, path)
//	bwref resolve [-sentinel] <candidate> <case-dir> <gen-dir> <in> <out> [<fragment>]
//	    resolves <in> into <out>; with <fragment>, early (that fragment alone), else late (the
//	    composed configuration); prints one `ref<TAB>document<TAB>path` line per resolution
//	bwref find <case-dir> <in>
//	    prints `ref<TAB>document<TAB>path` for each sentinel found in <in>, and
//	    `unidentifiable<TAB>ref<TAB>reason` for each reference that has none
//	bwref info <case-dir>
//	    prints the case's fragments, native expectation and candidate expectations
//	bwref patterns <file>
//	    prints every scalar line of 16 characters or more in <file>: the leak-refusal patterns
//
// Phase-0 evidence (ginsys/bronzeward issue 3). Not v1 tooling.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bwref:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bwref gen|resolve|find|info|patterns ...")
	}
	switch args[0] {
	case "gen":
		if len(args) != 3 {
			return fmt.Errorf("usage: bwref gen <case-dir> <out-dir>")
		}
		return gen(args[1], args[2])
	case "resolve":
		sentinel := len(args) > 1 && args[1] == "-sentinel"
		if sentinel {
			args = args[1:]
		}
		if len(args) != 6 && len(args) != 7 {
			return fmt.Errorf("usage: bwref resolve [-sentinel] <candidate> <case-dir> <gen-dir> <in> <out> [<fragment>]")
		}
		fragment := ""
		if len(args) == 7 {
			fragment = args[6]
		}
		return resolve(sentinel, args[1], args[2], args[3], args[4], args[5], fragment)
	case "find":
		if len(args) != 3 {
			return fmt.Errorf("usage: bwref find <case-dir> <in>")
		}
		return find(args[1], args[2])
	case "info":
		if len(args) != 2 {
			return fmt.Errorf("usage: bwref info <case-dir>")
		}
		return info(args[1])
	case "patterns":
		if len(args) != 2 {
			return fmt.Errorf("usage: bwref patterns <file>")
		}
		return patterns(args[1])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func gen(caseDir, out string) error {
	c, err := LoadCase(caseDir)
	if err != nil {
		return err
	}
	for _, form := range []string{"literal", "tag", "marked", "binding"} {
		frags, bindings, occs, err := Generate(c, form)
		if err != nil {
			return fmt.Errorf("%s form: %w", form, err)
		}
		dir := filepath.Join(out, form)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for i, docs := range frags {
			if err := writeDocs(filepath.Join(dir, c.Fragments[i].File), docs); err != nil {
				return err
			}
		}
		if form == "binding" {
			var b strings.Builder
			for _, x := range bindings {
				fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", x.Fragment, x.Ref, x.Doc, x.Path)
			}
			if err := os.WriteFile(filepath.Join(dir, "bindings.tsv"), []byte(b.String()), 0o644); err != nil {
				return err
			}
		}
		if form == "literal" {
			var b strings.Builder
			for _, o := range occs {
				fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", o.Fragment, o.Ref, o.Doc, o.Path)
			}
			if err := os.WriteFile(filepath.Join(out, "superset.tsv"), []byte(b.String()), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func readBindings(path string) ([]Binding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Binding
	s := bufio.NewScanner(f)
	for s.Scan() {
		parts := strings.Split(s.Text(), "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("%s: malformed line %q", path, s.Text())
		}
		p, err := ParsePath(parts[3])
		if err != nil {
			return nil, err
		}
		out = append(out, Binding{Fragment: parts[0], Ref: parts[1], Doc: parts[2], Path: p})
	}
	return out, s.Err()
}

func resolve(sentinel bool, candidate, caseDir, genDir, in, out, fragment string) error {
	c, err := LoadCase(caseDir)
	if err != nil {
		return err
	}
	values := map[string]*yaml.Node{}
	if sentinel {
		values, _ = Sentinels(c)
	} else {
		for r := range c.Values {
			v := c.Values[r]
			values[r] = &v
		}
	}
	var bindings []Binding
	if candidate == "binding" {
		if bindings, err = readBindings(filepath.Join(genDir, "binding", "bindings.tsv")); err != nil {
			return err
		}
	}
	docs, err := readDocs(in)
	if err != nil {
		return err
	}
	res, err := Resolve(c, candidate, fragment, docs, bindings, values)
	if err != nil {
		return err
	}
	for _, r := range res {
		fmt.Printf("%s\t%s\t%s\n", r.Ref, r.Doc, r.Path)
	}
	return writeDocs(out, docs)
}

func find(caseDir, in string) error {
	c, err := LoadCase(caseDir)
	if err != nil {
		return err
	}
	sentinels, unidentifiable := Sentinels(c)
	docs, err := readDocs(in)
	if err != nil {
		return err
	}
	res, err := FindSentinels(c, docs, sentinels)
	if err != nil {
		return err
	}
	for _, r := range res {
		fmt.Printf("%s\t%s\t%s\n", r.Ref, r.Doc, r.Path)
	}
	refs := make([]string, 0, len(unidentifiable))
	for r := range unidentifiable {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, r := range refs {
		fmt.Printf("unidentifiable\t%s\t%s\n", r, unidentifiable[r])
	}
	return nil
}

func info(caseDir string) error {
	c, err := LoadCase(caseDir)
	if err != nil {
		return err
	}
	for _, f := range c.Fragments {
		fmt.Printf("fragment\t%s\n", f.File)
	}
	fmt.Printf("native\t%s\n", c.Native)
	for _, cand := range candidates {
		for _, order := range []string{"late", "early"} {
			e := c.Expect[cand][order]
			if e == "" {
				return fmt.Errorf("%s/case.yaml: no expectation for %s %s", caseDir, cand, order)
			}
			fmt.Printf("expect\t%s\t%s\t%s\n", cand, order, e)
		}
	}
	return nil
}

func patterns(path string) error {
	docs, err := readDocs(path)
	if err != nil {
		return err
	}
	for _, p := range Patterns(docs) {
		fmt.Println(p)
	}
	return nil
}

// minPattern is the shortest leak-refusal pattern: short enough for a Kubernetes bootstrap token
// (23 characters), long enough not to match ordinary words in a message.
const minPattern = 16

// Patterns lists every line, of minPattern characters or more, of every scalar in docs, once.
func Patterns(docs []*yaml.Node) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range docs {
		collectLeaves(d, func(n *yaml.Node) {
			for _, line := range strings.Split(n.Value, "\n") {
				if len(line) >= minPattern && !seen[line] {
					seen[line] = true
					out = append(out, line)
				}
			}
		})
	}
	return out
}
