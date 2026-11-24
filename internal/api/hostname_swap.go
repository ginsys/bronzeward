package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/compile"
)

// checkHostnameSwap refuses a release that gives a machine a Kubernetes node name that another machine's
// last applied release recorded and its own does not hold: each machine has its own plan, so a release
// that renames machine X and gives X's old node name to machine M could be applied in the wrong order and
// leave two nodes with one name (compilation.md §6.1 item 6). It compares the recorded `nodeName`
// (persistence-api.md §9.3) and never a hostname; a null or an absent one is not compared. Where the
// other machine's applied release recorded no node name and its source is `machine-name`, the recorded
// name is compared as before the record existed, so a release made before it keeps the protection it had.
// What changes after publication is the commitment's, whose hostname gate reads the same rule
// (execution-recovery.md §3.2 comparison 6a). It runs in the publish transaction, after every machine
// state of the cluster is locked, so the records it reads cannot move before COMMIT. A machine of the
// release is never compared with its own record (a republish), and the other machine is any machine of
// the cluster, covered or not. It is named by its current name, or by its identifier where it has none.
func checkHostnameSwap(ctx context.Context, tx *sql.Tx, cluster string, u releaseUnit) (*refusal, error) {
	for _, m := range u.machines {
		filled := m.hostname.Source == compile.HostnameMachineName
		if m.nodeName == nil && !filled {
			continue
		}
		var other string
		var current sql.NullString
		var byNode bool
		err := tx.QueryRowContext(ctx, `SELECT s.machine, c.name, r.node_name IS NOT NULL
			FROM machine_state s
			JOIN release_machine r ON r.release = s.applied_release AND r.machine = s.machine
			JOIN machine c ON c.id = s.machine
			LEFT JOIN machine_state own ON own.machine = $2
			LEFT JOIN release_machine owned ON owned.release = own.applied_release AND owned.machine = own.machine
			WHERE c.cluster = $1 AND s.machine <> $2 AND (
				(r.node_name = $3::text AND owned.node_name IS DISTINCT FROM $3::text)
				OR (NOT r.node_name_recorded AND r.hostname_source = 'machine-name' AND r.hostname_name = $4::text))
			ORDER BY s.machine LIMIT 1`, cluster, m.machine, m.nodeName, sql.NullString{String: m.hostname.Name, Valid: filled}).
			Scan(&other, &current, &byNode)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		holder := other
		if current.Valid && current.String != "" {
			holder = current.String
		}
		return refuse(http.StatusUnprocessableEntity, "validation-failed", swapDetail(m, holder, byNode)).
			with("rule", ruleHostname).with("machine", m.machine).with("other", other), nil
	}
	return nil, nil
}

// swapDetail is the refusal's words (compilation.md §6.1 item 6): it names both machines, a node name only
// where the other machine's record holds one, and no configured value, which may be a secret reference.
func swapDetail(m unitMachine, holder string, byNode bool) string {
	label := m.name
	if label == "" {
		label = m.machine
	}
	switch {
	case !byNode: // compared by the recorded name, as before the node name was recorded
		return fmt.Sprintf("Machine %s would take the hostname %s from its name, but the last release applied to machine %s still sets that hostname. "+
			"Give machine %s another name for now, apply the release for machine %s, then give machine %s the name %s.",
			m.hostname.Name, m.hostname.Name, holder, m.hostname.Name, holder, m.hostname.Name, m.hostname.Name)
	case m.hostname.Source == compile.HostnameMachineName:
		return fmt.Sprintf("Machine %s would take the hostname %s from its name, but the last release applied to machine %s still gives the Kubernetes node name %s. "+
			"Give machine %s another name for now, apply the release for machine %s, then give machine %s the name %s.",
			label, m.hostname.Name, holder, *m.nodeName, label, holder, label, m.hostname.Name)
	}
	return fmt.Sprintf("Machine %s's configured hostname gives the Kubernetes node name %s, but the last release applied to machine %s records that. "+
		"Give machine %s another hostname for now, apply the release for machine %s, then set it.", label, *m.nodeName, holder, label, holder)
}
