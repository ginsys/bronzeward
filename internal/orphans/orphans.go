// Package orphans is the orphan report of persistence-api.md §6.4: the provider generations under
// gen/ that no committed reference row names and whose claim is not recorded held or resumed. It
// lists the provider through the orphan-report identity, outside any transaction, then reads the
// reference rows and the claims in one statement inside a read-only transaction, and changes
// nothing in either.
package orphans

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// Lister is the provider listing the report walks: *provider.Report.
type Lister interface {
	Clusters(ctx context.Context) (provider.Listing, error)
	Claims(ctx context.Context, cluster string) (provider.Listing, error)
	Values(ctx context.Context, cluster, claim string) (provider.Generations, error)
}

// Entry is one reported generation path and its claim as the database records it. State is empty,
// and the times zero, when the database holds no row for the claim.
type Entry struct {
	Path, Claim, State, Mode         string
	CreatedAt, ExpiresAt, LeaseUntil time.Time
}

// Report is one run's result. Orphans are the generations §6.4 reports; Expired are the
// unreferenced generations of claims recorded held or resumed that compilation §3.5 already
// treats as abandoned, listed apart and never counted as orphans. Listed counts the generation
// paths listed; Skipped the names under gen/ not of Bronzeward's form.
type Report struct {
	Orphans, Expired []Entry
	Listed, Skipped  int
}

// Error is a failed run: the step and the class of its failure, never a path, an identifier, or
// a provider's or database's own text.
type Error struct {
	Step, Class string
	err         error
}

func (e *Error) Error() string { return e.Step + ": " + e.Class }
func (e *Error) Unwrap() error { return e.err }

// Steps.
const (
	stepClusters   = "listing clusters"
	stepClaims     = "listing a cluster's claims"
	stepValues     = "listing a claim's generations"
	stepDBClusters = "reading the database's clusters"
	stepRead       = "reading references and claims"
)

func providerFailure(step string, err error) error {
	class := "the provider answered with an unexpected status"
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		class = "interrupted"
	case errors.Is(err, provider.ErrUnavailable):
		class = "the provider is unavailable"
	case errors.Is(err, provider.ErrDenied):
		class = "the provider refused the request"
	case errors.Is(err, provider.ErrProtocol):
		class = "the provider's response was not understood"
	}
	return &Error{Step: step, Class: class, err: err}
}

func databaseFailure(step string, err error) error {
	class := "the database failed"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		class = "interrupted"
	}
	return &Error{Step: step, Class: class, err: err}
}

// Collect runs the report over cluster, or over every cluster directory gen/ lists when cluster is
// empty, so that a cluster a restore removed from the database is still walked.
func Collect(ctx context.Context, l Lister, db *sql.DB, cluster string) (Report, error) {
	return collect(ctx, l, db, cluster, options{})
}

// options are the controls of §16's checks, each a wrong implementation the checks must catch;
// production runs none of them.
type options struct {
	stateOnly       bool   // select by claim state alone, ignoring reference rows
	readTimeAbandon bool   // treat a due claim as abandoned (an orphan) at read time
	omitLive        bool   // omit every held or resumed claim's generations
	lapsedLease     bool   // treat every lapsed lease as abandonment, encrypted claims included
	dbClusters      bool   // walk only the clusters the database records
	split           bool   // read references and claims in two statements
	between         func() // runs before the statement, or between the two under split
}

func collect(ctx context.Context, l Lister, db *sql.DB, cluster string, o options) (Report, error) {
	var r Report
	var clusters []string
	switch {
	case cluster != "":
		if id.MustHave(cluster, id.Cluster) != nil {
			return Report{}, errors.New("the cluster is not a cluster identifier")
		}
		clusters = []string{cluster}
	case o.dbClusters:
		var err error
		if clusters, err = recordedClusters(ctx, db); err != nil {
			return Report{}, databaseFailure(stepDBClusters, err)
		}
	default:
		ls, err := l.Clusters(ctx)
		if err != nil {
			return Report{}, providerFailure(stepClusters, err)
		}
		clusters, r.Skipped = ls.Names, ls.Skipped
	}
	var listed []string
	for _, cl := range clusters {
		claims, err := l.Claims(ctx, cl)
		if err != nil {
			return Report{}, providerFailure(stepClaims, err)
		}
		r.Skipped += claims.Skipped
		for _, claim := range claims.Names {
			gens, err := l.Values(ctx, cl, claim)
			if err != nil {
				return Report{}, providerFailure(stepValues, err)
			}
			r.Skipped += gens.Skipped
			for _, p := range gens.Paths {
				listed = append(listed, p.String())
			}
		}
	}
	r.Listed = len(listed)
	rows, err := read(ctx, db, listed, o)
	if err != nil {
		return Report{}, databaseFailure(stepRead, err)
	}
	for _, row := range rows {
		switch {
		case row.State == "" || row.State == "abandoned" || row.State == "released":
			r.Orphans = append(r.Orphans, row.Entry)
		case !row.due:
		case o.readTimeAbandon:
			r.Orphans = append(r.Orphans, row.Entry)
		case !o.omitLive:
			r.Expired = append(r.Expired, row.Entry)
		}
	}
	return r, nil
}

