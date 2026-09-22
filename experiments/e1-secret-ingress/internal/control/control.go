// Package control is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation, and nothing in it is a pattern to
// copy: every function here implements a design the experiment exists to argue against.
//
// The package exists because an absence is not a measurement. A leak scan that finds nothing tells
// you either that there was nothing to find or that the instrument could not see it, and those two
// are indistinguishable from the output alone. Each control below is a deliberate leak on a known
// surface, run through the same instrument as the honest path. If a control comes back clean, that
// surface is not evidence, and the report says so rather than reporting the honest run as proof.
//
// Two of the controls carry the experiment's strongest results:
//
//   - PersistFirst is the semantic control. It writes the observed configuration as an ordinary
//     plaintext draft and extracts afterwards, which is exactly what design §7.1 forbids. It must
//     produce hits in the dump, the heap and the write-ahead log, or the whole instrument is blind
//     to the thing it was built to detect.
//   - RedactAfter follows it with the redaction §7.1 says is not a remedy: the draft is updated to
//     references, the plaintext is gone from the live table, and it is still in the write-ahead
//     log and in any earlier snapshot. That turns "redacting it later is forbidden" from an
//     assertion into a measurement.
//
// SurfaceTransformed is the opposite kind of control: it writes a base64 encoding of the canary and
// must produce zero hits. It demonstrates the instrument's blindness on purpose, which is better
// evidence for the report's limits section than restating the caveat in prose.
//
// This package is one of the two allowed callers of secret.Unresolved.Unsafe, the other being
// internal/extract. That allowlist is asserted by a test in internal/secret.
package control

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

// Surface names a place a deliberate leak is written. The names are the --leak-at flag's values and
// the captions the report uses, so that a bundle, a flag and a table row cannot drift apart.
type Surface string

// The surfaces, in the order the boundary map lists them.
const (
	// SurfaceNone leaks nothing. It is the honest path.
	SurfaceNone Surface = "none"
	// SurfaceTempFile is a working file under the run root, the shape an implementation reaches for
	// when it wants to hand a configuration to another tool.
	SurfaceTempFile Surface = "temp-file"
	// SurfaceStaging is a staging file under the run root, the shape neither staging alternative
	// uses and that an implementation could still arrive at.
	SurfaceStaging Surface = "staging"
	// SurfaceAppLog is the application's own log.
	SurfaceAppLog Surface = "app-log"
	// SurfaceErrorReport is a failure report quoting the value that caused it, which is how most
	// real leaks of this kind happen.
	SurfaceErrorReport Surface = "error-report"
	// SurfaceDBLog is the database server's statement log, reached by putting the value in the
	// statement text rather than in a parameter.
	SurfaceDBLog Surface = "db-log"
	// SurfaceTransformed writes the value base64-encoded. It must produce zero hits: the scan
	// matches literal strings, and this is the shape it cannot see.
	SurfaceTransformed Surface = "transformed"
)

// surfaces is the closed set, in flag-help and report order.
var surfaces = []Surface{
	SurfaceNone, SurfaceTempFile, SurfaceStaging, SurfaceAppLog,
	SurfaceErrorReport, SurfaceDBLog, SurfaceTransformed,
}

// Surfaces returns the accepted --leak-at values.
func Surfaces() []string {
	names := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		names = append(names, string(s))
	}
	return names
}

// ParseSurface resolves a flag value.
//
// An unknown value is an error rather than a no-op: a misspelled control would run the honest path,
// produce a clean bundle, and be filed under the caption of a control that never ran.
func ParseSurface(s string) (Surface, error) {
	for _, known := range surfaces {
		if Surface(s) == known {
			return known, nil
		}
	}
	return SurfaceNone, fmt.Errorf("control: unknown leak surface %q; want one of %s",
		s, strings.Join(Surfaces(), ", "))
}

// Expectation is what a control's leak scan must show for the bundle to be usable.
type Expectation int

const (
	// ExpectNone means the run root and the database must scan clean.
	ExpectNone Expectation = iota
	// ExpectHit means the scan must find the value, and a clean result means the surface is not
	// evidence.
	ExpectHit
	// ExpectMiss means the scan must find nothing even though the value is there, which is the
	// instrument's blindness shown on purpose.
	ExpectMiss
)

// Expect reports what the surface's leak scan must show.
func (s Surface) Expect() Expectation {
	switch s {
	case SurfaceNone:
		return ExpectNone
	case SurfaceTransformed:
		return ExpectMiss
	default:
		return ExpectHit
	}
}

