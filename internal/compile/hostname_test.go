package compile

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The names and hostnames here are synthetic. compilation.md §6 step 5a and §6.1 item 2 say when the
// compiler fills a machine's hostname from its name and what source it records.

const (
	hostMachine  = "web-1"
	hostExplicit = "explicit-1"

	hostDocExplicit = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + hostExplicit + "\n"
	hostDocOff      = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\n"
	hostDocStable   = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: stable\n"
	hostLegacy      = "machine:\n  features:\n    stableHostname: true\n"
	hostLegacyOff   = "machine:\n  features:\n    stableHostname: false\n"
	hostLegacyRef   = "machine:\n  features:\n    stableHostname: !bwref app/stable\n"
)

// hostBase is the generated base without its HostnameConfig document: the only base on which
// machine.features.stableHostname is accepted (the document refuses to coexist with it).
func hostBase(t *testing.T) Source {
	t.Helper()
	cfg, err := configloader.NewFromBytes(generatedBase(t))
	if err != nil {
		t.Fatal(err)
	}
	var keep []config.Document
	for _, d := range cfg.Documents() {
		if d.Kind() != "HostnameConfig" {
			keep = append(keep, d)
		}
	}
	trimmed, err := container.New(keep...)
	if err != nil {
		t.Fatal(err)
	}
	b, err := trimmed.EncodeBytes(encoder.WithComments(encoder.CommentsAll))
	if err != nil {
		t.Fatal(err)
	}
	return source(t, string(b), ingest.Declarations{}, nil)
}

func generated(t *testing.T) Source {
	t.Helper()
	return source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
}

func frag(t *testing.T, text string) Source {
	t.Helper()
	return source(t, text, ingest.Declarations{}, nil)
}

func boolFrag(t *testing.T, v bool) Source {
	t.Helper()
	return source(t, hostLegacyRef, refs(map[string]ingest.Reference{"app/stable": ref(provider.KindBoolean)}),
		map[string]provider.Value{"app/stable": value(t, provider.KindBoolean, v)})
}

// composedHostname is the hostname the composed configuration's accessor returns, and whether the
// composition holds a HostnameConfig document.
func composedHostname(t *testing.T, c Compiled) (string, bool) {
	t.Helper()
	cfg, err := configloader.NewFromBytes(c.Materialized().bytes())
	if err != nil {
		t.Fatal(err)
	}
	doc := false
	for _, d := range cfg.Documents() {
		doc = doc || d.Kind() == "HostnameConfig"
	}
	h := cfg.NetworkHostnameConfig()
	if h == nil {
		return "", doc
	}
	return h.Hostname(), doc
}

func hostIn(base Source, name string, mode Mode, frags ...Source) Input {
	return Input{Base: base, Fragments: frags, Mode: mode, Name: name}
}

// wantHost asserts the decision, the hostname the composition ends up with, and that the patch left
// no trace in the origins a record can name.
func wantHost(t *testing.T, c Compiled, frags int, want Hostname, composed string) {
	t.Helper()
	if got := c.Hostname(); got != want {
		t.Errorf("decision %+v, want %+v", got, want)
	}
	if got, _ := composedHostname(t, c); got != composed {
		t.Errorf("composed hostname %q, want %q", got, composed)
	}
	if len(c.origins) != 1+frags {
		t.Errorf("%d origins, want %d: the patch is no source a record names", len(c.origins), 1+frags)
	}
	c.Provenance()
}

// The seven cases of the issue's first acceptance line, plus an absent document and a document at
// auto: stable, each composed in the modes that validate.
func TestHostnameDecision(t *testing.T) {
	for _, mode := range []Mode{ModeMetal, ModeCloud} {
		for _, tc := range []struct {
			name     string
			base     func(*testing.T) Source
			frags    []string
			machine  string
			want     Hostname
			composed string
		}{
			{"named, generated base: filled", generated, nil, hostMachine,
				Hostname{HostnameMachineName, hostMachine}, hostMachine},
			{"named, explicit hostname in a fragment: it wins", generated, []string{hostDocExplicit}, hostMachine,
				Hostname{HostnameConfiguration, ""}, hostExplicit},
			{"named, auto off and no hostname: no fill", generated, []string{hostDocOff}, hostMachine,
				Hostname{HostnameNone, ""}, ""},
			{"named, base with stableHostname true: no fill", hostBase, []string{hostLegacy}, hostMachine,
				Hostname{HostnameLegacyStable, ""}, ""},
			{"unnamed: no fill", generated, nil, "",
				Hostname{HostnameNone, ""}, ""},
			{"unnamed, explicit hostname: kept", generated, []string{hostDocExplicit}, "",
				Hostname{HostnameConfiguration, ""}, hostExplicit},
			{"unnamed, legacy stable hostname and no explicit one", hostBase, []string{hostLegacy}, "",
				Hostname{HostnameNone, ""}, ""},
			{"named, fragment auto stable and no hostname: filled", generated, []string{hostDocStable}, hostMachine,
				Hostname{HostnameMachineName, hostMachine}, hostMachine},
			{"named, no hostname document at all: filled", hostBase, nil, hostMachine,
				Hostname{HostnameMachineName, hostMachine}, hostMachine},
			{"named, a fragment turns stableHostname off over a base that sets it: filled", hostBase, []string{hostLegacy, hostLegacyOff}, hostMachine,
				Hostname{HostnameMachineName, hostMachine}, hostMachine},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				var frags []Source
				for _, f := range tc.frags {
					frags = append(frags, frag(t, f))
				}
				c := compiled(t, hostIn(tc.base(t), tc.machine, mode, frags...))
				wantHost(t, c, len(frags), tc.want, tc.composed)
			})
		}
	}
}

