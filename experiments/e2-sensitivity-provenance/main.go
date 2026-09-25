// Command bwprov is issue 4's provenance tracker and redactor: it derives the real, trace and
// flip passes of a case, attributes every leaf of talosctl's composition to the reference
// occurrence it came from, and writes each representation's redacted diff, errors, log and
// support data with what the oracle finds in them. It is an experiment, not a library.
//
//	bwprov derive  <case-dir> <issue-3-cases-dir> <out>
//	bwprov analyse <cell> <base-secrets.tsv> <artifacts> <base> <case>
//	bwprov pair    <cell-a> <cell-b> <base-secrets.tsv> <artifacts> <out> <pair> <base>
//	bwprov summary <list.tsv> <results>        exit 3 when the run is incomplete
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bwprov:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	want := map[string]int{"derive": 3, "analyse": 5, "pair": 7, "summary": 2}
	if len(args) == 0 || want[args[0]] == 0 || len(args)-1 != want[args[0]] {
		return fmt.Errorf("usage: bwprov derive|analyse|pair|summary <args> (see the package comment)")
	}
	a := args[1:]
	switch args[0] {
	case "derive":
		return derive(a[0], a[1], a[2])
	case "analyse":
		return analyse(a[0], a[1], a[2], a[3], a[4])
	case "pair":
		return analysePair(a[0], a[1], a[2], a[3], a[4], a[5], a[6])
	}
	complete, err := summarise(a[0], a[1])
	if err != nil {
		return err
	}
	if !complete {
		fmt.Fprintln(os.Stderr, "bwprov: the run is incomplete; see", a[1]+"/summary.txt")
		os.Exit(3)
	}
	return nil
}