// Options are the deliberate-failure flags.
type Options struct {
	// PersistFirst persists the observed configuration as plaintext before extracting.
	PersistFirst bool
	// RedactAfter follows PersistFirst with the redaction §7.1 says is not a remedy.
	RedactAfter bool
	// Rollback aborts the plaintext transaction instead of committing it.
	Rollback bool
	// LeakAt writes the value to one named surface.
	LeakAt Surface
}

// Active reports whether this run is a control rather than an honest run.
func (o Options) Active() bool {
	return o.PersistFirst || o.RedactAfter || o.Rollback || o.LeakAt != SurfaceNone
}

// Validate refuses combinations that would produce a bundle which is not the control it is filed
// as. Every one of these would otherwise run something, succeed, and be read as a result.
func (o Options) Validate() error {
	if o.RedactAfter && !o.PersistFirst {
		return errors.New("control: --redact-after needs --persist-first; there is nothing to redact " +
			"on the honest path, where the plaintext was never persisted")
	}
	if o.Rollback && !o.PersistFirst {
		return errors.New("control: --rollback needs --persist-first; the honest path's transaction " +
			"carries no plaintext, so rolling it back demonstrates nothing")
	}
	if o.RedactAfter && o.Rollback {
		return errors.New("control: --redact-after and --rollback cannot both be set; one commits the " +
			"plaintext and then updates it, the other never commits, and a bundle must be one or the other")
	}
	return nil
}

// Describe is the caption the journal and the bundle carry, so a capture is labelled by the code
// that produced it rather than by whoever ran it.
func (o Options) Describe() string {
	if !o.Active() {
		return "honest path"
	}
	var parts []string
	if o.PersistFirst {
		parts = append(parts, "persist-first (the design §7.1 forbids)")
	}
	if o.RedactAfter {
		parts = append(parts, "redact-after (the remedy §7.1 says is not one)")
	}
	if o.Rollback {
		parts = append(parts, "rollback (plaintext never committed)")
	}
	if o.LeakAt != SurfaceNone {
		parts = append(parts, "leak-at="+string(o.LeakAt))
	}
	return strings.Join(parts, ", ")
}

// Leak writes value to the named surface and returns a description of where it went.
//
// The value is passed as a secret.Unresolved so that a caller cannot reach this function with a
// plaintext it obtained some other way: the only plaintext in the program lives in that type, and
// unwrapping it here is deliberate and greppable.
func Leak(ctx context.Context, s Surface, runRoot string, db *sql.DB, value secret.Unresolved, j *journal.Journal) (string, error) {
	if s == SurfaceNone {
		return "", nil
	}
	if runRoot == "" {
		return "", errors.New("control: a leak needs a run root to write into")
	}

	where, err := leak(ctx, s, runRoot, db, value)
	if err != nil {
		return "", err
	}

	// The leak is journalled as a write, with the digest of what was written, so that verify-order
	// sees it as a persistence event like any other. A control that did not appear in the journal
	// would leave the ordering check unable to tell a leaking run from a clean one.
	if j != nil {
		if _, err := j.Append(journal.Record{
			Event:         journal.EventWrite,
			Surface:       string(s),
			PayloadSHA256: value.Digest(),
			Detail:        "deliberate control leak to " + where,
		}); err != nil {
			return where, fmt.Errorf("control: journalling the leak to %s: %w", where, err)
		}
	}
	return where, nil
}