func recordedClusters(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM cluster ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// row is an unreferenced listed path, its claim's recorded row and whether the claim is due.
type row struct {
	Entry
	due bool
}

// read returns the listed paths no committed reference row names, each with its claim, in path
// order. One statement in a read-only transaction: reference rows and claims from one snapshot
// under read committed (§5 rule 3), so a draft transaction releasing a claim with its reference
// rows is seen whole or not at all.
func read(ctx context.Context, db *sql.DB, listed []string, o options) ([]row, error) {
	due := staging.DueSQL
	if o.lapsedLease {
		due = `state IN ('held', 'resumed') AND (expires_at <= clock_timestamp() OR lease_until <= clock_timestamp())`
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var out []row
	if o.split {
		out, err = readSplit(ctx, tx, listed, due, o.between)
	} else {
		if o.between != nil {
			o.between()
		}
		q := `SELECT l.path, split_part(l.path, '/', 3), coalesce(c.state, ''), coalesce(c.mode, ''),
			c.created_at, c.expires_at, c.lease_until, coalesce(` + due + `, false)
			FROM unnest($1::text[]) AS l(path)
			LEFT JOIN staging_claim c ON c.id = split_part(l.path, '/', 3)`
		if !o.stateOnly {
			q += ` WHERE NOT EXISTS (SELECT 1 FROM import_base_reference r WHERE r.generation = l.path)`
		}
		out, err = scan(tx.QueryContext(ctx, q+` ORDER BY l.path`, listed))
	}
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func scan(rows *sql.Rows, err error) ([]row, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		var created, expires, lease sql.NullTime
		if err := rows.Scan(&r.Path, &r.Claim, &r.State, &r.Mode, &created, &expires, &lease, &r.due); err != nil {
			return nil, err
		}
		r.CreatedAt, r.ExpiresAt, r.LeaseUntil = created.Time, expires.Time, lease.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// readSplit is the two-statement control: the reference rows, then the claims.
func readSplit(ctx context.Context, tx *sql.Tx, listed []string, due string, between func()) ([]row, error) {
	refs, err := tx.QueryContext(ctx, `SELECT generation FROM import_base_reference WHERE generation = ANY($1::text[])`, listed)
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	for refs.Next() {
		var g string
		if err := refs.Scan(&g); err != nil {
			refs.Close()
			return nil, err
		}
		referenced[g] = true
	}
	refs.Close()
	if between != nil {
		between()
	}
	all, err := scan(tx.QueryContext(ctx, `SELECT l.path, split_part(l.path, '/', 3), coalesce(c.state, ''), coalesce(c.mode, ''),
		c.created_at, c.expires_at, c.lease_until, coalesce(`+due+`, false)
		FROM unnest($1::text[]) AS l(path)
		LEFT JOIN staging_claim c ON c.id = split_part(l.path, '/', 3) ORDER BY l.path`, listed))
	if err != nil {
		return nil, err
	}
	var out []row
	for _, r := range all {
		if !referenced[r.Path] {
			out = append(out, r)
		}
	}
	return out, nil
}

// Write prints the report: one tab-separated line per orphan, then one per generation listed
// apart, then a summary line. Paths, claim ids, states, modes and times only.
func Write(w io.Writer, r Report) error {
	var b strings.Builder
	for _, e := range r.Orphans {
		state := e.State
		if state == "" {
			state = "absent"
		}
		fmt.Fprintf(&b, "orphan\t%s\tclaim=%s\tstate=%s\tcreated=%s\texpires=%s\n",
			e.Path, e.Claim, state, stamp(e.CreatedAt), stamp(e.ExpiresAt))
	}
	for _, e := range r.Expired {
		fmt.Fprintf(&b, "expired\t%s\tclaim=%s\tstate=%s\tmode=%s\tcreated=%s\texpires=%s\tlease=%s\n",
			e.Path, e.Claim, e.State, e.Mode, stamp(e.CreatedAt), stamp(e.ExpiresAt), stamp(e.LeaseUntil))
	}
	fmt.Fprintf(&b, "%d orphans, %d expired and not yet abandoned, %d generations listed, %d names skipped\n",
		len(r.Orphans), len(r.Expired), r.Listed, r.Skipped)
	_, err := io.WriteString(w, b.String())
	return err
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
