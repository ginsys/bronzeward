package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

func revoke(e *env, tok, k, body string) *httptest.ResponseRecorder {
	return e.do(e.api, call{method: "POST", path: prefix + "/identity-revocations", token: tok, key: k, body: body})
}

func revocationOf(t *testing.T, rec *httptest.ResponseRecorder) revocationBody {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var b revocationBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRevokeBySubject(t *testing.T) {
	e := newEnv(t, options{})
	b := revocationOf(t, revoke(e, e.human("h-recovery"), key, `{"iss":"`+e.iss.URL+`","sub":"h-author","reason":"left"}`))
	var revoker string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-recovery'").Scan(&revoker); err != nil {
		t.Fatal(err)
	}
	if b.RevokedBy != revoker || b.Role != auth.RecoveryAdmin || b.Reason != "left" || b.DeniedSubjectsListed || b.Note == "" {
		t.Fatalf("body %+v", b)
	}
	if n := count(t, e.db, `SELECT count(*) FROM principal p JOIN identity_revocation r ON r.identity = p.id
		JOIN act a ON a.id = r.act WHERE p.sub = 'h-author' AND p.revoked AND a.role = 'recovery-admin' AND a.via = 'api'
		AND a.principal = $1 AND a.subjects = ARRAY[p.id] AND a.action = 'identity.revoke'`, revoker); n != 1 {
		t.Fatal("revocation row, revoked flag and act do not agree")
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: e.human("h-author")}), http.StatusForbidden, "identity-revoked")
}

// Scope ruling 6: the operator lists the subject first (§10.4), and the revocation is still recorded.
func TestRevokeDeniedNeverSeenSubject(t *testing.T) {
	e := newEnv(t, options{})
	e.cfg.DeniedSubjects = []config.DeniedSubject{{Iss: e.iss.URL, Sub: "h-author"}}
	e.api = e.build(options{})
	if b := revocationOf(t, revoke(e, e.human("h-recovery"), key, `{"iss":"`+e.iss.URL+`","sub":"h-author","reason":"left"}`)); !b.DeniedSubjectsListed {
		t.Fatalf("body %+v", b)
	}
}

func TestRevokeByIdentity(t *testing.T) {
	e := newEnv(t, options{})
	revocationOf(t, revoke(e, e.human("h-recovery"), key, `{"identity":"`+e.robotID+`","reason":"retired"}`))
	if n := count(t, e.db, "SELECT count(*) FROM automation_token WHERE owner = $1 AND revoked_at IS NULL", e.robotID); n != 0 {
		t.Fatal("the service identity's token survived its revocation")
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: e.robot}), http.StatusForbidden, "identity-revoked")
}

func TestRevocationRefusals(t *testing.T) {
	e := newEnv(t, options{})
	rec := e.human("h-recovery")
	for name, b := range map[string]string{
		"neither form":    `{"reason":"x"}`,
		"both forms":      `{"identity":"` + e.robotID + `","iss":"` + e.iss.URL + `","sub":"x","reason":"x"}`,
		"bad identity":    `{"identity":"tok_abc","reason":"x"}`,
		"other issuer":    `{"iss":"https://other.example","sub":"x","reason":"x"}`,
		"blank reason":    `{"identity":"` + e.robotID + `","reason":"  "}`,
		"reason too long": `{"identity":"` + e.robotID + `","reason":"` + strings.Repeat("r", maxReason+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) { wantProblem(t, revoke(e, rec, key, b), http.StatusBadRequest, "invalid-request") })
	}
	wantProblem(t, revoke(e, rec, "k-unknown-0123456789", `{"identity":"`+id.New(id.Principal)+`","reason":"x"}`), http.StatusNotFound, "not-found")
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-recovery'"); n != 1 {
		t.Fatal("the revoker's first-use row did not stay after a refusal inside the transaction (§10)")
	}
	revocationOf(t, revoke(e, rec, "k-first-0123456789", `{"identity":"`+e.robotID+`","reason":"x"}`))
	wantProblem(t, revoke(e, rec, "k-second-012345678", `{"identity":"`+e.robotID+`","reason":"x"}`), http.StatusConflict, "conflict")
	wantProblem(t, revoke(e, e.human("h-approver"), key, `{"identity":"`+e.robotID+`","reason":"x"}`), http.StatusForbidden, "forbidden")
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 1 {
		t.Fatalf("%d api acts; want the one committed revocation", n)
	}
}