// In container mode the generated base fails validation with and without the patch (compilation.md
// §6.1 measured claims), so only that is asserted.
func TestHostnameContainerBaseStaysInvalid(t *testing.T) {
	for _, name := range []string{"", hostMachine} {
		if _, err := Compile(hostIn(generated(t), name, ModeContainer)); err == nil {
			t.Errorf("name %q: the generated base compiled in container mode", name)
		}
	}
}

// A stableHostname that depends on a secret reference counts as set whatever its value: no fill and
// source legacy-stable on a named machine, and the decision never tells which value the reference holds.
func TestHostnameStableDependingOnASecret(t *testing.T) {
	var shown []string
	for _, v := range []bool{false, true} {
		c := compiled(t, hostIn(hostBase(t), hostMachine, ModeMetal, boolFrag(t, v)))
		wantHost(t, c, 1, Hostname{HostnameLegacyStable, ""}, "")
		shown = append(shown, fmt.Sprintf("%+v", c.Hostname()))
	}
	if shown[0] != shown[1] {
		t.Errorf("the decision differs with the held value: %q and %q", shown[0], shown[1])
	}
	// A literal false composed over the reference: the setting no longer depends on the secret.
	c := compiled(t, hostIn(hostBase(t), hostMachine, ModeMetal, boolFrag(t, true), frag(t, hostLegacyOff)))
	wantHost(t, c, 2, Hostname{HostnameMachineName, hostMachine}, hostMachine)
}

// A boolean member of a mapping reference holding stableHostname is flipped like any boolean reference.
func TestHostnameStableInAMappingMember(t *testing.T) {
	f := source(t, "machine:\n  features: !bwref app/features\n",
		refs(map[string]ingest.Reference{"app/features": ref(provider.KindMapping)}),
		map[string]provider.Value{"app/features": value(t, provider.KindMapping, map[string]any{"stableHostname": false})})
	c := compiled(t, hostIn(hostBase(t), hostMachine, ModeMetal, f))
	wantHost(t, c, 1, Hostname{HostnameLegacyStable, ""}, "")
}

// A reference at hostname is explicit whatever it holds: one source and no fill for two values, and
// neither value reaches the decision. An empty value is refused before any decision.
func TestHostnameHeldByAReference(t *testing.T) {
	held := func(v string) Source {
		return source(t, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: !bwref app/host\n", strRef("app/host"),
			map[string]provider.Value{"app/host": value(t, provider.KindString, v)})
	}
	var shown []string
	for _, v := range []string{"alpha-host", "beta-host"} {
		c := compiled(t, hostIn(generated(t), hostMachine, ModeMetal, held(v)))
		wantHost(t, c, 1, Hostname{HostnameConfiguration, ""}, v)
		if s := fmt.Sprintf("%+v", c.Hostname()); strings.Contains(s, v) {
			t.Errorf("the decision shows the held value: %s", s)
		} else {
			shown = append(shown, s)
		}
	}
	if shown[0] != shown[1] {
		t.Errorf("the decision differs with the held value: %q and %q", shown[0], shown[1])
	}
	if _, err := Compile(hostIn(generated(t), hostMachine, ModeMetal, held(""))); err == nil {
		t.Error("an empty hostname reference compiled")
	}
}

// A reference at `auto` is refused before any decision (the trace pass does not reproduce an
// enumerated value), and `hostname` is the document's only other field, so no reference occurrence
// can sit inside a HostnameConfig document the fill would replace: the patch is never an overrider
// of one (compilation.md §6 step 5a). A characterization, green before the fill exists.
func TestHostnameReferenceAtAutoIsRefused(t *testing.T) {
	for _, v := range []string{"stable", "off"} {
		f := source(t, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: !bwref app/auto\n", strRef("app/auto"),
			map[string]provider.Value{"app/auto": value(t, provider.KindString, v)})
		for _, name := range []string{"", hostMachine} {
			_, err := Compile(hostIn(generated(t), name, ModeMetal, f))
			if err == nil || !strings.Contains(err.Error(), string(RuleFidelity)) {
				t.Errorf("value %q, name %q: %v, want a fidelity refusal", v, name, err)
			}
		}
	}
}

