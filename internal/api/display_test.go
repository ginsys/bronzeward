package api

import (
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/dbtest"
)

// humanWith returns a valid access token for one of issuer.Humans carrying claims besides its own.
func (e *env) humanWith(name string, claims map[string]any) string {
	e.t.Helper()
	now := time.Now()
	c := map[string]any{"iss": e.iss.URL, "aud": "bronzeward", "sub": name, "groups": issuer.Humans[name], "jti": issuer.JTI(),
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
	maps.Copy(c, claims)
	tok, err := e.iss.Sign(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// displayOf is the display name GET /principals serves for identity, "" for null.
func displayOf(t *testing.T, e *env, a *API, identity string) string {
	t.Helper()
	got := readPrincipals(t, e, a, e.human("h-viewer"), identity)
	if got[0].Display == nil {
		return ""
	}
	return *got[0].Display
}

// subjectID is the principal identifier of the human sub.
func subjectID(t *testing.T, db *sql.DB, sub string) string {
	t.Helper()
	var idn string
	if err := db.QueryRow(`SELECT id FROM principal WHERE kind = 'human' AND sub = $1`, sub).Scan(&idn); err != nil {
		t.Fatal(err)
	}
	return idn
}

// §10, choice §17.40 (#335): a human's display follows the default claim, name, else
// preferred_username, across its admitted mutating requests; a token without it, with an empty or
// non-string one, and a read leave it as it is; a service identity and a row the token tool
// created have none.
func TestPrincipalsDisplay(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	n := 0
	write := func(claims map[string]any) {
		t.Helper()
		n++
		if rec := e.do(e.api, post(e.humanWith("h-author", claims), fmt.Sprintf("k-display-%02d-0123456789", n), `{"note":"a"}`)); rec.Code != http.StatusCreated {
			t.Fatalf("write %d: %d %s", n, rec.Code, rec.Body)
		}
	}
	want := func(step, display string) {
		t.Helper()
		if got := displayOf(t, e, e.api, subjectID(t, e.db, "h-author")); got != display {
			t.Fatalf("%s: display %q; want %q", step, got, display)
		}
	}
	write(map[string]any{"name": "BWSYNTH Ann"})
	want("the first request, which creates the row", "BWSYNTH Ann")
	write(map[string]any{"name": "BWSYNTH Ann Other", "preferred_username": "bwsynth-ann"})
	want("a changed name", "BWSYNTH Ann Other")
	write(nil)
	want("a token without the claim", "BWSYNTH Ann Other")
	write(map[string]any{"name": ""})
	want("an empty claim", "BWSYNTH Ann Other")
	write(map[string]any{"name": 42})
	want("a claim that is not a string", "BWSYNTH Ann Other")
	if rec := e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: e.humanWith("h-author", map[string]any{"name": "BWSYNTH Read"})}); rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	want("a read", "BWSYNTH Ann Other")
	write(map[string]any{"preferred_username": "bwsynth-ann"})
	want("preferred_username without name", "bwsynth-ann")

	// The token tool created h-all's row with the service identity's; the service has no display,
	// and its own requests write none.
	if rec := e.do(e.api, post(e.robot, "k-display-robot-0123456789", `{"note":"a"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("robot write: %d %s", rec.Code, rec.Body)
	}
	for _, idn := range []string{subjectID(t, e.db, "h-all"), e.robotID} {
		if got := displayOf(t, e, e.api, idn); got != "" {
			t.Fatalf("%s: display %q; want null", idn, got)
		}
	}
}

// §10: auth.oidc.displayClaim names the one claim read; the default claims then count for nothing.
func TestPrincipalsDisplayConfiguredClaim(t *testing.T) {
	e := newEnv(t, options{})
	e.cfg.OIDC.DisplayClaim = "nickname"
	a := e.build(options{extra: []*route{testRoute(nil)}})
	for i, c := range []struct {
		claims map[string]any
		want   string
	}{
		{map[string]any{"name": "BWSYNTH Ann", "nickname": "BWSYNTH Nick"}, "BWSYNTH Nick"},
		{map[string]any{"name": "BWSYNTH Ann", "preferred_username": "bwsynth-ann"}, "BWSYNTH Nick"},
		{map[string]any{"nickname": "BWSYNTH Nick Other"}, "BWSYNTH Nick Other"},
	} {
		if rec := e.do(a, post(e.humanWith("h-author", c.claims), fmt.Sprintf("k-claim-%02d-0123456789", i), `{"note":"a"}`)); rec.Code != http.StatusCreated {
			t.Fatalf("write %d: %d %s", i, rec.Code, rec.Body)
		}
		if got := displayOf(t, e, a, subjectID(t, e.db, "h-author")); got != c.want {
			t.Fatalf("write %d: display %q; want %q", i, got, c.want)
		}
	}
}

// §10: the refresh locks the row FOR NO KEY UPDATE, and only when the name changes. It passes a
// transaction holding the row FOR KEY SHARE, as every mutating request may (§5 rule 5); while one
// holds it FOR SHARE, as an approval does, a request carrying the stored name goes through and one
// carrying a new name waits for it. (FOR UPDATE would also hold off the act's foreign key.)
func TestPrincipalsDisplayLocks(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	e.recordHuman("h-author")
	idn := subjectID(t, e.db, "h-author")
	n := 0
	send := func(name string) <-chan *httptest.ResponseRecorder {
		n++
		c := post(e.humanWith("h-author", map[string]any{"name": name}), fmt.Sprintf("k-lock-%02d-0123456789", n), `{"note":"a"}`)
		ch := make(chan *httptest.ResponseRecorder, 1)
		go func() { ch <- e.do(e.api, c) }()
		return ch
	}
	done := func(step string, ch <-chan *httptest.ResponseRecorder) {
		t.Helper()
		select {
		case rec := <-ch:
			if rec.Code != http.StatusCreated {
				t.Fatalf("%s: %d %s", step, rec.Code, rec.Body)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: still waiting after 10s", step)
		}
	}
	hold := func(lock string) *sql.Tx {
		t.Helper()
		tx, err := e.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		if _, err := tx.Exec(`SELECT 1 FROM principal WHERE id = $1 FOR `+lock, idn); err != nil {
			t.Fatal(err)
		}
		return tx
	}

	share := hold("KEY SHARE")
	done("a new name past FOR KEY SHARE", send("BWSYNTH Ann"))
	_ = share.Rollback()

	shared := hold("SHARE")
	done("the stored name past FOR SHARE", send("BWSYNTH Ann"))
	waiting := send("BWSYNTH Ann Other")
	dbtest.WaitForLockWait(t, e.db)
	select {
	case rec := <-waiting:
		t.Fatalf("a new name did not wait for FOR SHARE: %d %s", rec.Code, rec.Body)
	default:
	}
	_ = shared.Rollback()
	done("a new name after FOR SHARE is released", waiting)
	if got := displayOf(t, e, e.api, idn); got != "BWSYNTH Ann Other" {
		t.Fatalf("display %q after the wait", got)
	}
}
