package auth

import (
	"errors"
	"testing"

	"github.com/ginsys/bronzeward/internal/config"
)

// T5c records a subject the operator listed in deniedSubjects before revoking it (§10.4), so
// RecordHuman creates its row where EnsureHuman refuses.
func TestRecordHumanIgnoresDeniedSubjects(t *testing.T) {
	db := migrated(t)
	d := NewDenied([]config.DeniedSubject{{Iss: "https://idp.test", Sub: "gone"}})
	if _, err := EnsureHuman(t.Context(), db, d, "https://idp.test", "gone"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("EnsureHuman: %v; want ErrIdentityRevoked", err)
	}
	if n := count(t, db, "SELECT count(*) FROM principal"); n != 0 {
		t.Fatalf("EnsureHuman created %d rows for a denied subject", n)
	}
	a, err := RecordHuman(t.Context(), db, "https://idp.test", "gone")
	if err != nil {
		t.Fatal(err)
	}
	b, err := RecordHuman(t.Context(), db, "https://idp.test", "gone")
	if err != nil || b != a {
		t.Fatalf("second RecordHuman = %q, %v; want %q", b, err, a)
	}
	if _, err := RecordHuman(t.Context(), db, "https://idp.test", ""); err == nil {
		t.Fatal("empty subject accepted")
	}
}
