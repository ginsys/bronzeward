package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func summaryFixture(t *testing.T, cellControls, pairControls, cellExpect string) (list, results string) {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, "c1", "analysis")
	writeFile(t, filepath.Join(c, "controls.tsv"), cellControls)
	writeFile(t, filepath.Join(c, "expectations.tsv"), cellExpect)
	writeFile(t, filepath.Join(c, "leaks.tsv"), "gen\tstring\tnone\tsupport\treg-pass@1\tref\texact\t2\n")
	writeFile(t, filepath.Join(c, "cell.tsv"), "base\tcase\tfidelity\ngen\tstring\tok\n")
	writeFile(t, filepath.Join(c, "provenance.tsv"), "occurrence\tref\n0\treg-pass\n")
	writeFile(t, filepath.Join(c, "dependencies.tsv"), "record\treference\tfragments\neffective\treg-pass@1\t-\n")
	p := filepath.Join(dir, "p1")
	writeFile(t, filepath.Join(p, "controls.tsv"), pairControls)
	writeFile(t, filepath.Join(p, "expectations.tsv"), "gen\tmoved\tpair-leak.none\tleak\tleak\tyes\n")
	writeFile(t, filepath.Join(p, "leaks.tsv"), "")
	list = filepath.Join(dir, "list.tsv")
	writeFile(t, list, "cell\tgen\tstring\t"+c+"\npair\tgen\tmoved\t"+p+"\tstale-paths-leak,stale-paths-version\n")
	return list, filepath.Join(dir, "results")
}

const goodCellControls = "gen\tstring\tfidelity-check-fires\tfired\tx\ngen\tstring\trender-identity\tpass\t-\n"
const goodPairControls = "gen\tmoved\tstale-paths-leak\tfired\tx\ngen\tmoved\tstale-values-leak\tdid-not-fire\tx\n" +
	"gen\tmoved\tstale-paths-version\tfired\tx\n"
const goodExpect = "gen\tstring\tleak.none\tleak\tleak\tyes\n"

func TestSummaryIsCompleteWhenEveryRowAndControlHolds(t *testing.T) {
	list, results := summaryFixture(t, goodCellControls, goodPairControls, goodExpect)
	complete, err := summarise(list, results)
	if err != nil {
		t.Fatal(err)
	}
	s := readFile(t, filepath.Join(results, "summary.txt"))
	if !complete || !strings.Contains(s, "verdict\tcomplete\n") {
		t.Fatalf("summary:\n%s", s)
	}
	leaks := readFile(t, filepath.Join(results, "leaks.tsv"))
	if !strings.HasPrefix(leaks, "base\tcase\trep\tartifact\tsecret\tclass\tform\tcount\n") || !strings.Contains(leaks, "reg-pass@1") {
		t.Errorf("leaks:\n%s", leaks)
	}
	if !strings.Contains(s, "none\t1\t0\n") {
		t.Errorf("per-representation counts missing:\n%s", s)
	}
}

// An observation that differs from the expectation is a result, not a gap: the run is complete
// and the summary counts it.
func TestSummaryCountsAnUnexpectedObservationWithoutCallingItIncomplete(t *testing.T) {
	list, results := summaryFixture(t, goodCellControls, goodPairControls, "gen\tstring\tleak.none\tleak\tclean\tno\n")
	complete, err := summarise(list, results)
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	s := readFile(t, filepath.Join(results, "summary.txt"))
	if !strings.Contains(s, "unexpected\t1\n") || !strings.Contains(s, "gen/string leak.none expected leak observed clean") {
		t.Fatalf("summary:\n%s", s)
	}
}

// A cell whose tracer pass did not reproduce its composition has no provenance to measure.
func TestSummaryIsIncompleteOnAFidelityFailure(t *testing.T) {
	list, results := summaryFixture(t, goodCellControls, goodPairControls, "gen\tstring\tfidelity\tok\tfail\tno\n")
	if complete, err := summarise(list, results); err != nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}

func TestSummaryIsIncompleteWhenAMustFireControlDidNot(t *testing.T) {
	list, results := summaryFixture(t, "gen\tstring\tfidelity-check-fires\tdid-not-fire\tx\n", goodPairControls, goodExpect)
	if complete, err := summarise(list, results); err != nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}

func TestSummaryIsIncompleteWhenAStaleControlNeverFired(t *testing.T) {
	list, results := summaryFixture(t, goodCellControls, "gen\tmoved\tstale-paths-leak\tdid-not-fire\tx\n", goodExpect)
	if complete, err := summarise(list, results); err != nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}

func TestSummaryIsIncompleteWhenACellIsMissing(t *testing.T) {
	list, results := summaryFixture(t, goodCellControls, goodPairControls, goodExpect)
	writeFile(t, list, readFile(t, list)+"cell\tgen\tabsent\t"+filepath.Join(filepath.Dir(list), "absent")+"\n")
	if complete, err := summarise(list, results); err != nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}
