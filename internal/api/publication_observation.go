package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// lockPublication fences a publication's remote read under its machine lock. A
// takeover cannot start or complete evidence using the preceding owner's claim.
func (a *API) lockPublication(ctx context.Context, tx *sql.Tx, machine string, f observeFor) error {
	if f.purpose != "publication" {
		return nil
	}
	var op string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE id = $1 FOR UPDATE`, f.operation).Scan(&op); err != nil {
		return a.fenced(err)
	}
	// Read the lease after the lock wait, never at the statement's earlier start.
	err := tx.QueryRowContext(ctx, `SELECT o.id FROM operation o JOIN draft d ON d.id = o.draft
  JOIN machine m ON m.cluster = d.cluster AND m.id = $1
  WHERE o.id = $2 AND o.kind = 'publish' AND o.state = 'running'
   AND o.owner = $3 AND o.owner_gen = $4 AND o.owner_epoch = $5
   AND o.lease_until > clock_timestamp()`, machine, f.operation, a.d.owner.ID,
		f.generation, a.d.owner.Epoch).Scan(&op)
	return a.fenced(err)
}

func (a *API) publicationObservations(ctx context.Context, j publishJob, s snapshot) (map[string]string, *refusal, error) {
	out := make(map[string]string, len(s.machines))
	for _, m := range s.machines {
		o, err := a.observe(ctx, m.machine, observeFor{purpose: "publication", operation: j.op, generation: j.gen})
		if err != nil {
			return nil, nil, err
		}
		if ref, err := a.publicationEvidence(ctx, a.db, j, m.machine, o.ID, s.contract, false); ref != nil || err != nil {
			return nil, ref, err
		}
		out[m.machine] = o.ID
	}
	return out, nil, nil
}

type publicationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// publicationEvidence compares an immutable read against inventory and the
// snapshot contract. T3 also checks endpoint events and higher-basis evidence,
// while its machine locks prevent either from changing before commit.
func (a *API) publicationEvidence(ctx context.Context, q publicationQuery, j publishJob, machine, observation, contract string,
	commit bool) (*refusal, error) {
	ref := func(status int, rule, cause string) *refusal {
		code := "validation-failed"
		if status == http.StatusServiceUnavailable {
			code = "dependency-unavailable"
		}
		if status == http.StatusConflict {
			code = "stale-input"
		}
		return refuse(status, code, "Publication refused by its node evidence check.").with("machine", machine).
			with("observation", observation).with("rule", rule).with("cause", cause)
	}
	var bound, usable, identity, minor, endpoint bool
	var basis int64
	err := q.QueryRowContext(ctx, `SELECT
  s.purpose = 'publication' AND s.operation = $3 AND s.publication_generation = $4 AND s.controller = $5
   AND se.epoch = $6 AND oe.epoch = $6 AND m.cluster = $7,
  o.access_path IS NOT DISTINCT FROM 'access/talos/' || m.cluster AND o.access_version IS NOT NULL
   AND o.talos_node_id IS NOT NULL AND o.talos_cluster_id IS NOT NULL AND o.running_version IS NOT NULL
   AND NOT o.unread ? 'identity' AND NOT o.unread ? 'runningVersion',
  `+identityMatch+`, substring(o.running_version FROM '^v[0-9]+\.[0-9]+') IS NOT DISTINCT FROM $8,
  s.endpoint = m.talos_endpoint, o.basis
  FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
  JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
  JOIN machine_event se ON se.machine = s.machine AND se.revision = s.revision
  JOIN machine_event oe ON oe.machine = o.machine AND oe.revision = o.revision
  WHERE o.id = $1 AND o.machine = $2`, observation, machine, j.op, j.gen, a.d.owner.ID, a.d.owner.Epoch, j.cluster, contract).
		Scan(&bound, &usable, &identity, &minor, &endpoint, &basis)
	if errors.Is(err, sql.ErrNoRows) {
		return ref(409, "publication-evidence", "missing-observation"), nil
	}
	if err != nil {
		return nil, err
	}
	if !bound {
		return ref(409, "publication-evidence", "superseded-observation"), nil
	}
	if !usable {
		return ref(503, "publication-evidence", "unreadable-identity-or-version"), nil
	}
	if !identity {
		return ref(422, "publication-identity", "identity-mismatch"), nil
	}
	if !minor {
		return ref(422, "publication-version", "minor-mismatch"), nil
	}
	if !commit {
		return nil, nil
	}
	var changed, newer bool
	err = q.QueryRowContext(ctx, `SELECT
  EXISTS (SELECT FROM machine_event WHERE machine = $1 AND revision > $2 AND kind = 'endpoint-change'),
  EXISTS (SELECT FROM observation o JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
   WHERE o.machine = $1 AND o.basis > $2 AND
   (o.access_version IS NULL OR o.talos_node_id IS NULL OR o.talos_cluster_id IS NULL OR o.running_version IS NULL
    OR o.unread ? 'identity' OR o.unread ? 'runningVersion' OR NOT `+identityMatch+`
    OR substring(o.running_version FROM '^v[0-9]+\.[0-9]+') IS DISTINCT FROM $3))`, machine, basis, contract).Scan(&changed, &newer)
	if err != nil {
		return nil, err
	}
	if !endpoint || changed {
		return ref(409, "publication-evidence", "endpoint-changed"), nil
	}
	if newer {
		return ref(409, "publication-evidence", "newer-observation"), nil
	}
	return nil, nil
}
