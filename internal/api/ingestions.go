package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/staging"
)

// POST /ingestions starts an import (persistence-api.md §8, §9.2). It is T11 (§5): the key lock,
// the installation state FOR SHARE, the draft FOR UPDATE, then the staging claim held and its
// ingest operation running, together, only if this process's epoch is the current one (§5.1).
// Its fingerprint is keyed (§7.1): the document is HMACed, never stored. The runner takes the
// job after the COMMIT.
func ingestionStart() effectRoute {
	return effectRoute{action: "ingestion.start", input: func() input { return &ingestionInput{} }, effect: startIngestion,
		keyed: "document"}
}

// maxMarks bounds a request's marks; every mark is a path into the document.
const maxMarks = 1024

type ingestionInput struct {
	Kind         string              `json:"kind"`
	Machine      string              `json:"machine"`
	Draft        string              `json:"draft"`
	Source       string              `json:"source"`
	Staging      string              `json:"staging"`
	Marks        []string            `json:"marks"`
	Document     ingest.Unresolved   `json:"document"`
	Declarations ingest.Declarations `json:"declarations"`
	marks        []ingest.Path
}

func (in *ingestionInput) check(*API) error {
	switch {
	case in.Kind == "drift-adoption":
		return errors.New("kind drift-adoption is not served yet")
	case in.Kind != "import":
		return errors.New("kind must be import")
	case in.Source == "machine":
		return errors.New("source machine is not served yet")
	case in.Source != "document":
		return errors.New("source must be machine or document")
	case in.Staging != "transient" && in.Staging != "encrypted":
		return errors.New("staging must be transient or encrypted")
	case id.MustHave(in.Machine, id.Machine) != nil:
		return errors.New("machine must be an mch identifier")
	case id.MustHave(in.Draft, id.Draft) != nil:
		return errors.New("draft must be a drf identifier")
	case in.Document.Size() == 0:
		return errors.New("document is required with source document")
	case len(in.Marks) > maxMarks:
		return fmt.Errorf("at most %d marks", maxMarks)
	}
	in.marks = make([]ingest.Path, len(in.Marks))
	for i, s := range in.Marks {
		p, err := ingest.ParsePath(s)
		if err != nil {
			return fmt.Errorf("marks[%d] is not a compilation §2.2 path", i)
		}
		in.marks[i] = p
	}
	return nil
}

func (in *ingestionInput) document() ingest.Unresolved { return in.Document }

// job is one ingestion as the runner takes it from T11: the claim at its generation, the
// operation, the draft revision it binds and the request's input, held in memory only.
type job struct {
	claim    staging.Claim
	op       string
	draft    string
	draftRev int
	marks    []ingest.Path
	decl     ingest.Declarations
	input    ingest.Unresolved
}

func startIngestion(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*ingestionInput)
	if a.d.owner.ID == "" {
		return result{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "ingestion is not configured; nothing was committed")
	}
	// Rule 5: machine rows before the draft. A machine's cluster never changes, and the claim's
	// key on (machine, cluster) holds the row.
	var machineCluster string
	switch err := tx.QueryRowContext(ctx, `SELECT cluster FROM machine WHERE id = $1`, in.Machine).Scan(&machineCluster); {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", in.Machine)
	case err != nil:
		return result{}, err
	}
	var cluster, state, token string
	var rev int
	switch err := tx.QueryRowContext(ctx, `SELECT cluster, state, revision, etag_token FROM draft WHERE id = $1 FOR UPDATE`,
		in.Draft).Scan(&cluster, &state, &rev, &token); {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such draft").with("draft", in.Draft)
	case err != nil:
		return result{}, err
	case state != "open":
		return result{}, refuse(http.StatusConflict, "conflict", "the draft is not open").with("draft", in.Draft)
	case etag(rev, token) != q.ifMatch:
		return result{}, refuse(http.StatusPreconditionFailed, "precondition-failed", "the draft has moved").with("draft", in.Draft)
	case machineCluster != cluster:
		return result{}, refuse(http.StatusUnprocessableEntity, "validation-failed", "the machine is not in the draft's cluster").
			with("machine", in.Machine).with("draft", in.Draft)
	}
	// §7.3's natural key, under the draft's lock: one running ingest per draft revision.
	var running string
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation
		WHERE kind = 'ingest' AND state = 'running' AND draft = $1 AND draft_revision = $2`, in.Draft, rev).Scan(&running); {
	case err == nil:
		return result{}, refuse(http.StatusConflict, "conflict", "an ingestion of this draft revision is running").
			with("operation", running)
	case !errors.Is(err, sql.ErrNoRows):
		return result{}, err
	}
	c := staging.Claim{ID: id.New(id.Ingestion), Mode: in.Staging, Cluster: cluster, Machine: in.Machine, Gen: 1}
	timers := staging.Timers{Lease: a.d.timers.Lease, AbsoluteExpiry: a.d.timers.AbsoluteExpiry}
	if err := staging.Create(ctx, tx, a.d.owner, timers, c, q.principal.ID, q.key); err != nil {
		return result{}, err
	}
	// The operation is the claim's, one to one: its owner, generation, epoch and lease (§5.1).
	op := id.New(id.Operation)
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until,
		last_event, draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'ingest', 'running', $2, owner, owner_gen, owner_epoch, lease_until, 1, $3, $4, id, $5, $6, $7, now()
		FROM staging_claim WHERE id = $8`,
		op, q.epoch, in.Draft, rev, q.principal.ID, string(q.principal.Kind), string(q.role), c.ID); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		VALUES ($1, 1, $2, 'ingest', '{"type": "started"}', now())`, op, q.epoch); err != nil {
		return result{}, err
	}
	j := job{claim: c, op: op, draft: in.Draft, draftRev: rev, marks: in.marks, decl: in.Declarations, input: in.Document}
	return result{status: http.StatusAccepted, location: prefix + "/operations/" + op,
		body: map[string]string{"operation": op, "ingestion": c.ID}, subjects: []string{op, c.ID, in.Draft, in.Machine},
		operation: op, afterCommit: func() { a.startRunner(j) }}, nil
}

// startRunner hands j to the runner, after T11's COMMIT and outside any transaction.
func (a *API) startRunner(j job) {
	if a.o.onRunner != nil {
		a.o.onRunner(j)
	}
}
