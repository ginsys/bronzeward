package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/staging"
)

// publisher builds an API over d's database whose process has a provider: an owner and
// publication clients (persistence-api.md §5.1).
func (d *draftEnv) publisher(o options) *API {
	d.t.Helper()
	return d.publisherAs("run-1/4242/publisher", o)
}

// publisherAs is publisher for another process, owner, over the same database, in the current
// epoch. Its lease is a minute and its heartbeat 20 ms.
func (d *draftEnv) publisherAs(owner string, o options) *API {
	d.t.Helper()
	var epoch string
	if err := d.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		d.t.Fatal(err)
	}
	return d.buildWith(deps{owner: staging.Owner{ID: owner, Epoch: epoch}, pub: &publishClients{},
		timers: config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: time.Minute}}, o)
}

func (d *draftEnv) publish(h http.Handler, ifMatch, k, body string) *httptest.ResponseRecorder {
	return d.do(h, call{method: "POST", path: prefix + "/drafts/" + d.draft + "/publications", token: d.robot, key: k,
		ifMatch: ifMatch, body: body})
}

// T2 (persistence-api.md §5, §7.3, §9.3): a publication request queues one publish operation for
// the draft revision it binds, with its first event, and answers it 202 with its Location; a replay
// answers the same, and another key for the same draft revision answers the operation already
// queued.
func TestPublicationRequest(t *testing.T) {
	d := newDraftEnv(t)
	woken := 0
	h := d.publisher(options{onPublish: func() { woken++ }})

	wantProblem(t, d.publish(d.api, d.etag, d.key(), `{}`), http.StatusServiceUnavailable, "dependency-unavailable")
	wantProblem(t, d.publish(h, d.etag, d.key(), `{"title":"x"}`), http.StatusBadRequest, "invalid-request")
	wantProblem(t, d.publish(h, `"1-aaaaaaaaaaaaaaaaaaaaaaaaaa"`, d.key(), `{}`), http.StatusPreconditionFailed, "precondition-failed")
	if n := count(t, d.db, `SELECT count(*) FROM operation WHERE kind = 'publish'`); n != 0 || woken != 0 {
		t.Fatalf("%d operations after refusals, worker woken %d times", n, woken)
	}

	k := d.key()
	rec := d.publish(h, d.etag, k, `{}`)
	op := decode[operationBody](t, rec, http.StatusAccepted)
	if op.Kind != "publish" || op.State != "queued" || op.LastEvent != 1 || op.Subject == nil ||
		op.Subject.Draft != d.draft || op.Subject.DraftRevision != 1 || op.CreatedBy == nil || op.CreatedBy.Role != "publisher" {
		t.Fatalf("publication answered %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != prefix+"/operations/"+op.ID {
		t.Fatalf("Location %q", loc)
	}
	if woken != 1 {
		t.Fatalf("worker woken %d times; want 1", woken)
	}
	var entry string
	if err := d.db.QueryRow(`SELECT entry::text FROM operation_event WHERE operation = $1 AND number = 1 AND kind = 'publish'`,
		op.ID).Scan(&entry); err != nil || entry != `{"type": "queued"}` {
		t.Fatalf("first event %q, %v", entry, err)
	}
	var owner *string
	if err := d.db.QueryRow(`SELECT owner FROM operation WHERE id = $1`, op.ID).Scan(&owner); err != nil || owner != nil {
		t.Fatalf("a queued operation has owner %v, %v", owner, err)
	}

	replay := d.publish(h, d.etag, k, `{}`)
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != rec.Body.String() {
		t.Fatalf("replay %d %s", replay.Code, replay.Body)
	}
	again := d.publish(h, d.etag, d.key(), `{}`)
	if b := decode[operationBody](t, again, http.StatusAccepted); b.ID != op.ID || again.Header().Get("Location") != rec.Header().Get("Location") {
		t.Fatalf("a second key answered %s; want the queued operation", again.Body)
	}
	if n := count(t, d.db, `SELECT count(*) FROM operation WHERE kind = 'publish'`); n != 1 {
		t.Fatalf("%d publish operations", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM operation_event WHERE operation = $1`, op.ID); n != 1 {
		t.Fatalf("%d events after the natural-key answer", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.publish'`); n != 2 {
		t.Fatalf("%d acts; want one per accepted key", n)
	}
	// The draft is now held by its publication (§3.1): an update or a discard names it.
	if doc := wantProblem(t, d.do(h, call{method: "POST", path: prefix + "/drafts/" + d.draft + "/discard", token: d.human("h-author"),
		key: d.key(), ifMatch: d.etag, body: `{}`}), http.StatusConflict, "conflict"); doc["operation"] != op.ID {
		t.Fatalf("discard while queued: %v", doc)
	}
}

// T2 answers a running publication of the draft revision as the existing one, and a published
// draft 409 naming its release; a discarded draft is refused 409.
func TestPublicationRequestStates(t *testing.T) {
	p := newPublishEnv(t)
	h := p.publisher(options{})
	rec := p.publish(h, p.etag, p.key(), `{}`)
	if b := decode[operationBody](t, rec, http.StatusAccepted); b.ID != p.job.op || b.State != "running" {
		t.Fatalf("a running publication: %s", rec.Body)
	}
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v", ref)
	}
	doc := wantProblem(t, p.publish(h, p.etag, p.key(), `{}`), http.StatusConflict, "conflict")
	if doc["release"] != rel {
		t.Fatalf("published draft: %v; want release %s", doc, rel)
	}

	d := newDraftEnv(t)
	hd := d.publisher(options{})
	decode[draftBody](t, d.do(hd, call{method: "POST", path: prefix + "/drafts/" + d.draft + "/discard", token: d.human("h-author"),
		key: d.key(), ifMatch: d.etag, body: `{}`}), http.StatusOK)
	// The state is checked before If-Match, as T1's: the ETag the draft had still names it.
	if doc := wantProblem(t, d.publish(hd, d.etag, d.key(), `{}`), http.StatusConflict, "conflict"); doc["draft"] != d.draft {
		t.Fatalf("discarded draft: %v", doc)
	}
}
