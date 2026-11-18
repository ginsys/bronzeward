package api

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/staging"
)

func ptr[T any](v T) *T { return &v }

// TestPositionStep is §9.3's table: each value of step, and the order that makes the first match win.
func TestPositionStep(t *testing.T) {
	op := func(state string) *positionOperation { return &positionOperation{State: state} }
	plan := func(state string, current *bool) *positionPlan {
		return &positionPlan{State: state, ApprovalCurrent: current}
	}
	ing := func(state string, lapsed bool) *positionIngestion {
		return &positionIngestion{State: state, LeaseLapsed: lapsed}
	}
	handedOver := machineBody{Desired: ptr("rel_x"), Applied: &applied{Release: "rel_x"}}
	cases := []struct {
		name string
		b    positionBody
		want string
	}{
		{"unresolved", positionBody{Operation: op("unresolved")}, "operation-unresolved"},
		{"committed", positionBody{Operation: op("committed")}, "operation-in-flight"},
		{"sending", positionBody{Operation: op("sending")}, "operation-in-flight"},
		{"verifying", positionBody{Operation: op("verifying")}, "operation-in-flight"},
		{"approved, current", positionBody{Plan: plan("approved", ptr(true))}, "awaiting-dispatch"},
		{"approved, earlier epoch (case 3)", positionBody{Plan: plan("approved", ptr(false))}, "awaiting-approval"},
		{"proposed", positionBody{Plan: plan("proposed", nil)}, "awaiting-approval"},
		{"paused", positionBody{Ingestion: ing("paused", false)}, "import-paused"},
		{"held, lapsed", positionBody{Ingestion: ing("held", true)}, "import-stalled"},
		{"resumed, lapsed", positionBody{Ingestion: ing("resumed", true)}, "import-stalled"},
		{"held", positionBody{Ingestion: ing("held", false)}, "import-running"},
		{"resumed", positionBody{Ingestion: ing("resumed", false)}, "import-running"},
		{"released, open draft", positionBody{Ingestion: ing("released", false), Draft: &positionDraft{State: "open"}}, "draft-to-publish"},
		{"released, published draft", positionBody{Ingestion: ing("released", false), Draft: &positionDraft{State: "published"}, Machine: handedOver}, "no-step"},
		{"abandoned", positionBody{Ingestion: ing("abandoned", false), Draft: &positionDraft{State: "open"}, Machine: handedOver}, "no-step"},
		{"drift", positionBody{Machine: machineBody{OpenDrift: &openDriftBody{}}}, "drift-open"},
		{"desired, no applied", positionBody{Machine: machineBody{Desired: ptr("rel_x")}}, "needs-plan"},
		{"pending convergence", positionBody{Machine: machineBody{Desired: ptr("rel_x"), Applied: &applied{}, PendingConvergence: true}}, "needs-plan"},
		{"neither", positionBody{}, "not-handed-over"},
		{"converged", positionBody{Machine: handedOver}, "no-step"},
		{"applied, no desired", positionBody{Machine: machineBody{Applied: &applied{}}}, "no-step"},

		// The order: an earlier row beats a later one that also holds.
		{"unresolved beats a plan", positionBody{Operation: op("unresolved"), Plan: plan("approved", ptr(true))}, "operation-unresolved"},
		{"in flight beats a plan", positionBody{Operation: op("sending"), Plan: plan("proposed", nil)}, "operation-in-flight"},
		{"a plan beats a live import", positionBody{Plan: plan("proposed", nil), Ingestion: ing("held", false)}, "awaiting-approval"},
		{"a live import beats drift (case 4)", positionBody{Ingestion: ing("held", true), Machine: machineBody{OpenDrift: &openDriftBody{}}}, "import-stalled"},
		{"a paused import beats drift", positionBody{Ingestion: ing("paused", false), Machine: machineBody{OpenDrift: &openDriftBody{}}}, "import-paused"},
		{"a draft to publish beats drift", positionBody{Ingestion: ing("released", false), Draft: &positionDraft{State: "open"},
			Machine: machineBody{OpenDrift: &openDriftBody{}}}, "draft-to-publish"},
		{"drift beats needs-plan", positionBody{Machine: machineBody{Desired: ptr("rel_x"), OpenDrift: &openDriftBody{}}}, "drift-open"},
		{"a held machine with no plan is by its fields", positionBody{Machine: machineBody{Desired: ptr("rel_x")}, RolloutHeldBy: &positionHolder{}}, "needs-plan"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := positionStep(&c.b); got != c.want {
				t.Fatalf("step = %q, want %q", got, c.want)
			}
		})
	}
}

