package monitor

import (
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
)

// kind is a DependencyAlert's kind (dependency monitor §6.2).
type kind string

const (
	kindLost       kind = "lost"
	kindBlocked    kind = "blocked"
	kindRegression kind = "regression"
	kindPersistent kind = "persistent"
	kindDeletion   kind = "deletion-scheduled"
)

// status is the part of a DependencyStatus row a pass reads and writes (§5.1). A zero time is
// NULL.
type status struct {
	class            classify.Class
	reason           classify.Reason
	firstRetained    time.Time
	unknownSince     time.Time
	persistentAt     time.Time
	deletionObserved time.Time
	deletionWarned   time.Time
}

// transition is §6.2 applied to the recorded status and a classification recorded at at: the
// status to record and the alerts its transition raises, other than a deletion warning, which
// depends on the releases (unwarned).
func transition(old status, r classify.Result, at time.Time, t Timings) (status, []kind) {
	s := old
	s.class, s.reason = r.Class, r.Reason
	s.deletionObserved = time.Time{}
	if r.Reason == classify.DeletionScheduled {
		s.deletionObserved = r.Deletion
	}
	var kinds []kind
	switch r.Class {
	case classify.Retained:
		if s.firstRetained.IsZero() {
			s.firstRetained = at
		}
	case classify.Lost:
		if old.class != classify.Lost {
			kinds = append(kinds, kindLost)
		}
	case classify.Blocked:
		if old.class != classify.Blocked {
			kinds = append(kinds, kindBlocked)
		}
	case classify.Unknown:
		if old.class != classify.Unknown {
			s.unknownSince, s.persistentAt = at, time.Time{}
		}
		if old.class == classify.Retained || old.class == classify.Blocked {
			kinds = append(kinds, kindRegression)
		}
		// At Persistent since unknown_since, then each Persistent since the last such alert.
		from := s.persistentAt
		if from.IsZero() {
			from = s.unknownSince
		}
		if !at.Before(from.Add(t.Persistent)) {
			kinds = append(kinds, kindPersistent)
			s.persistentAt = at
		}
	}
	if r.Class != classify.Unknown {
		s.unknownSince, s.persistentAt = time.Time{}, time.Time{}
	}
	return s, kinds
}

// unwarned is the releases a deletion warning names: those referencing the version that the
// version's earlier warnings for the same scheduled time did not name (§6.2), in the order given.
func unwarned(releases, warned []string) []string {
	var out []string
	for _, r := range releases {
		if !slices.Contains(warned, r) {
			out = append(out, r)
		}
	}
	return out
}
