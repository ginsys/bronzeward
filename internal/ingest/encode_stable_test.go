package ingest

import (
	"bytes"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// tail is a document whose last mapping ends in commented-out blocks, as a generated Talos
// configuration does: runs of comment lines at two indents with blank lines between them, one of
// the blank lines holding spaces, and a document separator after.
const tail = "machine:\n    ca:\n            key: abc\n        \n        # image: registry.example.test/etcd:v3.7.3\n\n" +
	"        #     - 10.0.0.0/8\n    #         - https://example.test/manifests/daemonset.yaml\n\n" +
	"    #     certLifetime: 1h0m0s # Admin kubeconfig certificate lifetime.\n---\n"

// rewrites is how many times the encoder writes text again before the writes stop changing it.
func rewrites(t *testing.T, text string) int {
	t.Helper()
	docs, err := parseStream([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeStream(docs)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 40; n++ {
		back, err := parseStream(out)
		if err != nil {
			t.Fatal(err)
		}
		again, err := encodeStream(back)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(again, out) {
			return n
		}
		out = again
	}
	t.Fatal("the encoder does not settle in 40 writes")
	return 0
}

// A tail of blank-line-separated comment runs needs more than three writes to settle (this one
// needs four), and it is reachable with any machinery version: the text names none. The stream is
// accepted, and what it returns is a fixed point of the encoder.
func TestEncodeStableTailOfCommentRuns(t *testing.T) {
	if n := rewrites(t, tail); n < 4 {
		t.Fatalf("the control tail settles in %d writes: it no longer needs more than three", n)
	}
	docs, err := parseStream([]byte(tail))
	if err != nil {
		t.Fatal(err)
	}
	out, back, err := encodeStable(docs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeStream(back)
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("the returned text is not a fixed point: %v", err)
	}
}

// Several such tails in one stream settle too, since the bound follows the blank lines.
func TestEncodeStableManyTails(t *testing.T) {
	text := strings.Repeat(tail, 6)
	if n := rewrites(t, text); n < 4 {
		t.Fatalf("the stream settles in %d writes", n)
	}
	docs, err := parseStream([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := encodeStable(docs); err != nil {
		t.Fatal(err)
	}
}

// A tail with no blank line settles within the floor of two writes, without the blank-line count.
func TestEncodeStableTailWithoutBlankLines(t *testing.T) {
	text := "machine:\n    ca:\n        key: abc\n        # image: registry.example.test/etcd:v3.7.3\n    # certLifetime: 1h0m0s\n---\n"
	if n := rewrites(t, text); n > 2 {
		t.Fatalf("the tail without a blank line settles in %d writes, more than the floor of two", n)
	}
	docs, err := parseStream([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := encodeStable(docs); err != nil {
		t.Fatal(err)
	}
}

// The loop accepts only a fixed point: an encoder whose output never settles is refused, whatever
// the bound. This is what keeps a bound that is too small from ever accepting an unstable stream.
func TestEncodeStableRefusesAnEncoderThatNeverSettles(t *testing.T) {
	docs, err := parseStream([]byte(tail))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	never := func([]*yaml.Node) ([]byte, error) {
		n++
		return []byte(strings.Repeat("# c\n", n) + "a: 1\n"), nil
	}
	if _, _, err := encodeStableWith(docs, never); err == nil || !strings.Contains(err.Error(), "no stable encoding") {
		t.Fatalf("an encoder that never settles gave %v", err)
	}
}
