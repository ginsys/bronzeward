package api

import (
	"context"
	"database/sql"
	"errors"
)

// hostnameHolderQuery is the hostname gate's rule (execution-recovery.md §3.2 comparison 6a) as one
// query: the other machine of the cluster whose `Applied` release records the node name that release
// machine gives the machine, unless the machine's own `Applied` release already holds that name (a name
// it holds now is not a name it takes). A node name that is null (not compared) or not recorded is
// NULL here, so it never equals another and is never compared; another machine with no `Applied`
// release joins nothing. The lowest identifier answers when several machines hold the name.
//
// machine and release are SQL expressions, so the commitment, the position read and the executor's
// candidate read judge one rule, each against its own row: hostnameHolderQuery("$1", "$2") answers a
// machine and the release its plan binds; a caller inside another query names that query's columns.
func hostnameHolderQuery(machine, release string) string {
	return `SELECT o.machine, c.name
		FROM release_machine mine
			JOIN machine_state o ON o.machine <> mine.machine
			JOIN machine c ON c.id = o.machine AND c.cluster = mine.cluster
			JOIN release_machine held ON held.release = o.applied_release AND held.machine = o.machine
			LEFT JOIN machine_state own ON own.machine = mine.machine
			LEFT JOIN release_machine owned ON owned.release = own.applied_release AND owned.machine = own.machine
		WHERE mine.release = ` + release + ` AND mine.machine = ` + machine + `
			AND mine.node_name IS NOT NULL AND held.node_name = mine.node_name
			AND owned.node_name IS DISTINCT FROM mine.node_name
		ORDER BY o.machine LIMIT 1`
}

// hostnameHolder is the machine whose `Applied` release holds a node name, as the position read and a
// commitment's refusal name it: by its name, or by its identifier where it has none.
type hostnameHolder struct {
	ID   string
	Name string // "" for none
}

// label names the holder in words: by name where it has one, by identifier only where it has none.
func (h hostnameHolder) label() string {
	if h.Name != "" {
		return h.Name
	}
	return h.ID
}

// hostnameHeldBy answers the machine that holds the node name release gives machine, or nil when none
// does (execution-recovery.md §3.2 comparison 6a). It reads the records as q's transaction sees them:
// the commitment calls it under the cluster's machine locks, the position read from its snapshot.
func hostnameHeldBy(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, machine, release string) (*hostnameHolder, error) {
	var h hostnameHolder
	var name sql.NullString
	err := q.QueryRowContext(ctx, hostnameHolderQuery("$1", "$2"), machine, release).Scan(&h.ID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h.Name = name.String
	return &h, nil
}
