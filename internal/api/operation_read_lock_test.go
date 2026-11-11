package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// holdOperation opens a transaction that holds operation op's row, as a transaction that moves the
// operation does until it commits. The caller ends it.
func holdOperation(t *testing.T, db *sql.DB, op string) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	mustExec(t, tx, `SELECT 1 FROM operation WHERE id = $1 FOR UPDATE`, op)
	return tx
}

// getAsync starts GET path and returns the channel that carries its answer.
func getAsync(e *env, path, token string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.do(e.api, call{method: "GET", path: prefix + path, token: token}) }()
	return done
}

// §8.3 (ginsys/bronzeward#799): GET /operations/{id} of an ingest operation reads no attempts, so
// it answers while another transaction holds the operation's row, and shows the row as committed.
func TestGetIngestOperationAnswersWhileRowHeld(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	d := decode[draftBody](t, e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: author, key: key,
		body: `{"cluster":"` + cl + `","title":"import"}`}), http.StatusCreated)
	var human string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-author'").Scan(&human); err != nil {
		t.Fatal(err)
	}
	m := decode[machineBody](t, e.do(e.api, machineCall(author, "k-machine-0123456789", cl, uuidA)), http.StatusCreated)
	claim, opID := id.New(id.Ingestion), id.New(id.Operation)
	mustExec(t, e.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		principal, idempotency_key, cluster, machine, kind, created_at)
		SELECT $1, 'transient', 'held', 'run/1/start', 1, epoch, now() + interval '1 minute', now() + interval '1 hour',
		$2, 'k-ingest-0123456789', $3, $4, 'import', now() FROM installation_state`, claim, human, cl, m.ID)
	mustExec(t, e.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, last_event,
		draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'ingest', 'running', epoch, 'run/1/start', 1, epoch, now() + interval '1 minute', 2, $2, 1, $3, $4, 'human',
		'author', now() FROM installation_state`, opID, d.ID, claim, human)

	held := holdOperation(t, e.db, opID)
	done := getAsync(e, "/operations/"+opID, e.human("h-viewer"))
	select {
	case rec := <-done:
		if got := decode[operationBody](t, rec, http.StatusOK); got.ID != opID || got.Kind != "ingest" {
			t.Fatalf("operation %s", rec.Body)
		}
	case <-time.After(5 * time.Second):
		_ = held.Rollback()
		<-done
		t.Fatal("the read of an ingest operation waited for a transaction that holds its row")
	}
}

// §8.3 (ginsys/bronzeward#799): a plan operation's read still takes the row lock before it reads
// the attempts, so a transaction that moves the operation and records an attempt commits whole
// before or after the read. The read waits for the holder and answers once it commits.
func TestGetPlanOperationWaitsForRowHeld(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	ce.evidence(t, ce.pid, nil)
	c, err := ce.a.commitPlan(t.Context(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	held := holdOperation(t, ce.db, c.Operation)
	done := getAsync(ce.env, "/operations/"+c.Operation, ce.robot)
	waitForLockWaits(t, ce.db, 1)
	select {
	case rec := <-done:
		t.Fatalf("the read of a plan operation answered %d while its row was held", rec.Code)
	default:
	}
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if got := decode[operationBody](t, rec, http.StatusOK); got.ID != c.Operation || got.Subject == nil || got.Subject.Plan != ce.pid {
			t.Fatalf("operation %s", rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read of a plan operation did not answer after the holder committed")
	}
}
