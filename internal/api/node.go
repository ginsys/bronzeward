package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// nodeTimeout bounds each request to a node, so that an endpoint that does not answer fails the
// ingestion talos-access-unavailable instead of holding its claim to the lease.
const nodeTimeout = 10 * time.Second

// readNode is a source machine job's input (persistence-api.md §3.3): the cluster's Talos access
// read; its version and the endpoint recorded as an event of the operation, committed before the
// node is dialled; the node dialled at the machine's endpoint, its identity compared with the
// machine's and its cluster's records; then its machine configuration. A failure is the
// operation's refusal, naming the cluster or the machine and never a value or a client's text; an
// error stops the run. The credential and the reader live only for this call.
func (a *API) readNode(ctx context.Context, j job) (ingest.Unresolved, *refusal, error) {
	n := j.node
	acc, err := a.d.ing.TalosAccess(ctx, j.claim.Cluster)
	if err != nil {
		if ctx.Err() != nil {
			return ingest.Unresolved{}, nil, errStop
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
		return ingest.Unresolved{}, a.accessFailure(j, cause, "cluster", j.claim.Cluster, err), nil
	}
	v := acc.Version()
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		return a.event(ctx, tx, j, map[string]any{"type": "talos-access", "path": v.Path, "version": v.Version,
			"createdTime": v.CreatedTime.UTC().Format(time.RFC3339Nano), "endpoint": n.endpoint})
	}); err != nil {
		return ingest.Unresolved{}, nil, fmt.Errorf("recording the Talos access: %w", err)
	}
	tc := acc.Talosconfig()
	r, err := a.o.dial(ctx, tc, n.endpoint)
	clear(tc)
	if err != nil {
		return ingest.Unresolved{}, a.accessFailure(j, "talosconfig", "cluster", j.claim.Cluster, err), nil
	}
	defer r.Close()

	id, err := nodeRead(ctx, a.o.nodeTimeout, r.Identity)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return ingest.Unresolved{}, nil, errStop
		case unanswered(err): // the connection was not accepted, or the node did not answer
			return ingest.Unresolved{}, a.accessFailure(j, "node-unreachable", "machine", j.claim.Machine, err), nil
		}
		return ingest.Unresolved{}, a.mismatch(j, "identity-read", err), nil
	}
	if cause := n.mismatch(id); cause != "" {
		return ingest.Unresolved{}, a.mismatch(j, cause, nil), nil
	}
	cfg, err := nodeRead(ctx, a.o.nodeTimeout, r.MachineConfig)
	if err != nil {
		if ctx.Err() != nil {
			return ingest.Unresolved{}, nil, errStop
		}
		// A credential Talos accepts for the identity but refuses for the configuration (a role
		// without os:admin), or a node that stopped answering.
		return ingest.Unresolved{}, a.accessFailure(j, "machine-config", "machine", j.claim.Machine, err), nil
	}
	return ingest.FromTalos(cfg), nil, nil
}

// nodeRead runs one request to the node, bounded by timeout.
func nodeRead[T any](ctx context.Context, timeout time.Duration, read func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return read(ctx)
}

// unanswered is a node request that reached no answer: a connection the node did not accept (a
// refused handshake included) or a request it did not answer in time.
func unanswered(err error) bool {
	c := status.Code(err)
	return c == codes.Unavailable || c == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded)
}

// mismatch is the cause, if any, for which the node that answered is not the recorded machine
// (§3.3, execution and recovery choice §10.26): for a machine recorded by SMBIOS UUID, a different
// or absent UUID; for one recorded by Talos node ID, any reported UUID or a different node ID; for
// either, a different Talos cluster ID. A recorded UUID is the database's lowercase form.
func (n *node) mismatch(got talos.Identity) string {
	switch {
	case n.smbiosUUID != "" && strings.ToLower(got.SMBIOSUUID) != n.smbiosUUID:
		return "smbios-uuid"
	case n.smbiosUUID == "" && got.SMBIOSUUID != "":
		return "smbios-uuid"
	case n.smbiosUUID == "" && got.NodeID != n.nodeID:
		return "node-id"
	case got.ClusterID != n.clusterID:
		return "cluster-id"
	}
	return ""
}

func (a *API) accessFailure(j job, cause, subject, id string, err error) *refusal {
	a.quote(j, cause, err)
	r := refuse(http.StatusServiceUnavailable, "talos-access-unavailable",
		"the machine could not be read through its cluster's Talos access; the claim is abandoned").with(subject, id)
	r.cause = cause
	return r
}

func (a *API) mismatch(j job, cause string, err error) *refusal {
	a.quote(j, cause, err)
	r := refuse(http.StatusConflict, "machine-identity-mismatch",
		"the node read is not the recorded machine; the claim is abandoned and nothing read is kept").with("machine", j.claim.Machine)
	r.cause = cause
	return r
}

// quote logs err's text under the pass-through control only: a provider's or a client's error
// can quote what it read (§3.3), so a failure is otherwise reported by its cause alone.
func (a *API) quote(j job, cause string, err error) {
	if a.o.quoteErrors && err != nil {
		a.o.logf("ingestion %s: %s: %v", j.claim.ID, cause, err)
	}
}
