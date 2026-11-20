package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/id"
)

// A machine's name (persistence-api.md §3.5): an operator's label, 1 to 63 characters of lowercase
// a-z, digits and the hyphen, neither the first nor the last a hyphen, so that it is always a valid
// DNS hostname label and never reads as an identifier. Uppercase is refused, never folded.
var nameShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// machineName checks a name's grammar. The error never quotes the value.
func machineName(s string) error {
	if !nameShape.MatchString(s) {
		return errors.New("name must be 1 to 63 characters of lowercase a-z, digits and the hyphen, neither the first nor the last a hyphen (persistence-api.md §3.5)")
	}
	return nil
}

// nameInUse is the refusal of a name another machine of the cluster holds, naming that machine (§9.4).
// It is a code of its own: `conflict` names a machine with the same identity key, and a client that
// adopts that machine must not read a name clash as one.
func nameInUse(holder string) *refusal {
	return refuse(http.StatusConflict, "name-in-use", "another machine of the cluster holds this name").with("machine", holder)
}

// POST /machines/{id}/names replaces a machine's name (§3.5). It is T11 (§5): the key lock, the
// installation state FOR SHARE, then the machine row FOR UPDATE, under which the change, its
// name-change timeline entry at the next machine revision (T7) and the act commit together. Like the
// endpoint route it is accepted installation-wide in recovery mode (§12.2) and changes no plan.
func nameReplacement() effectRoute {
	return effectRoute{action: "machine.name", input: func() input { return &nameInput{} }, effect: replaceMachineName}
}

type nameInput struct {
	Name string `json:"name"`
}

func (in *nameInput) check(*API) error { return machineName(in.Name) }

// nameChangeBody is a rename's answer: the machine with its timeline revision and act (§3.5).
type nameChangeBody struct {
	Machine      string    `json:"machine"`
	Revision     int64     `json:"revision"`
	PreviousName *string   `json:"previousName"`
	Name         string    `json:"name"`
	ChangedBy    string    `json:"changedBy"`
	Role         string    `json:"role"`
	Act          string    `json:"act"`
	Epoch        string    `json:"epoch"`
	At           time.Time `json:"at"`
}

// nameClash reports the machine_cluster_name index refusing a name (23505).
func nameClash(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "machine_cluster_name"
}

// nameHolder is the machine of the cluster that holds name, or "" when none does: the holder that
// stopped a statement can have renamed away before this read, under read committed.
func nameHolder(ctx context.Context, tx *sql.Tx, cluster, name string) (string, error) {
	var holder string
	err := tx.QueryRowContext(ctx, `SELECT id FROM machine WHERE cluster = $1 AND name = $2`, cluster, name).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return holder, err
}

// nameRace answers a refused name whose holder renamed away before it could be read: nothing
// committed, and the request can be repeated.
func nameRace() *refusal {
	return refuse(http.StatusServiceUnavailable, "transient-conflict", "the name's holder renamed away while the request ran; "+notCommitted)
}

func replaceMachineName(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*nameInput)
	m := q.r.PathValue("id")
	if id.MustHave(m, id.Machine) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine")
	}
	b := nameChangeBody{Machine: m, Name: in.Name, ChangedBy: q.principal.ID, Role: string(q.role), Act: q.actID, Epoch: q.epoch}
	var cluster string
	err := tx.QueryRowContext(ctx, `SELECT cluster, name FROM machine WHERE id = $1 FOR UPDATE`, m).Scan(&cluster, &b.PreviousName)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", m)
	case err != nil:
		return result{}, err
	case b.PreviousName != nil && *b.PreviousName == b.Name:
		return result{}, refuse(http.StatusConflict, "conflict", "the machine already has this name").with("machine", m)
	}
	// The unique index (cluster, name) is the arbiter of two renames to one name: the second waits
	// for the first's commit and then fails here. The failed statement is undone by the savepoint, so
	// the transaction can still read the holder it names.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT rename`); err != nil {
		return result{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE machine SET name = $2, revision_counter = revision_counter + 1
		WHERE id = $1 RETURNING revision_counter`, m, b.Name).Scan(&b.Revision)
	if nameClash(err) {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT rename`); err != nil {
			return result{}, err
		}
		holder, err := nameHolder(ctx, tx, cluster, b.Name)
		if err != nil {
			return result{}, err
		}
		if holder == "" {
			return result{}, nameRace()
		}
		return result{}, nameInUse(holder)
	}
	if err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT rename`); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'name-change',
			jsonb_build_object('previous', $4::text, 'new', $5::text, 'principal', $6::text, 'role', $7::text), now())
		RETURNING at`, m, b.Revision, b.Epoch, b.PreviousName, b.Name, b.ChangedBy, b.Role).Scan(&b.At); err != nil {
		return result{}, err
	}
	b.At = b.At.UTC()
	return result{status: http.StatusCreated, location: prefix + "/machines/" + m, body: b, subjects: []string{m}}, nil
}
