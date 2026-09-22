package staging

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

// These tests cover the guards that run before any statement and the process identity both modes
// depend on. The claim lifecycle itself needs a real PostgreSQL — a claim's whole purpose is to be
// visible to a second process, and an in-process substitute has no second process.

// failCipher records whether it was asked to do anything, so a test can assert a guard ran before
// the provider was contacted.
type failCipher struct{ used bool }

func (c *failCipher) Encrypt(context.Context, string, []byte) (string, error) {
	c.used = true
	return "", errors.New("the cipher should not have been reached")
}

func (c *failCipher) Decrypt(context.Context, string, string) ([]byte, error) {
	c.used = true
	return nil, errors.New("the cipher should not have been reached")
}

// TestBothModesSatisfyTheInterface pins that the comparison is like-for-like at the call site: the
// ingestion flow does not know which alternative it is running.
func TestBothModesSatisfyTheInterface(t *testing.T) {
	encrypted, err := NewEncrypted(nil, &failCipher{}, "bw-artifact", 0)
	if err != nil {
		t.Fatalf("NewEncrypted: %v", err)
	}

	modes := []Staging{NewTransient(nil, 0), encrypted}
	want := map[Mode]bool{ModeTransient: true, ModeEncrypted: true}
	for _, s := range modes {
		if !want[s.Mode()] {
			t.Errorf("unexpected mode %q", s.Mode())
		}
		delete(want, s.Mode())
	}
	if len(want) != 0 {
		t.Errorf("modes not covered: %v", want)
	}
}

// TestHoldRefusesAnUnextractedChange checks both modes reject the zero Sanitized before touching
// the database or the provider. Staging is a persistence surface in its own right under §7.1, so
// the same guard that protects the draft has to protect it.
func TestHoldRefusesAnUnextractedChange(t *testing.T) {
	cipher := &failCipher{}
	encrypted, err := NewEncrypted(nil, cipher, "bw-artifact", 0)
	if err != nil {
		t.Fatalf("NewEncrypted: %v", err)
	}

	for name, s := range map[string]Staging{"transient": NewTransient(nil, 0), "encrypted": encrypted} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Hold(context.Background(), "run-1", "operator", secret.Sanitized{}, checkpoint.InReview)
			if err == nil {
				t.Fatal("Hold accepted a change that did not come from extraction")
			}
			if !strings.Contains(err.Error(), "did not come from extraction") {
				t.Errorf("the error does not explain the refusal: %v", err)
			}
		})
	}
	if cipher.used {
		t.Error("encrypted staging encrypted an unextracted change before refusing it")
	}
}

// TestEncryptedHoldRequiresAPrincipal checks an unowned claim cannot be created. This mode's
// proposition is that a different principal can resume; a claim with no owner could be taken by
// anyone, which is a different proposition entirely.
func TestEncryptedHoldRequiresAPrincipal(t *testing.T) {
	cipher := &failCipher{}
	e, err := NewEncrypted(nil, cipher, "bw-artifact", 0)
	if err != nil {
		t.Fatalf("NewEncrypted: %v", err)
	}

	valid := secret.NewSanitized([]byte("machine: {}\n"), nil)
	if _, err := e.Hold(context.Background(), "run-1", "", valid, checkpoint.InReview); err == nil {
		t.Fatal("Hold accepted a claim with no principal")
	}
	if cipher.used {
		t.Error("the change was encrypted before the principal was checked")
	}
}

// TestNewEncryptedRequiresACipherAndAKey checks the constructor fails rather than the first hold.
func TestNewEncryptedRequiresACipherAndAKey(t *testing.T) {
	if _, err := NewEncrypted(nil, nil, "bw-artifact", 0); err == nil {
		t.Error("NewEncrypted accepted a nil cipher")
	}
	if _, err := NewEncrypted(nil, &failCipher{}, "", 0); err == nil {
		t.Error("NewEncrypted accepted an empty key name")
	}
}