// leak is the surface switch, separated so Leak's journalling is not repeated per case.
func leak(ctx context.Context, s Surface, runRoot string, db *sql.DB, value secret.Unresolved) (string, error) {
	plaintext := value.Unsafe()

	switch s {
	case SurfaceTempFile:
		return writeUnder(runRoot, filepath.Join("tmp", "effective-config.yaml"),
			"# deliberate control: the observed configuration written to a working file\n"+string(plaintext)+"\n")

	case SurfaceStaging:
		return writeUnder(runRoot, filepath.Join("staging", "pending.yaml"),
			"# deliberate control: a pending change staged as a plaintext file\n"+string(plaintext)+"\n")

	case SurfaceAppLog:
		return appendUnder(runRoot, "e1.log",
			"level=debug msg=\"deliberate control: applying configuration\" config="+string(plaintext)+"\n")

	case SurfaceErrorReport:
		return writeUnder(runRoot, "error-report.txt",
			"deliberate control: validation failed\n"+
				"the offending value was: "+string(plaintext)+"\n"+
				"(quoting the value that caused a failure is how most leaks of this kind happen)\n")

	case SurfaceDBLog:
		if db == nil {
			return "", errors.New("control: the db-log surface needs a database connection")
		}
		// Statement logging is off in this fixture (log_statement is 'none'), so the setting is
		// turned on for this one connection first. Without that the control comes back clean because
		// the surface was switched off, which reads in a bundle exactly like the surface being
		// unobservable — and a control that cannot fail is not a control.
		//
		// One connection, not the pool: SET applies to the session it ran in, and the pool is free
		// to hand the statement below to a different connection. It is reset before the connection
		// goes back, so nothing else in the run is logged in full.
		conn, err := db.Conn(ctx)
		if err != nil {
			return "", fmt.Errorf("control: taking a connection for the statement log: %w", err)
		}
		defer func() {
			// Best effort: the run is about to end, and a failure to reset would only mean more
			// logging than intended, never less.
			_, _ = conn.ExecContext(ctx, "RESET log_statement")
			_, _ = conn.ExecContext(ctx, "RESET standard_conforming_strings")
			_ = conn.Close()
		}()
		if _, err := conn.ExecContext(ctx, "SET log_statement = 'all'"); err != nil {
			return "", fmt.Errorf("control: enabling statement logging: %w", err)
		}
		// Doubling single quotes is the complete escaping rule for a standard-conforming literal, and
		// only for one: with standard_conforming_strings off, a backslash in the value would escape
		// the closing quote and change the statement. It is set here, on this connection, rather than
		// assumed from the server default.
		if _, err := conn.ExecContext(ctx, "SET standard_conforming_strings = on"); err != nil {
			return "", fmt.Errorf("control: making string literals standard-conforming: %w", err)
		}
		// The value goes in the statement text, not in a parameter: the text is what log_statement
		// records, and a parameter would be recorded separately or not at all.
		stmt := "SELECT '" + strings.ReplaceAll(string(plaintext), "'", "''") + "'::text AS deliberate_control"
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return "", fmt.Errorf("control: leaking to the statement log: %w", err)
		}
		return "the postgres statement log", nil

	case SurfaceTransformed:
		// base64 and nothing else: the point is that the value is present and unreadable to a scan
		// that matches literal strings. A stronger transformation would prove the same thing and
		// invite the objection that the value was really gone.
		return writeUnder(runRoot, "transformed-control.txt",
			"deliberate control, expected to produce ZERO leak-scan hits:\n"+
				base64.StdEncoding.EncodeToString(plaintext)+"\n")

	default:
		return "", fmt.Errorf("control: unknown leak surface %q", s)
	}
}

