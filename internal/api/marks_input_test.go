package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
)

// markTestRoute takes a mark request's body as the marks route does, with an effect that writes
// nothing beyond the act and the record.
func markTestRoute() *route {
	return &route{method: http.MethodPost, pattern: "/test-ingestions/{id}/marks", roles: []auth.Role{auth.Author}, keyed: "marks",
		action: "test.mark", input: func() input { return &markInput{} },
		effect: func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
			return result{status: http.StatusAccepted, body: map[string]string{"request": q.id}}, nil
		}}
}

func postMarks(tok, k, body string) call {
	return call{method: "POST", path: prefix + "/test-ingestions/ing_test/marks", token: tok, key: k, body: body}
}

// PA §9.3: a mark takes 1 to 1024 paths; a path that does not parse is 422 validation-failed
// naming its position, never its text, before any change.
func TestMarkInputRefusals(t *testing.T) {
	const sentinel = "mark-sentinel-6f2b"
	e := newEnvWith(t, deps{ing: &fakeIngester{latest: 1}}, options{extra: []*route{markTestRoute()}})
	tok := e.human("h-author")
	many := `{"marks":["doc[0]/a"` + strings.Repeat(`,"doc[0]/a"`, maxMarks) + `]}`
	for _, body := range []string{`{"marks":[]}`, `{}`, many, `{"marks":null}`, `{"marks":["doc[0]/a",1]}`, `{"marks":[null]}`} {
		wantProblem(t, e.do(e.api, postMarks(tok, key, body)), http.StatusBadRequest, "invalid-request")
	}
	rec := e.do(e.api, postMarks(tok, key, `{"marks":["doc[0]/a","doc[0]/`+sentinel+`~2"]}`))
	p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
	if p["position"] != float64(1) {
		t.Fatalf("position %v, want 1", p["position"])
	}
	if strings.Contains(rec.Body.String(), sentinel) || e.logged(sentinel) {
		t.Fatal("the refused path is quoted in the problem or the log")
	}
	p = wantProblem(t, e.do(e.api, postMarks(tok, key, `{"marks":["nope"]}`)), http.StatusUnprocessableEntity, "validation-failed")
	if p["position"] != float64(0) {
		t.Fatalf("position %v, want 0", p["position"])
	}
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d; want none", acts, records)
	}
}

// PA §9.3: a mark must be the text that was sent. encoding/json turns a lone surrogate escape or
// malformed UTF-8 into U+FFFD, and marks bypass the canonical transform that refuses both
// elsewhere, so each is 422 naming its position, as are U+FFFD and U+0000 written out; a valid
// non-ASCII mark is taken.
func TestMarkInputUnicode(t *testing.T) {
	e := newEnvWith(t, deps{ing: &fakeIngester{latest: 1}}, options{extra: []*route{markTestRoute()}})
	tok := e.human("h-author")
	for name, mark := range map[string]string{
		"lone surrogate": `doc[0]/\ud800`,
		"malformed":      "doc[0]/\xff",
		"replacement":    "doc[0]/�",
		"NUL":            `doc[0]/\u0000`,
	} {
		t.Run(name, func(t *testing.T) {
			p := wantProblem(t, e.do(e.api, postMarks(tok, key, `{"marks":["doc[0]/a","`+mark+`"]}`)),
				http.StatusUnprocessableEntity, "validation-failed")
			if p["position"] != float64(1) {
				t.Fatalf("position %v, want 1", p["position"])
			}
		})
	}
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d; want none", acts, records)
	}
	if rec := e.do(e.api, postMarks(tok, key, `{"marks":["doc[0]/a","doc[0]/hôte"]}`)); rec.Code != http.StatusAccepted {
		t.Fatalf("a valid non-ASCII mark: %d %s", rec.Code, rec.Body)
	}
}

// PA §7.1: a mark request's fingerprint is the digest key's HMAC over the request without its
// marks, then the marks, each length-prefixed; a mark path can spell an extracted value, so an
// unkeyed SHA-256 over the canonical body would let it be guessed offline.
func TestMarkFingerprint(t *testing.T) {
	e := newEnvWith(t, deps{ing: &fakeIngester{latest: 1}}, options{extra: []*route{markTestRoute()}})
	tok := e.human("h-author")
	body := `{"marks":["doc[0]/a/keyed-mark-1","doc[0]/b"]}`
	if rec := e.do(e.api, postMarks(tok, key, body)); rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var fkey string
	var fp []byte
	if err := e.db.QueryRow(`SELECT fingerprint_key, fingerprint FROM idempotency_record`).Scan(&fkey, &fp); err != nil {
		t.Fatal(err)
	}
	if fkey != "transit/bw-digest@v1" {
		t.Fatalf("fingerprint_key %q", fkey)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.SetPathValue("id", "ing_test")
	q := &request{route: markTestRoute(), r: r}
	in := material(q, []byte(`{}`))
	var enc []byte
	for _, m := range []string{"doc[0]/a/keyed-mark-1", "doc[0]/b"} {
		enc = binary.BigEndian.AppendUint64(enc, uint64(len(m)))
		enc = append(enc, m...)
	}
	in = binary.BigEndian.AppendUint64(in, uint64(len(enc)))
	want := hmac.New(sha256.New, []byte("1"))
	want.Write(append(in, enc...))
	if !hmac.Equal(fp, want.Sum(nil)) {
		t.Fatal("the stored fingerprint is not the HMAC of the request without its marks, then the marks")
	}
	// The control: an unkeyed fingerprint over the body with its marks is not what is stored.
	if unkeyed := fingerprint(q, []byte(body)); hmac.Equal(fp, unkeyed) {
		t.Fatal("the fingerprint is an unkeyed SHA-256 of the body")
	}
	if rec := e.do(e.api, postMarks(tok, key, body)); rec.Code != http.StatusAccepted || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	for _, other := range []string{
		`{"marks":["doc[0]/a/keyed-mark-2","doc[0]/b"]}`,
		`{"marks":["doc[0]/b","doc[0]/a/keyed-mark-1"]}`,
	} {
		wantProblem(t, e.do(e.api, postMarks(tok, key, other)), http.StatusUnprocessableEntity, "idempotency-key-reused")
	}
}