// TestDefaultLeaseIsAppliedToAZeroOrNegativeValue checks a caller that forgets the lease gets the
// default rather than a claim that is already expired.
func TestDefaultLeaseIsAppliedToAZeroOrNegativeValue(t *testing.T) {
	for _, lease := range []time.Duration{0, -time.Minute} {
		if got := NewTransient(nil, lease).claims.lease; got != DefaultLease {
			t.Errorf("transient lease %s became %s, want %s", lease, got, DefaultLease)
		}
		e, err := NewEncrypted(nil, &failCipher{}, "bw-artifact", lease)
		if err != nil {
			t.Fatalf("NewEncrypted: %v", err)
		}
		if got := e.claims.lease; got != DefaultLease {
			t.Errorf("encrypted lease %s became %s, want %s", lease, got, DefaultLease)
		}
	}
}

// TestExpiredIsEvaluatedAtRead covers the expiry rule. A sweeper that has not run yet leaves an
// expired claim looking valid, and the window between expiry and the sweep is exactly when a
// second party would take one it should not have.
func TestExpiredIsEvaluatedAtRead(t *testing.T) {
	now := time.Now()
	cl := Claim{ExpiresAt: now.Add(time.Minute)}

	if cl.Expired(now) {
		t.Error("a claim with a minute left reported itself expired")
	}
	if !cl.Expired(now.Add(2 * time.Minute)) {
		t.Error("a claim a minute past its expiry reported itself valid")
	}
	// The boundary: at exactly the expiry instant the claim is still good, since After is strict.
	if cl.Expired(cl.ExpiresAt) {
		t.Error("a claim reported itself expired at exactly its expiry instant")
	}
}

// TestProcessStartTokenIsStableAndNumeric covers the identity both modes rely on to tell a process
// apart from a later one that reused its PID.
func TestProcessStartTokenIsStableAndNumeric(t *testing.T) {
	first, err := ProcessStartToken()
	if err != nil {
		t.Fatalf("ProcessStartToken: %v", err)
	}
	if _, err := strconv.ParseUint(first, 10, 64); err != nil {
		t.Errorf("the token %q is not a number: %v", first, err)
	}

	second, err := ProcessStartToken()
	if err != nil {
		t.Fatalf("ProcessStartToken (again): %v", err)
	}
	if first != second {
		t.Errorf("the token changed within one process: %q then %q", first, second)
	}

	// The token must not be the PID. If it were, it would defend against nothing, since PID reuse
	// is the exact case it exists for — and reading the wrong field of /proc/self/stat is how that
	// would happen silently.
	if first == strconv.Itoa(os.Getpid()) {
		t.Errorf("the token %q is this process's PID, so it defends against nothing", first)
	}
}

// TestErrNotRecoverableSaysWhatToDoInstead covers the transient mode's result as a caller meets it.
// The change was in a process's memory, the process is gone, and nobody can resume — so the error
// has to carry the remedy, because at the moment it fires an operator is deciding whether to wait
// or to re-ingest.
func TestErrNotRecoverableSaysWhatToDoInstead(t *testing.T) {
	// The error a caller sees must say what to do, not only that it failed: at the moment this
	// fires, an operator is deciding whether to wait or to re-ingest.
	if !strings.Contains(ErrNotRecoverable.Error(), "re-ingest") {
		t.Errorf("ErrNotRecoverable does not say what to do instead: %v", ErrNotRecoverable)
	}
	if !strings.Contains(ErrNotRecoverable.Error(), "no principal can resume it") {
		t.Errorf("ErrNotRecoverable does not state the limitation plainly: %v", ErrNotRecoverable)
	}
}

