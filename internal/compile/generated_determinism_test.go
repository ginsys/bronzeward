package compile

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The generated base and stream carry no random value. The ingest guard scans every scalar for the
// short stand-ins the tests plant, so a random certificate, key, token or id that held one would
// fail a test on a rare run (ginsys/bronzeward#572, ginsys/bronzeward#802). Two generations that
// agree byte for byte leave no random value of any shape, and a random value added later fails
// this test on its first run.
func TestGeneratedMaterialIsDeterministic(t *testing.T) {
	if a, b := generatedBase(t), generatedBase(t); !bytes.Equal(a, b) {
		t.Error("two generated bases differ: some value in them is random")
	}
	if a, b := generatedStream(t), generatedStream(t); !bytes.Equal(a, b) {
		t.Error("two generated streams differ: some value in them is random")
	}
}

// scalars returns every scalar, key or value, of every document of a YAML stream, as the guard
// reads them. Comments are not scalars.
func scalars(t *testing.T, stream []byte) []string {
	t.Helper()
	var out []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			out = append(out, n.Value)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(stream))
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			t.Fatal(err)
		}
		walk(&n)
	}
}

// With the material fixed, a short stand-in either always sits inside a scalar of it or never
// does. This names the short values the compile tests extract into a source built on the
// generated base (checks_test.go and render_test.go plant "abc") and shows the fixed material
// holds none of them. A test that extracts a new short value into such a source adds it here. A
// value planted only in a fragment is never scanned against the base, so it is not listed.
func TestGeneratedMaterialHoldsNoShortStandIn(t *testing.T) {
	for name, stream := range map[string][]byte{"base": generatedBase(t), "stream": generatedStream(t)} {
		for _, s := range scalars(t, stream) {
			for _, stand := range []string{"abc"} {
				if strings.Contains(s, stand) {
					t.Errorf("the generated %s holds the stand-in %q in a scalar of %d bytes", name, stand, len(s))
				}
			}
		}
	}
}