// writeUnder writes a file under the run root, creating its directory.
func writeUnder(runRoot, rel, body string) (string, error) {
	path := filepath.Join(runRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("control: preparing %s: %w", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("control: writing %s: %w", rel, err)
	}
	return path, nil
}

// appendUnder appends to a file under the run root, creating it and its directory.
func appendUnder(runRoot, rel, body string) (string, error) {
	path := filepath.Join(runRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("control: preparing %s: %w", rel, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("control: opening %s: %w", rel, err)
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return "", fmt.Errorf("control: appending to %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("control: closing %s: %w", rel, err)
	}
	return path, nil
}

// PersistFirst writes the observed configuration as an ordinary plaintext draft, with a plaintext
// parsed index beside it, before anything has been extracted. This is the design design §7.1
// forbids, implemented so that the instrument can be shown to detect it.
//
// It takes raw bytes rather than a secret.Sanitized precisely because it must not go through the
// type contract the honest path uses. store.PersistDraft cannot express this, by construction, and
// that is the structural argument the report rests on: the forbidden design had to be written in a
// separate package, against the database handle directly, because the ordinary persistence function
// will not accept a document that did not come from extraction.
//
// With opts.Rollback the transaction is aborted instead of committed, which is its own control:
// the plaintext is in the heap and the write-ahead log regardless.
func PersistFirst(ctx context.Context, db *sql.DB, runID, source string, body []byte, index map[string]string, j *journal.Journal, opts Options, ctrl *checkpoint.Control) error {
	if db == nil {
		return errors.New("control: persist-first needs a database connection")
	}
	if runID == "" {
		return errors.New("control: persist-first needs a run id")
	}
	if j == nil {
		return errors.New("control: persist-first needs a journal; a control that recorded nothing " +
			"would be indistinguishable from a run that never happened")
	}

	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	// Journalled with no extraction digests, because nothing was extracted. That is what makes
	// verify-order fail on these runs: a write naming a payload whose secrets have no earlier
	// extraction record is the violation the check exists to find.
	if _, err := j.Append(journal.Record{
		Event:         journal.EventWrite,
		Surface:       "machine_draft (plaintext, before extraction)",
		PayloadSHA256: digest,
		Detail:        "deliberate control: " + opts.Describe(),
	}); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("control: beginning the plaintext transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // the commit path below is the one that reports.

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO machine_draft (run_id, source, document, document_sha256) VALUES ($1, $2, $3, $4)`,
		runID, source, string(body), digest); err != nil {
		return fmt.Errorf("control: writing the plaintext draft: %w", err)
	}

	// The parsed index is indexed separately by §7.1 because a system can sanitize the document it
	// stores and still index the values it parsed out of the original. Here both carry plaintext.
	for _, path := range sortedKeys(index) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO parsed_index (run_id, path, value) VALUES ($1, $2, $3)
			 ON CONFLICT (run_id, path) DO UPDATE SET value = EXCLUDED.value`,
			runID, path, index[path]); err != nil {
			return fmt.Errorf("control: writing the plaintext parsed index at %s: %w", path, err)
		}
	}

	if ctrl != nil {
		ctrl.Reach(checkpoint.InDBTxn)
	}

	if opts.Rollback {
		if err := tx.Rollback(); err != nil {
			return fmt.Errorf("control: rolling back the plaintext transaction: %w", err)
		}
		if _, err := j.Append(journal.Record{
			Event: journal.EventNote,
			Detail: "deliberate control: the plaintext transaction was rolled back; the rows are gone " +
				"from the live table and the bytes are still in the heap and the write-ahead log",
		}); err != nil {
			return err
		}
		return nil
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("control: committing the plaintext draft: %w", err)
	}
	if ctrl != nil {
		ctrl.Reach(checkpoint.AfterCommit)
	}
	return nil
}

// RedactAfter replaces a committed plaintext draft with its sanitized form, and its plaintext
// parsed index with references. It is the remedy §7.1 rules out, run so that the ruling can be
// measured: after this returns, the live tables are clean and the plaintext is still in the
// write-ahead log and in any snapshot taken before it.
//
// Nothing here removes the earlier bytes, and nothing could. That is the finding.
func RedactAfter(ctx context.Context, db *sql.DB, runID string, s secret.Sanitized, j *journal.Journal) error {
	if db == nil {
		return errors.New("control: redact-after needs a database connection")
	}
	if !s.Valid() {
		return errors.New("control: redact-after needs the sanitized form to replace the draft with")
	}
	if j == nil {
		return errors.New("control: redact-after needs a journal")
	}

	sum := sha256.Sum256(s.Document())
	digest := hex.EncodeToString(sum[:])

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("control: beginning the redaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // the commit path below is the one that reports.

	result, err := tx.ExecContext(ctx,
		`UPDATE machine_draft SET document = $2, document_sha256 = $3 WHERE run_id = $1`,
		runID, string(s.Document()), digest)
	if err != nil {
		return fmt.Errorf("control: redacting the draft: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("control: redacting the draft: %w", err)
	}
	if n == 0 {
		// A redaction that updated nothing would leave a clean scan of the live table and be read
		// as the redaction having worked.
		return fmt.Errorf("control: no draft for run %s to redact; the control did not run", runID)
	}

	// Each index row is checked the way the draft is. A reference whose path matched no row would
	// leave that value's plaintext in the live index while this control reported success, and the
	// result it exists for — nothing in the dump, everything in the write-ahead log — would then
	// rest on a redaction that never happened.
	for _, ref := range s.References() {
		result, err := tx.ExecContext(ctx,
			`UPDATE parsed_index SET value = $3 WHERE run_id = $1 AND path = $2`,
			runID, ref.Path, "bw:ref:"+ref.URI)
		if err != nil {
			return fmt.Errorf("control: redacting the parsed index at %s: %w", ref.Path, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("control: redacting the parsed index at %s: %w", ref.Path, err)
		}
		if n == 0 {
			return fmt.Errorf("control: no parsed-index row for run %s at %s to redact; the control did not run", runID, ref.Path)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("control: committing the redaction: %w", err)
	}

	if _, err := j.Append(journal.Record{
		Event:         journal.EventWrite,
		Surface:       "machine_draft (redacted, after the fact)",
		PayloadSHA256: digest,
		Detail: "deliberate control: the live rows are now clean and the plaintext committed earlier " +
			"remains in the write-ahead log and in any snapshot taken before this update",
	}); err != nil {
		return err
	}
	return nil
}

// sortedKeys gives the index a stable write order, so two runs of the same control produce the same
// sequence of statements and a diff between their bundles is about the data and not the map.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
