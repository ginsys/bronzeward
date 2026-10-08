package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// observationBody is one observation as GET /machines/{id}/observations answers it
// (execution-recovery.md §4.1): its start, recorded before the read, and what the read returned.
// A value the observation did not read is null and named in unread with its cause. It answers no
// configuration digest, as a plan answers none (persistence-api.md §9.1).
type observationBody struct {
	ID                 string                 `json:"id"`
	Machine            string                 `json:"machine"`
	Basis              int64                  `json:"basis"`
	Revision           int64                  `json:"revision"`
	Purpose            string                 `json:"purpose"`
	Plan               *string                `json:"plan"`
	Operation          *string                `json:"operation"`
	Endpoint           string                 `json:"endpoint"`
	Controller         string                 `json:"controller"`
	StartedAt          time.Time              `json:"startedAt"`
	At                 time.Time              `json:"at"`
	Access             *accessVersionBody     `json:"access"`
	SMBIOSUUID         *string                `json:"smbiosUuid"`
	TalosNodeID        *string                `json:"talosNodeId"`
	TalosClusterID     *string                `json:"talosClusterId"`
	AssignmentEvidence *string                `json:"assignmentEvidence"`
	RunningVersion     *string                `json:"runningVersion"`
	ResourceVersion    *string                `json:"resourceVersion"`
	Health             json.RawMessage        `json:"health"`
	Unread             map[string]unreadValue `json:"unread"`
}

// accessVersionBody is the Talos access version an observation read (persistence-api.md §3.3):
// its provider path, version and creation time, never the talosconfig.
type accessVersionBody struct {
	Path    string    `json:"path"`
	Version int       `json:"version"`
	Created time.Time `json:"created"`
}

// machineNamed refuses with 404 unless v is a machine the database holds.
func machineNamed(ctx context.Context, tx *sql.Tx, v string) error {
	notFound := refuse(http.StatusNotFound, "not-found", "no such resource")
	if id.MustHave(v, id.Machine) != nil {
		return notFound
	}
	err := tx.QueryRowContext(ctx, `SELECT id FROM machine WHERE id = $1`, v).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	return err
}

// listObservations is GET /machines/{id}/observations: the machine's recorded observations in
// identifier order, each with its start. A start whose observation was never recorded (its process
// ended during the read) is a timeline entry only.
func listObservations(a *API, w http.ResponseWriter, q *request) {
	m := q.r.PathValue("id")
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if err := machineNamed(ctx, tx, m); err != nil {
			return "", nil, err
		}
		out, err := listRows(ctx, tx, q, id.Observation, `SELECT o.id, o.machine, o.basis, o.revision, s.purpose, s.plan, s.operation,
				s.endpoint, s.controller, se.at, o.at, o.access_path, o.access_version, o.access_created, o.smbios_uuid::text,
				o.talos_node_id, o.talos_cluster_id, o.assignment_evidence, o.running_version, o.resource_version, o.health, o.unread
			FROM observation o
			JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
			JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
			WHERE o.id > $1 AND o.machine = $3 ORDER BY o.id LIMIT $2`, scanObservation, m)
		return "", out, err
	})
}

func scanObservation(r *sql.Rows) (observationBody, string, error) {
	var b observationBody
	var plan, operation, path sql.NullString
	var version sql.NullInt64
	var created sql.NullTime
	var health, unread []byte
	err := r.Scan(&b.ID, &b.Machine, &b.Basis, &b.Revision, &b.Purpose, &plan, &operation, &b.Endpoint, &b.Controller,
		&b.StartedAt, &b.At, &path, &version, &created, &b.SMBIOSUUID, &b.TalosNodeID, &b.TalosClusterID,
		&b.AssignmentEvidence, &b.RunningVersion, &b.ResourceVersion, &health, &unread)
	if err != nil {
		return b, "", err
	}
	if plan.Valid {
		b.Plan = &plan.String
	}
	if operation.Valid {
		b.Operation = &operation.String
	}
	if path.Valid {
		b.Access = &accessVersionBody{Path: path.String, Version: int(version.Int64), Created: created.Time}
	}
	if health != nil {
		b.Health = health
	}
	if err := json.Unmarshal(unread, &b.Unread); err != nil {
		return b, "", err
	}
	return b, b.ID, nil
}
