package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/staging"
)

// POST /ingestions starts an import (persistence-api.md §8, §9.2). It is T11 (§5): the key lock,
// the installation state FOR SHARE, the draft FOR UPDATE, a due claim of the draft revision's
// running operation written abandoned, then the staging claim held and its ingest operation
// running, together, only if this process's epoch is the current one (§5.1).
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
	case len(in.Declarations.References) > 0:
		return errors.New("declarations.references is not served yet")
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
	// §7.3's natural key, under the draft's lock: one running ingest per draft revision. A running
	// operation whose claim is due is abandoned here first, as the sweep would: a late sweep
	// delays only the clearing of ciphertext, never a start (compilation §3.5). A claim another
	// transaction holds is not waited for: T1 takes its claim before this draft.
	var running, claim string
	switch err := tx.QueryRowContext(ctx, `SELECT id, ingestion FROM operation
		WHERE kind = 'ingest' AND state = 'running' AND draft = $1 AND draft_revision = $2`, in.Draft, rev).Scan(&running, &claim); {
	case err == nil:
		abandoned, err := staging.AbandonDueNoWait(ctx, tx, claim, q.epoch)
		if err != nil {
			return result{}, err
		}
		if !abandoned {
			return result{}, refuse(http.StatusConflict, "conflict", "an ingestion of this draft revision is running").
				with("operation", running)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return result{}, err
	}
	c := staging.Claim{ID: id.New(id.Ingestion), Mode: in.Staging, Cluster: cluster, Machine: in.Machine, Gen: 1}
	timers := staging.Timers{Lease: a.d.timers.Lease, AbsoluteExpiry: a.d.timers.AbsoluteExpiry}
	owner := a.d.owner
	if a.o.noEpochTerm { // the control: the process takes the current epoch as its own, so the term always holds
		owner.Epoch = q.epoch
	}
	if err := staging.Create(ctx, tx, owner, timers, c, q.principal.ID, q.key); err != nil {
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

// ingestionBody is the ingestion resource (§9.2): its staging claim as every read treats it
// (compilation §3.5) and its ingest operation. The owner string, the payload and its digest are
// never answered.
type ingestionBody struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Mode            string    `json:"mode"`
	State           string    `json:"state"`
	Machine         string    `json:"machine"`
	Draft           string    `json:"draft"`
	Operation       string    `json:"operation"`
	OwnerGeneration int64     `json:"ownerGeneration"`
	LeaseUntil      time.Time `json:"leaseUntil"`
	ExpiresAt       time.Time `json:"expiresAt"`
	CreatedAt       time.Time `json:"createdAt"`
}

const selectIngestion = `SELECT c.id, c.kind, c.mode, c.state, c.machine, o.draft, o.id, c.owner_gen, c.lease_until, c.expires_at,
		c.created_at
	FROM (SELECT id, kind, mode, ` + staging.EffectiveStateSQL + ` AS state, machine, owner_gen, lease_until, expires_at, created_at
		FROM staging_claim) c
	JOIN operation o ON o.ingestion = c.id
	WHERE c.id = $1`

func readIngestion(ctx context.Context, tx *sql.Tx, claim string) (ingestionBody, error) {
	var b ingestionBody
	err := tx.QueryRowContext(ctx, selectIngestion, claim).Scan(&b.ID, &b.Kind, &b.Mode, &b.State, &b.Machine, &b.Draft, &b.Operation,
		&b.OwnerGeneration, &b.LeaseUntil, &b.ExpiresAt, &b.CreatedAt)
	return b, err
}

var getIngestion = item(id.Ingestion, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	b, err := readIngestion(ctx, tx, v)
	return "", b, err
})

// POST /ingestions/{id}/abandonments is an operator's abandonment (§9.2, compilation §3.2), T11:
// a claim live as read is written abandoned with its payload cleared, and its running ingest
// operation fails ingestion-abandoned with its terminal event (§8.2). A claim a read already
// treats as abandoned has ended: it refuses 409 and is left for the sweep (compilation §3.5). It is not owner-fenced: a runner still holding the claim is refused at its next
// statement. The claim is locked before its operation, as every claim transaction takes them.
func ingestionAbandonment() effectRoute {
	return effectRoute{action: "ingestion.abandon", input: func() input { return &abandonInput{} }, effect: abandonIngestion}
}

type abandonInput struct{}

func (*abandonInput) check(*API) error { return nil }

func abandonIngestion(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	claim := q.r.PathValue("id")
	notFound := refuse(http.StatusNotFound, "not-found", "no such ingestion")
	if id.MustHave(claim, id.Ingestion) != nil {
		return result{}, notFound
	}
	var live bool
	// The state as read (compilation §3.5): a claim a read treats as abandoned has ended, and the
	// sweep writes it.
	switch err := tx.QueryRowContext(ctx, `SELECT `+staging.EffectiveStateSQL+` IN ('held', 'resumed') FROM staging_claim
		WHERE id = $1 FOR UPDATE`, claim).Scan(&live); {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, notFound
	case err != nil:
		return result{}, err
	case !live:
		return result{}, refuse(http.StatusConflict, "conflict", "the ingestion has ended").with("ingestion", claim)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE staging_claim SET state = 'abandoned', payload = NULL, payload_digest = NULL
		WHERE id = $1`, claim); err != nil {
		return result{}, err
	}
	var op string
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE ingestion = $1 AND state = 'running' FOR UPDATE`,
		claim).Scan(&op); {
	case errors.Is(err, sql.ErrNoRows): // its operation has ended already: the claim alone is written
	case err != nil:
		return result{}, err
	default:
		doc := jsonOrNull(problemDoc(op, refuse(http.StatusConflict, "ingestion-abandoned", "an operator abandoned the ingestion")))
		var n int
		if err := tx.QueryRowContext(ctx, `UPDATE operation SET state = 'failed', error = $2::jsonb, owner = NULL, owner_epoch = NULL,
			lease_until = NULL, last_event = last_event + 1 WHERE id = $1 RETURNING last_event`, op, doc).Scan(&n); err != nil {
			return result{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'ingest', '{"type": "failed", "code": "ingestion-abandoned"}', now())`, op, n, q.epoch); err != nil {
			return result{}, err
		}
	}
	b, err := readIngestion(ctx, tx, claim)
	if err != nil {
		return result{}, err
	}
	return result{status: http.StatusOK, body: b, subjects: []string{claim, b.Operation, b.Draft, b.Machine}}, nil
}
