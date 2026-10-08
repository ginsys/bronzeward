package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// observeFor is what an observation is taken for (execution-recovery.md §4.1): its purpose, and
// the plan and operation it is taken for, if any.
type observeFor struct {
	purpose, plan, operation string
}

// observed is what observe recorded: the observation's id, its basis (the start entry's revision)
// and revision, the endpoint dialled, and each value read; Unread names each value not read and
// why. Access is nil when the access read failed.
type observed struct {
	ID                 string
	Basis, Revision    int64
	Endpoint           string
	Access             *provider.TalosAccessVersion
	Identity           *talos.Identity
	AssignmentEvidence string
	RunningVersion     string
	Digest             *[32]byte
	ResourceVersion    string
	Unread             map[string]unreadValue
}

// unreadValue is why a value was not read: a talos-access-unavailable code and its cause for a
// failed access read, connection or request, or a cause alone.
type unreadValue struct {
	Code  string `json:"code,omitempty"`
	Cause string `json:"cause"`
}

// The forms the observation table holds a node's report in; a report outside them is that read's
// failure, so a node cannot make the record fail.
var (
	talosNodeIDForm    = regexp.MustCompile(`^[!-~]{1,128}$`)
	talosClusterIDForm = regexp.MustCompile(`^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]=$`)
	runningVersionForm = regexp.MustCompile(`^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(-[0-9A-Za-z.-]{1,64})?$`)
	resourceVersionFm  = regexp.MustCompile(`^[!-~]{1,64}$`)
)

var errNotController = errors.New("observation: this process is no controller of the current epoch")

// observe takes one observation of machine for f (execution-recovery.md §4.1, persistence-api.md
// §3.3) as this process, the controller: an "observation started" entry recorded first, naming
// the endpoint it dials (the plan's route for a plan, the machine's current endpoint otherwise),
// whose revision is the basis; then the executor's read of the cluster's Talos access, the node
// read through the read-only client (identity, running version, configuration digest and
// resource version, each bounded by the node timeout); then the observation entry recording what
// was read and, for each value not read, why. A failed access read or connection is still an
// observation, reading no node value. The talosconfig and the configuration live only in memory
// for this call; neither is stored, logged or returned. An error before the start entry commits
// records nothing; one after it leaves the start without an observation.
func (a *API) observe(ctx context.Context, machine string, f observeFor) (observed, error) {
	if a.d.owner.ID == "" || a.d.exe == nil {
		return observed{}, errNotController
	}
	o := observed{ID: id.New(id.Observation), Unread: map[string]unreadValue{"health": {Cause: "none-bound"}}}
	var cluster string
	var head sql.NullString
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		cluster, head, err = a.startObservation(ctx, tx, machine, f, &o)
		return err
	}); err != nil {
		return observed{}, err
	}

	acc, err := a.d.exe.TalosAccess(ctx, cluster)
	if err != nil {
		if ctx.Err() != nil {
			return observed{}, ctx.Err()
		}
		cause := "provider-unavailable" // sealed, silent, or a status the client does not classify
		switch {
		case errors.Is(err, provider.ErrAbsent):
			cause = "absent"
		case errors.Is(err, provider.ErrDenied):
			cause = "denied"
		case errors.Is(err, provider.ErrProtocol):
			cause = "malformed"
		}
		a.quoteObservation(o.ID, cause, err)
		o.unreadAll(cause, "identity", "runningVersion", "configuration", "assignmentEvidence")
		return o, a.recordObservation(ctx, machine, f, &o)
	}
	v := acc.Version()
	o.Access = &v
	if head.Valid {
		o.AssignmentEvidence = head.String
	} else {
		o.Unread["assignmentEvidence"] = unreadValue{Cause: "unassigned"}
	}
	tc := acc.Talosconfig()
	r, err := a.o.dial(ctx, tc, o.Endpoint)
	clear(tc)
	if err != nil {
		a.quoteObservation(o.ID, "talosconfig", err)
		o.unreadAll("talosconfig", "identity", "runningVersion", "configuration")
		return o, a.recordObservation(ctx, machine, f, &o)
	}
	defer r.Close()

	reads := []struct {
		name, cause string
		read        func(context.Context) error
	}{
		{"identity", "identity-read", func(ctx context.Context) error {
			got, err := r.Identity(ctx)
			if err == nil {
				if !validIdentity(got) {
					return errors.New("the node reported an identity outside the recorded forms")
				}
				o.Identity = &got
			}
			return err
		}},
		{"runningVersion", "version-read", func(ctx context.Context) error {
			got, err := r.Version(ctx)
			if err == nil {
				if !runningVersionForm.MatchString(got) {
					return errors.New("the node reported a version outside the recorded form")
				}
				o.RunningVersion = got
			}
			return err
		}},
		{"configuration", "machine-config", func(ctx context.Context) error {
			got, err := r.MachineConfig(ctx)
			if err == nil {
				if !resourceVersionFm.MatchString(got.ResourceVersion()) {
					return errors.New("the node reported a resource version outside the recorded form")
				}
				d := got.Digest()
				o.Digest, o.ResourceVersion = &d, got.ResourceVersion()
			}
			return err
		}},
	}
	for i, rd := range reads {
		_, err := nodeRead(ctx, a.o.nodeTimeout, func(ctx context.Context) (struct{}, error) { return struct{}{}, rd.read(ctx) })
		switch {
		case err == nil:
			continue
		case ctx.Err() != nil:
			return observed{}, ctx.Err()
		case unanswered(err): // the node did not accept the connection or did not answer: read no more
			a.quoteObservation(o.ID, "node-unreachable", err)
			for _, rest := range reads[i:] {
				o.unreadAll("node-unreachable", rest.name)
			}
			return o, a.recordObservation(ctx, machine, f, &o)
		}
		a.quoteObservation(o.ID, rd.cause, err)
		o.unreadAll(rd.cause, rd.name)
	}
	return o, a.recordObservation(ctx, machine, f, &o)
}