// TestComparison2Terms is each term of comparison 2 and of the drift rule alone, as the position
// read and the commitment share them.
func TestComparison2Terms(t *testing.T) {
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	ni := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }
	m := machineBinding{head: ns("rev_a"), desired: ns("rel_x"), baseline: ni(3)}
	p := planBinding{assignment: "rev_a", desired: ns("rel_x"), baseline: ni(3)}
	cases := []struct {
		name   string
		m      machineBinding
		p      planBinding
		failed bool
	}{
		{"all three equal", m, p, false},
		{"assignment head differs", machineBinding{head: ns("rev_b"), desired: m.desired, baseline: m.baseline}, p, true},
		{"the machine has no assignment head", machineBinding{desired: m.desired, baseline: m.baseline}, p, true},
		{"baseline differs", machineBinding{head: m.head, desired: m.desired, baseline: ni(4)}, p, true},
		{"the plan bound a baseline, the machine has none", machineBinding{head: m.head, desired: m.desired}, p, true},
		{"desired differs", machineBinding{head: m.head, desired: ns("rel_y"), baseline: m.baseline}, p, true},
		{"the plan bound no desired, the machine has one", m, planBinding{assignment: "rev_a", baseline: ni(3)}, true},
		{"neither has a desired (a handover)", machineBinding{head: m.head, baseline: m.baseline},
			planBinding{assignment: "rev_a", baseline: ni(3)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := comparison2(c.m, c.p) != ""; got != c.failed {
				t.Fatalf("comparison2 failed = %v, want %v", got, c.failed)
			}
		})
	}
}

// TestDriftCause is the drift rule: with a record open only a plan that binds it passes, and with
// none open only a plan that binds none.
func TestDriftCause(t *testing.T) {
	ni := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }
	for _, c := range []struct {
		name        string
		open, bound sql.NullInt64
		failed      bool
	}{
		{"none open, none bound", ni(0), ni(0), false},
		{"one open, the plan binds it", ni(7), ni(7), false},
		{"one open, the plan binds none", ni(7), ni(0), true},
		{"one open, the plan binds another", ni(7), ni(5), true},
		{"none open, the plan binds a closed one", ni(0), ni(5), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := driftCause(c.open, c.bound) != ""; got != c.failed {
				t.Fatalf("driftCause failed = %v, want %v", got, c.failed)
			}
		})
	}
}

// The position read takes one clock reading and passes it to every time-evaluated member: the
// statements it builds name that parameter and never the database's clock, while the statements
// every other read uses still read the clock per row.
func TestPositionStatementsUseOneClock(t *testing.T) {
	for name, sqlText := range map[string]string{
		"ingestions": selectIngestionsAt("$2"), "plan state": planStateAt("$2"),
	} {
		if strings.Contains(sqlText, "clock_timestamp") || !strings.Contains(sqlText, "$2") {
			t.Errorf("%s reads another clock than the parameter: %s", name, sqlText)
		}
	}
	for name, sqlText := range map[string]string{
		"ingestions": selectIngestions, "plan state": planStateSQL, "due": staging.DueSQL, "effective state": staging.EffectiveStateSQL,
	} {
		if !strings.Contains(sqlText, "clock_timestamp()") && !strings.Contains(sqlText, "c.t") {
			t.Errorf("%s no longer reads the database's clock: %s", name, sqlText)
		}
	}
}
