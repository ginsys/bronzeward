package compile

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The pinned machinery checks the content of a secret field: it refuses a stand-in where a PEM
// block or a key stands. Validation of the trace and flip passes therefore restores the value of
// every schema-secret reference in memory (compilation.md §8.1). The control shows the stand-in
// composition of the same base is invalid, so the restore is what lets the base compile.
func TestCompileValidatesPassesWithSchemaSecretsRestored(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	trace, _, _, _, err := traceAll([]Source{base})
	if err != nil {
		t.Fatal(err)
	}
	tm, err := Compose(trace[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	var e *Error
	if err := tm.Validate(ModeMetal); !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("control: the stand-in composition: got %v, want an invalid verdict", err)
	}
	if _, err := Compile(Input{Base: base, Mode: ModeMetal}); err != nil {
		t.Fatalf("the generated base: %v", err)
	}
}

// The other tests compile a base with five v1.14 documents left out, so their v1alpha1 fragments
// stay valid. This one compiles the whole generated stream, no document left out, and so reaches
// the case the restore is for: the PEM and key content checks of every document, KubeNodeConfig and
// the rest included.
func TestCompileTheFullGeneratedStream(t *testing.T) {
	text := string(generatedStream(t))
	for _, kind := range []string{"KubeNodeConfig", "KubePrismConfig", "KubeNetworkConfig", "KubeFlannelCNIConfig", "ResolverConfig"} {
		if !strings.Contains(text, "kind: "+kind) {
			t.Fatalf("control: the generated stream holds no %s document", kind)
		}
	}
	c, err := Compile(Input{Base: source(t, text, ingest.Declarations{}, nil), Mode: ModeMetal})
	if err != nil {
		t.Fatalf("the full generated stream: %v", err)
	}
	if v, err := c.KubernetesVersion("v1.14.5"); err != nil || v == "" {
		t.Fatalf("its Kubernetes version: %q, %v", v, err)
	}
}

// On a base that holds a KubeNodeConfig document, node labels belong to that document: a v1alpha1
// machine.nodeLabels fragment is refused, and a KubeNodeConfig fragment with the same labels is
// composed and validates (ginsys/bronzeward#712).
func TestCompileNodeLabelsGoThroughKubeNodeConfig(t *testing.T) {
	base := source(t, string(generatedStream(t)), ingest.Declarations{}, nil)
	in := func(frag string) Input {
		return Input{Base: base, Fragments: []Source{source(t, frag, ingest.Declarations{}, nil)}, Mode: ModeMetal}
	}
	_, err := Compile(in("machine:\n  nodeLabels:\n    node.example.test/pool: workers\n"))
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("a v1alpha1 nodeLabels fragment: got %v, want an invalid verdict", err)
	}
	if _, err := Compile(in("apiVersion: v1alpha1\nkind: KubeNodeConfig\nlabels:\n  node.example.test/pool: workers\n")); err != nil {
		t.Fatalf("a KubeNodeConfig labels fragment: %v", err)
	}
}

// A leaf the operator marked, outside the schema-secret fields, keeps its stand-in in the passes:
// an advertised subnet the machinery reads as a CIDR is refused as a fidelity failure, since the
// stand-in is not one.
func TestCompileStillRefusesAMarkedLeafWhoseStandInIsInvalid(t *testing.T) {
	text := string(generatedBase(t))
	in := func(f Source) Input {
		return Input{Base: source(t, text, ingest.Declarations{}, nil), Fragments: []Source{f}, Mode: ModeMetal}
	}
	literal := source(t, "cluster:\n  etcd:\n    advertisedSubnets:\n      - 10.0.0.0/8\n", ingest.Declarations{}, nil)
	if _, err := Compile(in(literal)); err != nil {
		t.Fatalf("control: the unmarked subnet: %v", err)
	}
	marked := source(t, "cluster:\n  etcd:\n    advertisedSubnets:\n      - !bwref app/subnet\n", strRef("app/subnet"),
		map[string]provider.Value{"app/subnet": value(t, provider.KindString, "10.0.0.0/8")})
	_, err := Compile(in(marked))
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleFidelity {
		t.Fatalf("the marked subnet: got %v, want a fidelity refusal", err)
	}
}

// The restored passes hold real secrets, so no text of theirs may leave. A cluster secret that is
// not base64 makes the real composition invalid, and the machinery's message quotes the value. The
// message the compiler returns is redacted like any other, and no rendering of the error holds it.
func TestCompileRestoredPassesNeverEchoASecret(t *testing.T) {
	text := regexp.MustCompile(`(?m)^(\s*clusterSecret: )\S+`).ReplaceAllString(string(generatedBase(t)), "${1}"+compileSecret)
	_, err := Compile(Input{Base: source(t, text, ingest.Declarations{}, nil), Mode: ModeMetal})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("got %v, want an invalid verdict", err)
	}
	if !strings.Contains(e.Message, "invalid clusterSecret") || !strings.Contains(e.Message, "<redacted:") {
		t.Errorf("the message %q is neither the machinery's nor redacted", e.Message)
	}
	for _, rendered := range []string{
		err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err),
		fmt.Sprintf("%q", e.Message), strings.Join(e.Paths, " "), e.Input, string(e.Rule),
	} {
		if shows(rendered, compileSecret) {
			t.Errorf("a rendering holds the secret: %q", rendered)
		}
	}
}
