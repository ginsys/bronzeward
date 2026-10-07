package monitor

import (
	"slices"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
)

var t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

func result(c classify.Class, r classify.Reason) classify.Result {
	return classify.Result{Class: c, Reason: r}
}

// TestTransition holds each row of dependency monitor §6.2's table that a pass raises.
func TestTransition(t *testing.T) {
	tm := Defaults()
	unknownAt := func(since, persistent time.Time) status {
		return status{class: classify.Unknown, reason: classify.Absent, unknownSince: since, persistentAt: persistent}
	}
	cases := []struct {
		name     string
		old      status
		r        classify.Result
		at       time.Time
		want     []kind
		since    time.Time
		persists time.Time
	}{
		{"retained stays", status{class: classify.Retained}, result(classify.Retained, classify.None), t0, nil, time.Time{}, time.Time{}},
		{"retained to lost", status{class: classify.Retained}, result(classify.Lost, classify.Destroyed), t0, []kind{kindLost}, time.Time{}, time.Time{}},
		{"lost stays", status{class: classify.Lost, reason: classify.Destroyed}, result(classify.Lost, classify.Pruned), t0, nil, time.Time{}, time.Time{}},
		{"unknown to lost", unknownAt(t0.Add(-time.Hour), time.Time{}), result(classify.Lost, classify.Destroyed), t0, []kind{kindLost}, time.Time{}, time.Time{}},
		{"retained to blocked", status{class: classify.Retained}, result(classify.Blocked, classify.SoftDeleted), t0, []kind{kindBlocked}, time.Time{}, time.Time{}},
		{"blocked stays", status{class: classify.Blocked, reason: classify.SoftDeleted}, result(classify.Blocked, classify.SoftDeleted), t0, nil, time.Time{}, time.Time{}},
		{"lost to blocked", status{class: classify.Lost, reason: classify.Destroyed}, result(classify.Blocked, classify.BelowDecryptionFloor), t0, []kind{kindBlocked}, time.Time{}, time.Time{}},
		{"retained to unknown", status{class: classify.Retained}, result(classify.Unknown, classify.Absent), t0, []kind{kindRegression}, t0, time.Time{}},
		{"blocked to unknown", status{class: classify.Blocked, reason: classify.SoftDeleted}, result(classify.Unknown, classify.Unavailable), t0, []kind{kindRegression}, t0, time.Time{}},
		{"lost to unknown", status{class: classify.Lost, reason: classify.Destroyed}, result(classify.Unknown, classify.Absent), t0, nil, t0, time.Time{}},
		{"unknown before 15 minutes", unknownAt(t0.Add(-tm.Persistent+time.Second), time.Time{}), result(classify.Unknown, classify.Denied), t0, nil, t0.Add(-tm.Persistent + time.Second), time.Time{}},
		{"unknown at 15 minutes", unknownAt(t0.Add(-tm.Persistent), time.Time{}), result(classify.Unknown, classify.Denied), t0, []kind{kindPersistent}, t0.Add(-tm.Persistent), t0},
		{"persistent not again before 15 minutes", unknownAt(t0.Add(-time.Hour), t0.Add(-tm.Persistent+time.Second)), result(classify.Unknown, classify.Absent), t0, nil, t0.Add(-time.Hour), t0.Add(-tm.Persistent + time.Second)},
		{"persistent again at 15 minutes", unknownAt(t0.Add(-time.Hour), t0.Add(-tm.Persistent)), result(classify.Unknown, classify.Absent), t0, []kind{kindPersistent}, t0.Add(-time.Hour), t0},
		{"unknown to retained", unknownAt(t0.Add(-time.Hour), t0.Add(-time.Minute)), result(classify.Retained, classify.None), t0, nil, time.Time{}, time.Time{}},
		{"unknown to blocked", unknownAt(t0.Add(-time.Hour), t0.Add(-time.Minute)), result(classify.Blocked, classify.SoftDeleted), t0, []kind{kindBlocked}, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, kinds := transition(c.old, c.r, c.at, tm)
			if !slices.Equal(kinds, c.want) {
				t.Fatalf("alerts %v, want %v", kinds, c.want)
			}
			if got.class != c.r.Class || got.reason != c.r.Reason {
				t.Fatalf("recorded %s/%s, want %s/%s", got.class, got.reason, c.r.Class, c.r.Reason)
			}
			if !got.unknownSince.Equal(c.since) || !got.persistentAt.Equal(c.persists) {
				t.Fatalf("unknown_since %v persistent %v, want %v %v", got.unknownSince, got.persistentAt, c.since, c.persists)
			}
		})
	}
}

// TestTransitionFirstRetained: the first-seen-retained time is set once and kept (§5.1).
func TestTransitionFirstRetained(t *testing.T) {
	got, _ := transition(status{class: classify.Unknown, reason: classify.Absent, unknownSince: t0}, result(classify.Retained, classify.None), t0.Add(time.Minute), Defaults())
	if !got.firstRetained.Equal(t0.Add(time.Minute)) {
		t.Fatalf("first retained %v", got.firstRetained)
	}
	got, _ = transition(status{class: classify.Retained, firstRetained: t0}, result(classify.Unknown, classify.Absent), t0.Add(time.Hour), Defaults())
	if !got.firstRetained.Equal(t0) {
		t.Fatalf("first retained moved to %v", got.firstRetained)
	}
}

// TestTransitionSchedule: a scheduled deletion is recorded as observed, only with that reason; the
// time last warned is kept for the warning step.
func TestTransitionSchedule(t *testing.T) {
	at := t0.Add(48 * time.Hour)
	got, kinds := transition(status{class: classify.Retained, deletionWarned: t0}, classify.Result{Class: classify.Retained, Reason: classify.DeletionScheduled, Deletion: at}, t0, Defaults())
	if !got.deletionObserved.Equal(at) || !got.deletionWarned.Equal(t0) || kinds != nil {
		t.Fatalf("observed %v warned %v alerts %v", got.deletionObserved, got.deletionWarned, kinds)
	}
	got, _ = transition(got, classify.Result{Class: classify.Blocked, Reason: classify.SoftDeleted, Deletion: t0}, t0, Defaults())
	if !got.deletionObserved.IsZero() {
		t.Fatalf("a past deletion recorded as scheduled: %v", got.deletionObserved)
	}
}

// TestUnwarned: a repeated warning names only the releases the earlier ones for that time did not.
func TestUnwarned(t *testing.T) {
	got := unwarned([]string{"rel_a", "rel_b", "rel_c"}, []string{"rel_b", "rel_x"})
	if !slices.Equal(got, []string{"rel_a", "rel_c"}) {
		t.Fatalf("unwarned %v", got)
	}
	if got := unwarned([]string{"rel_a"}, []string{"rel_a"}); len(got) != 0 {
		t.Fatalf("unwarned %v", got)
	}
}

// TestNext: a pass starts one interval after the previous one started, or at once if that took
// longer (§6.1).
func TestNext(t *testing.T) {
	iv := time.Minute
	if d := next(t0, t0.Add(10*time.Second), iv); d != 50*time.Second {
		t.Fatalf("wait %v", d)
	}
	if d := next(t0, t0.Add(2*time.Minute), iv); d != 0 {
		t.Fatalf("wait %v", d)
	}
	if d := next(t0, t0.Add(iv), iv); d != 0 {
		t.Fatalf("wait %v", d)
	}
}