// Rule 2: the revoking human is locked FOR SHARE and checked again; revoked meanwhile, it is refused.
func TestRevokerRevokedMeanwhile(t *testing.T) { revokerRevokedMeanwhile(t, false) }

// Control: without that lock the revocation commits by an identity revoked before its commit.
func TestRevokerRevokedMeanwhileNoLockControl(t *testing.T) { revokerRevokedMeanwhile(t, true) }

func revokerRevokedMeanwhile(t *testing.T, control bool) {
	e := newEnv(t, options{})
	e.api = e.build(options{noRevokerCheck: control})
	e.recordHuman("h-recovery")
	var revoker string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-recovery'").Scan(&revoker); err != nil {
		t.Fatal(err)
	}
	tok := e.human("h-recovery")
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE principal SET revoked = true WHERE id = $1", revoker); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- revoke(e, tok, key, `{"identity":"`+e.robotID+`","reason":"x"}`) }()
	if control {
		rec := <-done // nothing blocks it
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusCreated {
			t.Fatalf("control: %d %s; want the unguarded revocation to commit", rec.Code, rec.Body)
		}
		return
	}
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	wantProblem(t, rec, http.StatusForbidden, "identity-revoked")
	// §9.1: a 403 identity-revoked carries no epoch, even when the revocation is found in the
	// transaction, after the headers were set.
	if rec.Header().Get("Bronzeward-Epoch") != "" || rec.Header().Get("Bronzeward-Recovery-Mode") != "" {
		t.Fatalf("a revoked identity was told the epoch: %v", rec.Header())
	}
	if n := count(t, e.db, "SELECT count(*) FROM identity_revocation"); n != 0 {
		t.Fatal("a revoked identity recorded a revocation")
	}
}

// Rule 2 when the revoking human names itself: revoked meanwhile, it is 403 identity-revoked with
// no epoch, not 409 conflict. Control: revoking itself while unrevoked commits.
func TestSelfRevokerRevokedMeanwhile(t *testing.T) {
	e := newEnv(t, options{})
	e.recordHuman("h-recovery")
	var revoker string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-recovery'").Scan(&revoker); err != nil {
		t.Fatal(err)
	}
	tok := e.human("h-recovery")
	body := `{"identity":"` + revoker + `","reason":"x"}`
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE principal SET revoked = true WHERE id = $1", revoker); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- revoke(e, tok, key, body) }()
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	wantProblem(t, rec, http.StatusForbidden, "identity-revoked")
	if rec.Header().Get("Bronzeward-Epoch") != "" {
		t.Fatalf("a revoked identity was told the epoch: %v", rec.Header())
	}
	if n := count(t, e.db, "SELECT count(*) FROM identity_revocation"); n != 0 {
		t.Fatal("a revoked identity recorded a revocation")
	}

	c := newEnv(t, options{})
	c.recordHuman("h-recovery")
	if err := c.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-recovery'").Scan(&revoker); err != nil {
		t.Fatal(err)
	}
	revocationOf(t, revoke(c, c.human("h-recovery"), key, `{"identity":"`+revoker+`","reason":"x"}`))
}

// §7.2 on T5c: a same-key duplicate in flight replays the first revocation.
func TestRevocationSameKeyInFlight(t *testing.T) {
	hook, held, release := holdFirst()
	e := newEnv(t, options{afterEffect: hook})
	t.Cleanup(release)
	e.recordHuman("h-recovery")
	tok := e.human("h-recovery")
	body := `{"identity":"` + e.robotID + `","reason":"x"}`
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- revoke(e, tok, key, body) }()
	<-held
	go func() { b <- revoke(e, tok, key, body) }()
	dbtest.WaitForLockWait(t, e.db)
	release()
	ra, rb := <-a, <-b
	if ra.Code != http.StatusCreated || rb.Code != http.StatusCreated || rb.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("first %d %s; second %d %v %s", ra.Code, ra.Body, rb.Code, rb.Header(), rb.Body)
	}
}
