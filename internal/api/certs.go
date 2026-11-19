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

// certFor signs a certificate for one use of a cluster (persistence-api.md §3.3, §3.4): for role r,
// valid for life, from the exact CA key version the cluster's latest first-key act names, never the
// latest the provider holds. It reads that version (path, version and created_time must be the
// act's), then signs; no certificate is kept beyond the caller's use. A failure is a cause for
// talos-access-unavailable: absent (no act, or the signer cannot read the path), version-gone (the
// named version is deleted or destroyed), version-changed (written again since the act), denied,
// malformed, provider-unavailable, lifetime-exceeds-maximum (life is above the configured maximum,
// which is never shortened to fit) or sign. A cancelled ctx answers its own error and no cause.
func (a *API) certFor(ctx context.Context, cluster string, r role.Role, life time.Duration) (signer.Cert, string, error) {
	if a.d.sg == nil {
		return signer.Cert{}, "provider-unavailable", errors.New("the process has no signer")
	}
	var path string
	var version int
	var created time.Time
	// The latest act is the newest by its sequence, not the highest version: a path whose metadata was
	// deleted and written again has version 1 again, with another created_time.
	err := a.db.QueryRowContext(ctx, `SELECT k.key_path, k.key_version, k.key_created_time FROM first_key_act k
		JOIN act a ON a.id = k.act WHERE k.cluster = $1 ORDER BY a.seq DESC LIMIT 1`, cluster).Scan(&path, &version, &created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return signer.Cert{}, "absent", errNoKeyAct
	case err != nil:
		return signer.Cert{}, "", err
	}
	k, err := a.d.sg.ReadCAKeyVersion(ctx, cluster, version)
	if err != nil {
		if ctx.Err() != nil {
			return signer.Cert{}, "", ctx.Err()
		}
		if errors.Is(err, provider.ErrAbsent) {
			return signer.Cert{}, "version-gone", err
		}
		return signer.Cert{}, accessCause(err), err
	}
	// The database keeps microseconds and the provider nanoseconds: a version is the act's when the
	// two times agree to the microsecond.
	got := k.Version()
	if gap := got.CreatedTime.Sub(created).Abs(); got.Path != path || got.Version != version || gap >= time.Microsecond {
		return signer.Cert{}, "version-changed", errKeyChanged
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
