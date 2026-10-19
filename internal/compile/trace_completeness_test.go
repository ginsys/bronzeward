package compile

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// C §8.1 and §13: no real fragment may be silently dropped when its trace
// counterpart cannot be constructed. Put it on either side of a boolean source
// to include the flip pass in the successful controls.
func TestCompileRefusesAnUntraceableFragment(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	boolean := source(t, "machine:\n  features:\n    rbac: !bwref enabled\n",
		refs(map[string]ingest.Reference{"enabled": ref(provider.KindBoolean)}),
		map[string]provider.Value{"enabled": value(t, provider.KindBoolean, false)})
	for _, position := range []string{"before-boolean", "after-boolean"} {
		t.Run(position, func(t *testing.T) {
			for _, text := range []string{"traceable-label", "x-0"} {
				label := source(t, "machine:\n  nodeLabels:\n    tier: !bwref tier\n", strRef("tier"),
					map[string]provider.Value{"tier": value(t, provider.KindString, text)})
				fragments := []Source{label, boolean}
				if position == "after-boolean" {
					fragments = []Source{boolean, label}
				}
				real, err := Compose(mustResolve(t, base), []ingest.Resolved{mustResolve(t, fragments[0]), mustResolve(t, fragments[1])})
				if err != nil {
					t.Fatal(err)
				}
				if err := real.Validate(ModeMetal); err != nil {
					t.Fatalf("control: real configuration invalid: %v", err)
				}
				got, err := Compile(Input{Base: base, Fragments: fragments, Mode: ModeMetal})
				if text == "x-0" {
					var refusal *ingest.Refusal
					if !errors.As(err, &refusal) || refusal.Rule != ingest.RuleTraceIndistinct || len(got.Materialized().bytes()) != 0 {
						t.Fatalf("untraceable fragment: %v; want refusal and no artifact", err)
					}
					continue
				}
				if err != nil || !bytes.Equal(got.Materialized().bytes(), real.bytes()) {
					t.Fatalf("traceable control did not retain both real inputs: %v", err)
				}
				if len(pathsOf(got, "tier")) != 1 || len(pathsOf(got, "enabled")) != 1 {
					t.Fatal("traceable control did not attribute the label and boolean")
				}
			}
		})
	}
}
