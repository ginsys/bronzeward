package compile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// compileSecret is a synthetic value, used nowhere else.
const compileSecret = "compile-test-secret-7a3c"

// generatedBase is a freshly generated controlplane configuration, as the operator would import
// it, with the install disk talosctl gen config sets by default.
func generatedBase(t *testing.T) []byte {
	t.Helper()
	in, err := generate.NewInput("compile-test", "https://192.0.2.20:6443", constants.DefaultKubernetesVersion,
		generate.WithInstallDisk("/dev/sda"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := in.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsAll))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// resolved ingests text as bronzeward does (schema secrets extracted, marks applied), stores
// each extracted value beside the given ones, and resolves the result.
func resolved(t *testing.T, text string, decl ingest.Declarations, values map[string]provider.Value, marks ...string) ingest.Resolved {
	t.Helper()
	u, err := ingest.Read(strings.NewReader(text), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	req := ingest.Request{Input: u, Declarations: decl}
	for _, m := range marks {
		p, err := ingest.ParsePath(m)
		if err != nil {
			t.Fatal(err)
		}
		req.Marks = append(req.Marks, p)
	}
	c, err := ingest.Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	all := maps.Clone(values)
	if all == nil {
		all = map[string]provider.Value{}
	}
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		all[name] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := ingest.Resolve(s, all)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func value(t *testing.T, k provider.Kind, v any) provider.Value {
	t.Helper()
	val, err := provider.NewValue(k, v)
	if err != nil {
		t.Fatal(err)
	}
	return val
}

func strRef(name string) ingest.Declarations {
	return ingest.Declarations{References: map[string]ingest.Reference{name: {Kind: provider.KindString, Version: 1}}}
}

// native is talosctl's composition of literal inputs (machineconfig patch: LoadPatch, Apply,
// Bytes), normalized as SR normalized it, by re-encoding the loaded configuration.
func native(t *testing.T, base []byte, fragments ...string) []byte {
	t.Helper()
	var patches []configpatcher.Patch
	for _, f := range fragments {
		p, err := configpatcher.LoadPatch([]byte(f))
		if err != nil {
			t.Fatal(err)
		}
		patches = append(patches, p)
	}
	out, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := out.Config()
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestComposeMatchesNativeComposition(t *testing.T) {
	base := generatedBase(t)
	literal := "machine:\n  nodeLabels:\n    team: " + compileSecret + "\n"
	tagged := "machine:\n  nodeLabels:\n    team: !bwref team\n"
	plain := "cluster:\n  network:\n    dnsDomain: compile.test\n"
	m, err := Compose(
		resolved(t, string(base), ingest.Declarations{}, nil),
		[]ingest.Resolved{
			resolved(t, tagged, strRef("team"), map[string]provider.Value{"team": value(t, provider.KindString, compileSecret)}),
			resolved(t, plain, ingest.Declarations{}, nil),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := native(t, base, literal, plain)
	if !bytes.Equal(m.bytes(), want) {
		t.Fatalf("composition differs from native composition of the literal inputs")
	}
}

func TestComposeWithoutFragmentsReencodes(t *testing.T) {
	base := generatedBase(t)
	if !bytes.Contains(base, []byte("# ")) {
		t.Fatal("control: the generated base carries no comments")
	}
	m, err := Compose(resolved(t, string(base), ingest.Declarations{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.bytes(), native(t, base, "machine: {}\n")) {
		t.Fatal("a base without fragments is not re-encoded as talosctl's normalized output")
	}
}

func TestComposeRejectsTypeMismatch(t *testing.T) {
	base := generatedBase(t)
	frag := "machine:\n  features:\n    kubePrism:\n      port: !bwref port\n"
	r := resolved(t, frag, strRef("port"), map[string]provider.Value{"port": value(t, provider.KindString, compileSecret)})
	ok := resolved(t, "machine:\n  nodeLabels:\n    a: b\n", ingest.Declarations{}, nil)
	_, err := Compose(resolved(t, string(base), ingest.Declarations{}, nil), []ingest.Resolved{ok, r})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleRejected || e.Input != "fragment[1]" {
		t.Fatalf("got %v, want a rejection of fragment[1]", err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if s := fmt.Sprintf(verb, err); strings.Contains(s, compileSecret) || strings.Contains(s, "cannot") {
			t.Fatalf("%s renders the machinery's message: %s", verb, s)
		}
	}
}

func TestComposeRejectsAnUnloadableBase(t *testing.T) {
	r := resolved(t, "machine:\n  features:\n    kubePrism:\n      port: !bwref port\n", strRef("port"),
		map[string]provider.Value{"port": value(t, provider.KindString, compileSecret)})
	_, err := Compose(r, nil)
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleRejected || e.Input != "base" {
		t.Fatalf("got %v, want a rejection of the base", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", err), compileSecret) {
		t.Fatal("the error renders the value")
	}
}

func TestComposeRefusesReservedTextInOutput(t *testing.T) {
	base := generatedBase(t)
	tagged := "machine:\n  nodeLabels:\n    team: !bwref team\n"
	for _, tc := range []struct {
		name  string
		frag  string
		decl  ingest.Declarations
		value provider.Value
		want  []string
	}{
		{"value", tagged, strRef("team"), value(t, provider.KindString, "x-!bwref-y"), []string{"doc[0]"}},
		{"key", "machine:\n  nodeLabels: !bwref labels\n",
			ingest.Declarations{References: map[string]ingest.Reference{"labels": {Kind: provider.KindMapping, Version: 1}}},
			value(t, provider.KindMapping, map[string]string{"!bwref-k": "v"}), []string{"doc[0]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "team"
			if tc.name == "key" {
				name = "labels"
			}
			r := resolved(t, tc.frag, tc.decl, map[string]provider.Value{name: tc.value})
			_, err := Compose(resolved(t, string(base), ingest.Declarations{}, nil), []ingest.Resolved{r})
			var e *Error
			if !errors.As(err, &e) || e.Rule != RuleReservedText || fmt.Sprint(e.Paths) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want reserved-text at %v", err, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	base := generatedBase(t)
	b := resolved(t, string(base), ingest.Declarations{}, nil)
	m, err := Compose(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(ModeMetal); err != nil {
		t.Fatalf("a generated base does not validate in metal mode: %v", err)
	}
	frag := "cluster:\n  network:\n    dnsDomain: !bwref domain\n"
	bad, err := Compose(b, []ingest.Resolved{resolved(t, frag, strRef("domain"),
		map[string]provider.Value{"domain": value(t, provider.KindString, "compile not a domain 7a3c")})})
	if err != nil {
		t.Fatal(err)
	}
	err = bad.Validate(ModeMetal)
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("got %v, want an invalid verdict", err)
	}
	if s := fmt.Sprintf("%#v", err); strings.Contains(s, "7a3c") {
		t.Fatalf("the verdict renders the machinery's message: %s", s)
	}
	// A warning is a refusal, as talosctl validate --strict makes it.
	warned, err := Compose(b, []ingest.Resolved{resolved(t,
		"machine:\n  install:\n    extensions:\n      - image: ghcr.io/example/compile-test:1\n", ingest.Declarations{}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := warned.Validate(ModeMetal); !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("a warning: got %v, want an invalid verdict", err)
	}
	// The node's mode decides: container mode requires kube DNS forwarded to the host.
	unforwarded, err := Compose(b, []ingest.Resolved{resolved(t,
		"machine:\n  features:\n    hostDNS:\n      enabled: true\n      forwardKubeDNSToHost: false\n", ingest.Declarations{}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := unforwarded.Validate(ModeMetal); err != nil {
		t.Fatalf("metal mode: %v", err)
	}
	if err := unforwarded.Validate(ModeContainer); !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("container mode: got %v, want an invalid verdict", err)
	}
	if err := m.Validate(Mode("")); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if err := (Materialized{}).Validate(ModeMetal); err == nil {
		t.Fatal("the zero Materialized was validated")
	}
}

func TestModeIsRuntimeMode(t *testing.T) {
	for _, tc := range []struct {
		m                  Mode
		install, container bool
	}{
		{ModeMetal, true, false},
		{ModeContainer, false, true},
		{ModeCloud, false, false},
	} {
		if tc.m.String() != string(tc.m) || tc.m.RequiresInstall() != tc.install || tc.m.InContainer() != tc.container {
			t.Errorf("%s: install %v container %v", tc.m, tc.m.RequiresInstall(), tc.m.InContainer())
		}
	}
	if _, err := ParseMode("docker"); err == nil {
		t.Fatal("an unknown mode parsed")
	}
	if m, err := ParseMode("container"); err != nil || m != ModeContainer {
		t.Fatalf("container: %v %v", m, err)
	}
}

func TestMaterializedNeverRenders(t *testing.T) {
	r := resolved(t, "machine:\n  nodeLabels:\n    team: !bwref team\n", strRef("team"),
		map[string]provider.Value{"team": value(t, provider.KindString, compileSecret)})
	m, err := Compose(resolved(t, string(generatedBase(t)), ingest.Declarations{}, nil), []ingest.Resolved{r})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(m.bytes(), []byte(compileSecret)) {
		t.Fatal("control: the composition does not hold the value")
	}
	holder := struct{ M Materialized }{m}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		for _, x := range []any{m, &m, holder} {
			if s := fmt.Sprintf(verb, x); strings.Contains(s, compileSecret) || strings.Contains(s, "team") {
				t.Fatalf("%s renders the composition: %s", verb, s)
			}
		}
	}
	if _, err := json.Marshal(m); err == nil {
		t.Fatal("json marshalled the composition")
	}
	if _, err := yaml.Marshal(m); err == nil {
		t.Fatal("yaml marshalled the composition")
	}
	if _, err := m.MarshalText(); err == nil {
		t.Fatal("text marshalled the composition")
	}
}
