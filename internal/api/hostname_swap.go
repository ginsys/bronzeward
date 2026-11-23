package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/compile"
)

// checkHostnameSwap refuses a release that gives a machine, as the hostname filled from its name, the
// full name another machine's last applied release recorded as its own (source `machine-name`): each
// machine has its own plan, so a release that renames machine X and gives X's old name to machine M
// could be applied in the wrong order and leave two nodes with one hostname (compilation.md §6.1 item
// 6). The comparison is on full names and reads the hostname record of each machine's applied release
// alone; the cases it does not cover are ginsys/bronzeward#1052. It runs in the publish transaction,
// after every machine state of the cluster is locked, so the records it reads cannot move before
// COMMIT. A machine of the release is never compared with its own record (a republish), and the other
// machine is any machine of the cluster, covered or not. It is named by its current name, or by its
// identifier where it has none: the name its record holds is the old one and only the hostname N is.
func checkHostnameSwap(ctx context.Context, tx *sql.Tx, cluster string, u releaseUnit) (*refusal, error) {
	for _, m := range u.machines {
		if m.hostname.Source != compile.HostnameMachineName {
			continue
		}
		var other string
		var current sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT s.machine, c.name
			FROM machine_state s
			JOIN release_machine r ON r.release = s.applied_release AND r.machine = s.machine
			JOIN machine c ON c.id = s.machine
			WHERE c.cluster = $1 AND s.machine <> $2 AND r.hostname_source = 'machine-name' AND r.hostname_name = $3
			ORDER BY s.machine LIMIT 1`, cluster, m.machine, m.hostname.Name).Scan(&other, &current)
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
		return refuse(http.StatusUnprocessableEntity, "validation-failed",
			fmt.Sprintf("Machine %s would take the hostname %s from its name, but the last release applied to machine %s still sets that hostname. "+
				"Give machine %s another name for now, apply the release for machine %s, then give machine %s the name %s.",
				m.hostname.Name, m.hostname.Name, holder, m.hostname.Name, holder, m.hostname.Name, m.hostname.Name)).
			with("rule", ruleHostname).with("machine", m.machine).with("other", other), nil
	}
	return nil, nil
}
