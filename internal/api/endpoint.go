package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// POST /machines/{id}/talos-endpoints replaces a machine's Talos endpoint (persistence-api.md
// §3.3). It is T11 (§5): the key lock, the installation state FOR SHARE, then the machine row FOR
// UPDATE, under which the change, its endpoint-change timeline entry at the next machine revision
// (T7) and the act commit together. Like inventory it is accepted installation-wide in recovery
// mode (§12.2), and it changes no existing plan, whose route is the endpoint it bound.
func endpointReplacement() effectRoute {
	return effectRoute{action: "machine.talos-endpoint", input: func() input { return &endpointInput{} }, effect: replaceTalosEndpoint}
}

type endpointInput struct {
	TalosEndpoint string `json:"talosEndpoint"`
}

func (in *endpointInput) check(*API) error {
	ep, err := talosEndpoint(in.TalosEndpoint)
	if err != nil {
		return err
	}
	in.TalosEndpoint = ep
	return nil
}

// endpointChangeBody is a replacement's answer: the MachineEndpointChange with its timeline
// revision and act (§3.3).
type endpointChangeBody struct {
	Machine          string    `json:"machine"`
	Revision         int64     `json:"revision"`
	PreviousEndpoint string    `json:"previousEndpoint"`
	TalosEndpoint    string    `json:"talosEndpoint"`
	ChangedBy        string    `json:"changedBy"`
	Role             string    `json:"role"`
	Act              string    `json:"act"`
	Epoch            string    `json:"epoch"`
	At               time.Time `json:"at"`
}

func replaceTalosEndpoint(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*endpointInput)
	m := q.r.PathValue("id")
	if id.MustHave(m, id.Machine) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine")
	}
	b := endpointChangeBody{Machine: m, TalosEndpoint: in.TalosEndpoint, ChangedBy: q.principal.ID, Role: string(q.role),
		Act: q.actID, Epoch: q.epoch}
	err := tx.QueryRowContext(ctx, `SELECT talos_endpoint FROM machine WHERE id = $1 FOR UPDATE`, m).Scan(&b.PreviousEndpoint)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", m)
	case err != nil:
		return result{}, err
	case b.PreviousEndpoint == b.TalosEndpoint:
		return result{}, refuse(http.StatusConflict, "conflict", "the machine already has this Talos endpoint").with("machine", m)
	}
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET talos_endpoint = $2, revision_counter = revision_counter + 1
		WHERE id = $1 RETURNING revision_counter`, m, b.TalosEndpoint).Scan(&b.Revision); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'endpoint-change',
			jsonb_build_object('previous', $4::text, 'new', $5::text, 'principal', $6::text, 'role', $7::text), now())
		RETURNING at`, m, b.Revision, b.Epoch, b.PreviousEndpoint, b.TalosEndpoint, b.ChangedBy, b.Role).Scan(&b.At); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_endpoint_change (machine, revision, previous_endpoint, new_endpoint, act)
		VALUES ($1, $2, $3, $4, $5)`, m, b.Revision, b.PreviousEndpoint, b.TalosEndpoint, b.Act); err != nil {
		return result{}, err
	}
	b.At = b.At.UTC()
	return result{status: http.StatusCreated, location: prefix + "/machines/" + m, body: b, subjects: []string{m}}, nil
}
