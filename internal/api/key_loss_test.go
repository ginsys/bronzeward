package api

import (
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// PA §7.1 and §14: loss of the old fingerprint key does not make a new request
// replay an uncomparable refusal, nor let it duplicate an active ingestion.
func TestNewKeyAfterDigestKeyLoss(t *testing.T) {
	loseOldKey := func(ie *ingestEnv) {
		ie.f.mu.Lock()
		defer ie.f.mu.Unlock()
		ie.f.latest = 2
		ie.f.errAt = map[int]error{1: provider.ErrDenied}
	}
	t.Run("refused draft entry", func(t *testing.T) {
		ie := newFragmentEnv(t, options{})
		body := fragmentPut(t, "cluster", labelDoc, []string{"doc[0]/machine/nodeLabels/absent"}, nil)
		first := wantProblem(t, ie.putFragment("registries", body, ie.etag, "k-old-key-refusal-0123"),
			http.StatusUnprocessableEntity, "validation-failed")
		loseOldKey(ie)
		wantProblem(t, ie.putFragment("registries", body, ie.etag, "k-old-key-refusal-0123"),
			http.StatusUnprocessableEntity, "idempotency-key-reused")
		if n := count(t, ie.db, `SELECT count(*) FROM staging_claim`); n != 1 {
			t.Fatal("uncomparable retry created another claim")
		}
		r := ie.putFragment("registries", body, ie.etag, "k-new-key-refusal-0123")
		second := wantProblem(t, r, http.StatusUnprocessableEntity, "validation-failed")
		if second["rule"] != "mark-unaddressed" || second["ingestion"] == first["ingestion"] || r.Header().Get("Idempotent-Replayed") != "" {
			t.Fatal("new key did not ingest and refuse afresh")
		}
		if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE state = 'abandoned' AND payload IS NULL`); n != 2 {
			t.Fatal("refused entries did not leave two abandoned claims without payload")
		}
		if n := count(t, ie.db, `SELECT count(*) FROM idempotency_record WHERE key IN ('k-old-key-refusal-0123', 'k-new-key-refusal-0123')`); n != 2 {
			t.Fatal("each refusal must retain its own idempotency record")
		}
		if calls, _ := ie.f.paths(); calls != 0 || ie.currentETag(t) != ie.etag || count(t, ie.db, `SELECT count(*) FROM fragment_revision`) != 0 {
			t.Fatal("refused input changed the draft or created a generation")
		}
		assertAbsent(t, ie, runLabel)
	})
	t.Run("active ingestion", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		author := ie.human("h-author")
		first := ie.do(ie.api, ingestCall(author, "k-old-key-active-01234", ie.etag, ie.body(nil)))
		if first.Code != http.StatusAccepted {
			t.Fatal("initial ingestion was not accepted")
		}
		loseOldKey(ie)
		wantProblem(t, ie.do(ie.api, ingestCall(author, "k-old-key-active-01234", ie.etag, ie.body(nil))),
			http.StatusUnprocessableEntity, "idempotency-key-reused")
		p := wantProblem(t, ie.do(ie.api, ingestCall(author, "k-new-key-active-01234", ie.etag, ie.body(nil))),
			http.StatusConflict, "conflict")
		if op, ok := p["operation"].(string); !ok || first.Header().Get("Location") != prefix+"/operations/"+op {
			t.Fatal("new-key conflict did not name the original operation")
		}
		if claims, ops := ie.rowCounts(t); claims != 1 || ops != 1 || len(ie.runs()) != 1 {
			t.Fatal("new key duplicated the active ingestion")
		}
	})
}