// TestNewPrincipalIsDistinct checks the recovery runs get identifiers that are actually different,
// since the whole point of the encrypted mode's recovery test is that a *different* principal
// takes the claim.
func TestNewPrincipalIsDistinct(t *testing.T) {
	first, err := NewPrincipal("recovery")
	if err != nil {
		t.Fatalf("NewPrincipal: %v", err)
	}
	second, err := NewPrincipal("recovery")
	if err != nil {
		t.Fatalf("NewPrincipal: %v", err)
	}

	if first == second {
		t.Errorf("two principals are identical: %q", first)
	}
	if !strings.HasPrefix(first, "recovery-") {
		t.Errorf("the principal %q does not carry its prefix", first)
	}
}

// TestNullableHelpers checks an absent field becomes SQL NULL rather than a zero that a later
// query would have to know to treat as missing. The transient mode stores a PID and the encrypted
// mode does not, so this distinction carries real information in the claim table.
func TestNullableHelpers(t *testing.T) {
	if nullableInt(0) != nil {
		t.Error("a zero PID did not become NULL")
	}
	if nullableInt(42) != 42 {
		t.Error("a real PID was nulled")
	}
	if nullableString("") != nil {
		t.Error("an empty token did not become NULL")
	}
	if nullableString("123") != "123" {
		t.Error("a real token was nulled")
	}
}

// TestEnvelopeKeepsTheReferences is the regression for encrypted staging resuming a change without
// its references. Hold used to encrypt the document alone and Resume rebuilt it with none, so a
// draft recovered by a second principal was persisted with no secret_reference rows at all.
func TestEnvelopeKeepsTheReferences(t *testing.T) {
	refs := []secret.Reference{
		{Path: "doc[0].machine.token", URI: "openbao:secret/run/doc[0].machine.token#1", Digest: strings.Repeat("a", 64)},
		{Path: "doc[0].cluster.secret", URI: "openbao:secret/run/doc[0].cluster.secret#1", Digest: strings.Repeat("b", 64)},
	}
	held := secret.NewSanitized([]byte("machine:\n  token: bw:ref:x\n"), refs)

	plain, err := sealEnvelope(held)
	if err != nil {
		t.Fatalf("sealEnvelope: %v", err)
	}
	resumed, err := openEnvelope(plain)
	if err != nil {
		t.Fatalf("openEnvelope: %v", err)
	}

	if got, want := string(resumed.Document()), string(held.Document()); got != want {
		t.Errorf("document changed across staging: %q, want %q", got, want)
	}
	got := resumed.References()
	if len(got) != len(refs) {
		t.Fatalf("resumed with %d reference(s), want %d", len(got), len(refs))
	}
	for i := range refs {
		if got[i] != refs[i] {
			t.Errorf("reference %d changed across staging: %+v, want %+v", i, got[i], refs[i])
		}
	}
}

// TestSealEnvelopeRefusesInvalidUTF8 checks a document JSON would silently rewrite is refused at
// Hold rather than staged and resumed as something else.
func TestSealEnvelopeRefusesInvalidUTF8(t *testing.T) {
	if _, err := sealEnvelope(secret.NewSanitized([]byte("machine:\n  x: \xff\xfe\n"), nil)); err == nil {
		t.Fatal("sealEnvelope accepted a document that is not valid UTF-8")
	}
	for _, ref := range []secret.Reference{
		{Path: "doc[0].\xffa", URI: "u", Digest: "d"},
		{Path: "p", URI: "openbao:\xfe", Digest: "d"},
		{Path: "p", URI: "u", Digest: "\xff"},
	} {
		if _, err := sealEnvelope(secret.NewSanitized([]byte("machine: {}\n"), []secret.Reference{ref})); err == nil {
			t.Errorf("sealEnvelope accepted a reference that is not valid UTF-8: %q", ref)
		}
	}
}

// TestOpenEnvelopeRefusesABareDocument checks the payload shape the first version wrote — the
// document alone — is refused rather than resumed as a change with no references.
func TestOpenEnvelopeRefusesABareDocument(t *testing.T) {
	if _, err := openEnvelope([]byte("machine:\n  token: bw:ref:x\n")); err == nil {
		t.Fatal("openEnvelope accepted a bare document as a staged change")
	}
}