// A name the grammar refuses is an error and never a patch.
func TestHostnameRefusesAnInvalidName(t *testing.T) {
	for _, name := range []string{"Web-1", "web_1", "-web", "web-1.", "a b", "1.2"} {
		if _, err := Compile(hostIn(generated(t), name, ModeMetal)); err == nil {
			t.Errorf("name %q compiled", name)
		}
	}
	// A refused name that the composition would not use compiles: the explicit hostname wins first.
	if _, err := Compile(hostIn(generated(t), "Web-1", ModeMetal, frag(t, hostDocExplicit))); err != nil {
		t.Errorf("an explicit hostname beside an unused invalid name: %v", err)
	}
}

// A hostname a reference holds is a secret value: when its resolved value is invalid (too long), the
// refusal quotes neither it nor, since the compiler redacts what a reference holds (§8.3), anything
// that tells it. The test prints no text, only that the value is shown.
func TestHostnameInvalidHeldValueIsWithheld(t *testing.T) {
	long := strings.Repeat("a", 64)
	f := source(t, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: !bwref app/host\n", strRef("app/host"),
		map[string]provider.Value{"app/host": value(t, provider.KindString, long)})
	for _, name := range []string{"", hostMachine} {
		_, err := Compile(hostIn(generated(t), name, ModeMetal, f))
		if err == nil {
			t.Fatalf("name %q: an invalid held hostname compiled", name)
		}
		if strings.Contains(err.Error(), long) {
			t.Errorf("name %q: the refusal shows the held value", name)
		}
	}
}

// The guard after the second compile: an outcome overridden by the patch (an index past the caller's
// fragments) is the bare fidelity refusal; one overridden by a fragment, or by none, passes.
func TestNotOverriddenByPatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		by   []int
		bad  bool
	}{
		{"none overridden", []int{-1, -1}, false},
		{"overridden by the last fragment", []int{-1, 1}, false},
		{"overridden by the patch", []int{-1, 2}, true},
		{"overridden past the patch", []int{3}, true},
		{"no outcome", nil, false},
	} {
		var outcomes []outcome
		for _, by := range tc.by {
			outcomes = append(outcomes, outcome{by: by})
		}
		err := notOverriddenByPatch(outcomes, 2)
		var e *Error
		if tc.bad != (err != nil) || (tc.bad && (!errors.As(err, &e) || e.Rule != RuleFidelity || len(e.Paths) != 0)) {
			t.Errorf("%s: %v, want bare fidelity refusal: %v", tc.name, err, tc.bad)
		}
	}
}

// The fill comes before the output check (compilation.md §6 steps 5a and 7): a value held by a
// reference that equals the generated document's literal `auto: stable` is no copy once the patch
// has replaced that literal. An unnamed machine keeps the literal, so the copy is refused.
func TestHostnameFillPrecedesTheOutputCheck(t *testing.T) {
	f := source(t, "machine:\n  nodeLabels:\n    example: !bwref app/value\n", strRef("app/value"),
		map[string]provider.Value{"app/value": value(t, provider.KindString, "stable")})
	c := compiled(t, hostIn(generated(t), hostMachine, ModeMetal, f))
	wantHost(t, c, 1, Hostname{HostnameMachineName, hostMachine}, hostMachine)
	if _, err := Compile(hostIn(generated(t), "", ModeMetal, f)); err == nil {
		t.Error("an unnamed machine kept a copy of the literal and compiled")
	}
}

// The refusal for an invalid name is fixed text: it shows neither the name nor any hostname the
// composition holds, and the decision of a compilation with an explicit hostname does not either.
func TestHostnameShowsNoExplicitValue(t *testing.T) {
	_, err := Compile(hostIn(generated(t), "Web-1", ModeMetal))
	if err == nil {
		t.Fatal("an invalid name compiled")
	}
	if strings.Contains(err.Error(), "Web-1") {
		t.Errorf("the refusal shows the name: %v", err)
	}
	c := compiled(t, hostIn(generated(t), hostMachine, ModeMetal, frag(t, hostDocExplicit)))
	if s := fmt.Sprintf("%+v %v", c.Hostname(), c.Provenance()); strings.Contains(s, hostExplicit) {
		t.Errorf("the decision or a record shows an explicit hostname: %s", s)
	}
}
