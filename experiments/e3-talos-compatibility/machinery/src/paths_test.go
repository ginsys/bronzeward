package main

import (
	"slices"
	"strings"
	"testing"
)

func TestPathDiffNamesPathsNeverValues(t *testing.T) {
	a := []byte("version: v1alpha1\nmachine:\n  token: aaaa\n  kubelet:\n    image: k:1\ncluster:\n  id: x\n")
	b := []byte("version: v1alpha1\nmachine:\n  token: bbbb\n  kubelet:\n    image: k:1\n    extra: {}\n")
	got, err := pathDiff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"- 0/cluster",
		"+ 0/machine/kubelet/extra",
		"~ 0/machine/token",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pathDiff = %q, want %q", got, want)
	}
	for _, line := range got {
		for _, secret := range []string{"aaaa", "bbbb"} {
			if strings.Contains(line, secret) {
				t.Fatalf("line %q holds a value", line)
			}
		}
	}
}

func TestPathDiffNamesDocumentsByKind(t *testing.T) {
	a := []byte("version: v1alpha1\nmachine: {}\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: stable\n")
	b := []byte("version: v1alpha1\nmachine: {}\n")
	got, err := pathDiff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"- HostnameConfig"}; !slices.Equal(got, want) {
		t.Fatalf("pathDiff = %q, want %q", got, want)
	}
}

func TestPathDiffIdentical(t *testing.T) {
	a := []byte("a:\n  - 1\n  - 2\n")
	got, err := pathDiff(a, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("pathDiff of identical documents = %q", got)
	}
}

func TestDigestNormalizesTrailingNewlines(t *testing.T) {
	if digest([]byte("x\n\n\n")) != digest([]byte("x")) {
		t.Fatal("trailing newlines changed the digest")
	}
	if digest([]byte("x\n")) == digest([]byte("y\n")) {
		t.Fatal("different content gave one digest")
	}
}

func TestWithholdDiffKeepsTheReasonAndDropsTheDiff(t *testing.T) {
	for _, marker := range []string{"diff:", "Config diff:", "--- a"} {
		kept, withheld := withholdDiff("rpc error: desc = can't be applied\n\t* reason\n" + marker + "\n-  token: aaaa\n+  token: bbbb\n")
		if kept != "rpc error: desc = can't be applied\n\t* reason" || withheld != 3 {
			t.Fatalf("withholdDiff with %q = %q, %d", marker, kept, withheld)
		}
	}
	msg := "1 error occurred:\n\t* unknown machine type \"bogus\"\n\n"
	if kept, withheld := withholdDiff(msg); kept != msg || withheld != 0 {
		t.Fatalf("withholdDiff without a diff = %q, %d", kept, withheld)
	}
}
