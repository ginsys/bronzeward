package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos/signer"
)

// errNoKeyAct is a cluster no first-key act names a key for: there is nothing to sign from.
var errNoKeyAct = errors.New("no first-key act names a CA key for the cluster")

// noFirstKeyDetail is the remedy a refusal for cause "absent" names: only the first-keys route
// creates an act, so a cluster onboarded before it needs a first key supplied (persistence-api.md §9.1).
const noFirstKeyDetail = "no first key is recorded for the cluster; supply one with POST /clusters/{id}/first-keys"

// errKeyChanged is a stored CA key version whose created_time is not the one the act recorded: the
// path and version name other bytes than the supply the act records.
var errKeyChanged = errors.New("the CA key version was written again since its act")

// firstKeyAct is the CA key version a cluster's latest first-key act names: its path, its version
// and that version's created_time as the database holds it.
type firstKeyAct struct {
	path    string
	version int
	created time.Time
}

// latestFirstKey reads cluster's latest first-key act; sql.ErrNoRows when it has none. The latest is
// the newest by its sequence, not the highest version: a path whose metadata was deleted and written
// again has version 1 again, with another created_time.
func latestFirstKey(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, cluster string) (firstKeyAct, error) {
	var a firstKeyAct
	err := q.QueryRowContext(ctx, `SELECT k.key_path, k.key_version, k.key_created_time FROM first_key_act k
		JOIN act a ON a.id = k.act WHERE k.cluster = $1 ORDER BY a.seq DESC LIMIT 1`, cluster).Scan(&a.path, &a.version, &a.created)
	return a, err
}

// actsTime reports whether a version's created_time is the one an act records. The database keeps
// microseconds and the provider nanoseconds: the two agree when they are within a microsecond.
func actsTime(provider, act time.Time) bool { return provider.Sub(act).Abs() < time.Microsecond }

// certFor signs a certificate for one use of a cluster (persistence-api.md §3.3, §3.4): for role r,
// valid for life, from the exact CA key version the cluster's latest first-key act names, never the
// latest the provider holds. It reads that version (path, version and created_time must be the
// act's), then signs; no certificate is kept beyond the caller's use. A failure is a cause for
// talos-access-unavailable: absent (no act, or the signer cannot read the path), version-gone (the
// named version is deleted or destroyed), version-changed (written again since the act), denied,
// malformed, provider-unavailable, lifetime-exceeds-maximum (life is above the configured maximum,
// which is never shortened to fit) or sign. A cancelled ctx answers its own error and no cause.
func (a *API) certFor(ctx context.Context, cluster string, r role.Role, life time.Duration) (signer.Cert, string, error) {
	k, cause, err := a.actKey(ctx, cluster)
	if err != nil {
		return signer.Cert{}, cause, err
	}
	c, err := signer.Sign(k, r, time.Now(), life, a.d.maxCert)
	if err != nil {
		if errors.Is(err, signer.ErrLifetime) {
			return signer.Cert{}, "lifetime-exceeds-maximum", err
		}
		return signer.Cert{}, "sign", err
	}
	return c, "", nil
}

// actKey is cluster's CA key at the version its latest first-key act names, never the latest the
// provider holds: path, version and created_time must be the act's. A failure carries certFor's
// cause for it; a database failure and a cancelled ctx carry none.
func (a *API) actKey(ctx context.Context, cluster string) (provider.CAKey, string, error) {
	var none provider.CAKey
	if a.d.sg == nil {
		return none, "provider-unavailable", errors.New("the process has no signer")
	}
	act, err := latestFirstKey(ctx, a.db, cluster)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return none, "absent", errNoKeyAct
	case err != nil:
		return none, "", err
	}
	k, err := a.d.sg.ReadCAKeyVersion(ctx, cluster, act.version)
	if err != nil {
		if ctx.Err() != nil {
			return none, "", ctx.Err()
		}
		if errors.Is(err, provider.ErrAbsent) {
			return none, "version-gone", err
		}
		return none, accessCause(err), err
	}
	// The database keeps microseconds and the provider nanoseconds: a version is the act's when the
	// two times agree to the microsecond.
	if got := k.Version(); got.Path != act.path || got.Version != act.version || !actsTime(got.CreatedTime, act.created) {
		return none, "version-changed", errKeyChanged
	}
	return k, "", nil
}