// startObservation is the start entry's transaction (T7): the installation's epoch, which must be
// this process's, read FOR SHARE; the machine row FOR UPDATE; the assignment head FOR SHARE, the
// assignment evidence; the entry at the machine's next revision, at a time read after the locks
// (persistence-api.md §5 rules 4-5). It sets o's basis and endpoint, and answers the machine's
// cluster and assignment head.
func (a *API) startObservation(ctx context.Context, tx *sql.Tx, machine string, f observeFor, o *observed) (string, sql.NullString,
	error) {
	var head sql.NullString
	var current, cluster string
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
		return "", head, err
	}
	if current != a.d.owner.Epoch {
		return "", head, errNotController
	}
	if err := tx.QueryRowContext(ctx, `SELECT cluster, talos_endpoint FROM machine WHERE id = $1 FOR UPDATE`, machine).Scan(&cluster,
		&o.Endpoint); err != nil {
		return "", head, fmt.Errorf("observation of machine %s: %w", machine, err)
	}
	if f.plan != "" { // a plan is immutable: its route needs no lock
		if err := tx.QueryRowContext(ctx, `SELECT route FROM plan WHERE id = $1 AND machine = $2`, f.plan, machine).Scan(&o.Endpoint); err != nil {
			return "", head, fmt.Errorf("observation of machine %s for plan %s: %w", machine, f.plan, err)
		}
	}
	err := tx.QueryRowContext(ctx, `SELECT head_revision_id FROM assignment WHERE machine = $1 FOR SHARE`, machine).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", head, err
	}
	at, err := nextEntry(ctx, tx, machine, &o.Basis)
	if err != nil {
		return "", head, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'observation-started', jsonb_build_object('purpose', $4::text, 'plan', $5::text, 'operation', $6::text,
			'endpoint', $7::text, 'controller', $8::text), $9)`,
		machine, o.Basis, current, f.purpose, nullable(f.plan), nullable(f.operation), o.Endpoint, a.d.owner.ID, at); err != nil {
		return "", head, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO observation_start (machine, revision, purpose, plan, operation, endpoint, controller)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, machine, o.Basis, f.purpose, nullable(f.plan), nullable(f.operation), o.Endpoint,
		a.d.owner.ID); err != nil {
		return "", head, err
	}
	return cluster, head, nil
}

// recordObservation is the observation entry's transaction (T7), under the same epoch check and
// machine lock as its start.
func (a *API) recordObservation(ctx context.Context, machine string, f observeFor, o *observed) error {
	unread, err := json.Marshal(o.Unread)
	if err != nil {
		return err
	}
	var path, version, created, smbios, nodeID, clusterID any
	var digest []byte
	if o.Access != nil {
		path, version, created = o.Access.Path, int64(o.Access.Version), o.Access.CreatedTime
	}
	if o.Identity != nil {
		smbios, nodeID, clusterID = nullable(o.Identity.SMBIOSUUID), o.Identity.NodeID, o.Identity.ClusterID
	}
	assignment, running, resource := nullable(o.AssignmentEvidence), nullable(o.RunningVersion), nullable(o.ResourceVersion)
	if o.Digest != nil {
		digest = o.Digest[:]
	}
	return a.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return errNotController
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, machine); err != nil {
			return err
		}
		at, err := nextEntry(ctx, tx, machine, &o.Revision)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'observation', jsonb_build_object('observation', $4::text, 'purpose', $5::text, 'basis', $6::bigint), $7)`,
			machine, o.Revision, current, o.ID, f.purpose, o.Basis, at); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
				smbios_uuid, talos_node_id, talos_cluster_id, assignment_evidence, running_version, configuration_digest, resource_version,
				health, unread, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid, $9, $10, $11, $12, $13, $14, NULL, $15, $16)`,
			o.ID, machine, o.Basis, o.Revision, path, version, created, smbios, nodeID, clusterID, assignment, running, digest, resource,
			unread, at)
		return err
	})
}

// nextEntry allocates the machine's next timeline revision into rev and answers the entry's time,
// read after every lock the caller took (persistence-api.md §5 rule 4).
func nextEntry(ctx context.Context, tx *sql.Tx, machine string, rev *int64) (time.Time, error) {
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
		RETURNING revision_counter`, machine).Scan(rev); err != nil {
		return time.Time{}, err
	}
	var at time.Time
	err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at)
	return at, err
}

// unreadAll names each value unread for a talos-access-unavailable cause.
func (o *observed) unreadAll(cause string, names ...string) {
	for _, n := range names {
		o.Unread[n] = unreadValue{Code: "talos-access-unavailable", Cause: cause}
	}
}

// validIdentity is a report the observation table holds: a UUID or none, and the node and cluster
// IDs in their forms.
func validIdentity(got talos.Identity) bool {
	if got.SMBIOSUUID != "" && !uuidShape.MatchString(strings.ToLower(got.SMBIOSUUID)) {
		return false
	}
	return talosNodeIDForm.MatchString(got.NodeID) && talosClusterIDForm.MatchString(got.ClusterID)
}

// quoteObservation logs err's text under the pass-through control only, as quote does.
func (a *API) quoteObservation(obs, cause string, err error) {
	if a.o.quoteErrors && err != nil {
		a.o.logf("observation %s: %s: %v", obs, cause, err)
	}
}
