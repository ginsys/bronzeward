package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// testStore opens the PostgreSQL database named by E4X_TEST_PG_DSN (the fixture's, during a
// capture) and gives it a fresh schema with one machine whose Applied digest is "d0". CI has no
// PostgreSQL, so these tests skip there; run/all runs them against the fixture.
func testStore(t *testing.T, noScope bool) *Store {
	t.Helper()
	dsn := os.Getenv("E4X_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("E4X_TEST_PG_DSN is not set")
	}
	ctx := context.Background()
	s, err := OpenStore(ctx, dsn, NewLog(io.Discard, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Setup(ctx, "m1", "d0", noScope); err != nil {
		t.Fatal(err)
	}
	return s
}

func testPlan(t *testing.T, s *Store, id, artifact string) PlanSpec {
	t.Helper()
	p := PlanSpec{
		ID: id, Machine: "m1", ArtifactPath: "/nonexistent/" + id, ArtifactDigest: artifact,
		Expires: time.Hour, MaxAttempts: 2, Verify: time.Minute, MaxAge: time.Minute, Transport: time.Minute,
	}
	ctx := context.Background()
	if err := s.Plan(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(ctx, id); err != nil {
		t.Fatal(err)
	}
	return p
}

// evidence records a successful §3.1 observation of the machine for the plan's operation.
func evidence(t *testing.T, s *Store, op, digest string) {
	t.Helper()
	if _, err := s.RecordObservation(context.Background(), op, "m1", purposeEvidence, digest, nil); err != nil {
		t.Fatal(err)
	}
}

func wantRefusal(t *testing.T, err error, comparison int) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("want a refusal of comparison %d, got %v", comparison, err)
	}
	if r.Comparison != comparison {
		t.Fatalf("want a refusal of comparison %d, got comparison %d (%s)", comparison, r.Comparison, r.Reason)
	}
}

func TestCommitRefusesRevokedApproval(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d0")
	if err := s.Revoke(ctx, "A", "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	wantRefusal(t, err, 1)
}

func TestCommitRefusesStaleEvidence(t *testing.T) {
	s := testStore(t, false)
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d-other")
	_, err := s.Commit(context.Background(), "A", "X", ModeProtocol, nil)
	wantRefusal(t, err, 3)
}

func TestCommitRefusesHeldScope(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	testPlan(t, s, "B", "db")
	evidence(t, s, "A", "d0")
	if _, err := s.Commit(ctx, "A", "X", ModeProtocol, nil); err != nil {
		t.Fatal(err)
	}
	evidence(t, s, "B", "d0")
	_, err := s.Commit(ctx, "B", "Z", ModeProtocol, nil)
	wantRefusal(t, err, 4)
}

func TestCommitWithoutScopeIndexAdmitsSecondOperation(t *testing.T) {
	s := testStore(t, true)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	testPlan(t, s, "B", "db")
	evidence(t, s, "A", "d0")
	if _, err := s.Commit(ctx, "A", "X", ModeProtocol, nil); err != nil {
		t.Fatal(err)
	}
	evidence(t, s, "B", "d0")
	if _, err := s.Commit(ctx, "B", "Z", ModeProtocol, nil); err != nil {
		t.Fatalf("the control without the scope index should admit B: %v", err)
	}
}

func TestAttemptRefusesLostOwnership(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d0")
	gen, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Takeover(ctx, "A", "Y"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Attempt(ctx, "A", "X", gen, ModeProtocol, 0, nil)
	wantRefusal(t, err, comparisonOwner)
	// The control without the ownership check records the stale attempt.
	if _, err := s.Attempt(ctx, "A", "X", gen, ModeNoFence, 0, nil); err != nil {
		t.Fatalf("nofence control: %v", err)
	}
}

func TestAttemptAfterRevocationCancels(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d0")
	gen, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, "A", "test"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Attempt(ctx, "A", "X", gen, ModeProtocol, 0, nil)
	wantRefusal(t, err, 1)
	st, err := s.State(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if st != stateCancelled {
		t.Fatalf("state after a refused first attempt under revocation = %s, want %s", st, stateCancelled)
	}
}

func TestCompleteNeedsEveryAttemptAccounted(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d0")
	gen, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attempt(ctx, "A", "X", gen, ModeProtocol, 0, nil); err != nil {
		t.Fatal(err)
	}
	gen, err = s.Takeover(ctx, "A", "Y")
	if err != nil {
		t.Fatal(err)
	}
	// A matching digest does not account for X's attempt: X may be stalled, not finished.
	if _, err := s.RecordObservation(ctx, "A", "m1", purposeCompletion, "da", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, "A", "Y", gen); err == nil {
		t.Fatal("completed with an unaccounted attempt")
	}
	if err := s.Account(ctx, "A", 1, "test", "executor X reaped"); err != nil {
		t.Fatal(err)
	}
	// The observation above predates the accounting, so it still does not complete the operation.
	if _, err := s.Complete(ctx, "A", "Y", gen); err == nil {
		t.Fatal("completed from an observation taken before the attempt was accounted for")
	}
	if _, err := s.RecordObservation(ctx, "A", "m1", purposeCompletion, "da", nil); err != nil {
		t.Fatal(err)
	}
	st, err := s.Complete(ctx, "A", "Y", gen)
	if err != nil {
		t.Fatal(err)
	}
	if st != stateCompleted {
		t.Fatalf("state = %s, want %s", st, stateCompleted)
	}
}

// §3.2 item 3: only a retry may proceed on evidence showing the bound artifact's digest.
func TestEvidenceMayShowArtifactOnlyOnRetry(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "da")
	_, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	wantRefusal(t, err, 3)
	evidence(t, s, "A", "d0")
	gen, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attempt(ctx, "A", "X", gen, ModeProtocol, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Account(ctx, "A", 1, "test", "executor X reaped"); err != nil {
		t.Fatal(err)
	}
	gen, err = s.Takeover(ctx, "A", "Y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordObservation(ctx, "A", "m1", purposeRecovery, "d0", nil); err != nil {
		t.Fatal(err)
	}
	rev, err := s.ClassifySafeToRetry(ctx, "A", "Y", gen, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordObservation(ctx, "A", "m1", purposeRecovery, "da", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attempt(ctx, "A", "Y", gen, ModeProtocol, rev, nil); err != nil {
		t.Fatalf("a retry on evidence showing the artifact: %v", err)
	}
}

func TestRetryRefusedAfterNewerTimelineFact(t *testing.T) {
	s := testStore(t, false)
	ctx := context.Background()
	testPlan(t, s, "A", "da")
	evidence(t, s, "A", "d0")
	gen, err := s.Commit(ctx, "A", "X", ModeProtocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attempt(ctx, "A", "X", gen, ModeProtocol, 0, nil); err != nil {
		t.Fatal(err)
	}
	// X's attempt is accounted for by evidence, not by a response: X is believed stopped.
	if err := s.Account(ctx, "A", 1, "test", "executor X reaped"); err != nil {
		t.Fatal(err)
	}
	gen, err = s.Takeover(ctx, "A", "Y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordObservation(ctx, "A", "m1", purposeRecovery, "d0", nil); err != nil {
		t.Fatal(err)
	}
	rev, err := s.ClassifySafeToRetry(ctx, "A", "Y", gen, "test")
	if err != nil {
		t.Fatal(err)
	}
	// The belief was wrong: attempt 1's late acceptance arrives after the classification.
	if _, err := s.RecordResponse(ctx, "A", 1, "X", 1, Response{Class: respAccepted, Detail: "late"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Attempt(ctx, "A", "Y", gen, ModeProtocol, rev, nil)
	wantRefusal(t, err, comparisonRetry)
}
