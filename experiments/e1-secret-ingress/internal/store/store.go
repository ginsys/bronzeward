// Package store is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It writes the surfaces design §7.1 names as ordinary plaintext persistence — a draft row, a
// parsed index, the reference table and the journal — into the PostgreSQL the investigation
// fixtures stand up. That is the whole reason it is a database rather than a file: a row reaches
// the heap, the write-ahead log and every backup taken afterwards, and that reach is the property
// under test. §7.1's sentence about redacting later is a claim about backups and history, and only
// something that gets into them can test it.
//
// The ordering requirement is enforced by the signature, not by the order of statements here.
// PersistDraft takes a secret.Sanitized, and extract.Run is that type's only constructor, so there
// is no way to call this function with a document that has not been through extraction. The one
// thing Go leaves open is the zero value, which any package can write; it is rejected below.
//
// Nothing here selects a database or a driver for v1. lib/pq is used because it has no transitive
// dependencies, which keeps go.sum auditable in full; that is a property of this experiment's
// evidence, not a recommendation.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/baseline"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

//go:embed schema.sql
var schema string

// Environment and defaults. The fixtures publish the password in their state directory and expose
// PostgreSQL on the host (fixtures/versions.env, POSTGRES_PORT=55432).
const (
	// PasswordEnv is where the password is read from. It is never a flag: argv is world-readable
	// and is captured by the evidence bundles this program's own runs produce.
	PasswordEnv = "BW_POSTGRES_PASSWORD"
	// DSNEnv overrides the whole connection string, for a run against something else.
	DSNEnv = "BW_POSTGRES_DSN"

	defaultHost     = "127.0.0.1"
	defaultPort     = "55432"
	defaultUser     = "bronzeward"
	defaultDatabase = "bronzeward"
)

// DB is a connection to the experiment's database.
type DB struct {
	sql *sql.DB
}

// Open connects and verifies the connection, so a failure is reported here rather than on the
// first write, halfway through a run.
func Open(ctx context.Context, dsn string) (*DB, error) {
	if dsn == "" {
		return nil, errors.New("store: no connection string")
	}
	handle, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening the database: %w", err)
	}
	if err := handle.PingContext(ctx); err != nil {
		handle.Close()
		// A DSN carries the password. It must not reach an error string, which ends up in a log.
		return nil, fmt.Errorf("store: connecting: %w", redactDSN(err, dsn))
	}
	return &DB{sql: handle}, nil
}

// OpenFromEnv builds the DSN from the fixtures' published password, or takes one whole from the
// environment.
func OpenFromEnv(ctx context.Context) (*DB, error) {
	if dsn := os.Getenv(DSNEnv); dsn != "" {
		return Open(ctx, dsn)
	}
	password := os.Getenv(PasswordEnv)
	if password == "" {
		return nil, fmt.Errorf("store: %s is not set; the fixtures publish it in their state directory", PasswordEnv)
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		defaultHost, defaultPort, defaultUser, password, defaultDatabase)
	return Open(ctx, dsn)
}

// Close releases the connection.
func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

// EnsureSchema applies schema.sql. Every statement in it is idempotent, so a second run is a
// no-op rather than an error, and there is no migration framework to get wrong.
func (db *DB) EnsureSchema(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("store: applying the schema: %w", err)
	}
	return nil
}

// Draft is one ingestion's persistable result.
type Draft struct {
	// RunID identifies the run.
	RunID string
	// Source names where the configuration came from, for the report: an import or a drift
	// adoption, and which file.
	Source string
	// Sanitized is the document. Its type is the guarantee: there is no way to build one without
	// going through extraction.
	Sanitized secret.Sanitized
	// Digests are every secret extracted from the source, which each write record names so that
	// verification can assert the write came after all of them.
	Digests []string
}

// PersistDraft writes the draft, its parsed index and its references in one transaction.
//
// The checkpoint is reached inside the transaction, before COMMIT. A crash there leaves an aborted
// transaction whose bytes may still be in the heap and the write-ahead log, which is exactly the
// case §7.1's prohibition is about: the cleanup path did not run, and what is on disk is what a
// backup would capture.
func (db *DB) PersistDraft(ctx context.Context, d Draft, j *journal.Journal, ctrl *checkpoint.Control) error {
	switch {
	case d.RunID == "":
		return errors.New("store: no run id")
	case j == nil:
		return errors.New("store: no journal; an unrecorded write cannot be shown to have come after extraction")
	case !d.Sanitized.Valid():
		// The zero Sanitized is the one thing the type system cannot stop a caller writing. It is
		// the only route by which an unextracted document could reach this function.
		return errors.New("store: the document did not come from extraction; refusing to persist it")
	}

	body := d.Sanitized.Document()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// Recorded before the transaction opens, so that a crash inside it still leaves a journal
	// entry naming what was about to be written. A record written after COMMIT would be missing
	// from precisely the runs the crash controls exist to produce.
	if _, err := j.Append(journal.Record{
		Event:         journal.EventWrite,
		Checkpoint:    checkpoint.InDBTxn.String(),
		Surface:       "machine_draft.document",
		PayloadSHA256: digest,
		Digests:       d.Digests,
		Detail:        d.Source,
	}); err != nil {
		return fmt.Errorf("store: recording the draft write: %w", err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this is safe unconditionally and covers
	// every early return below.
	defer tx.Rollback() //nolint:errcheck // the deferred rollback's error is not actionable

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO machine_draft (run_id, source, document, document_sha256) VALUES ($1, $2, $3, $4)`,
		d.RunID, d.Source, string(body), digest); err != nil {
		return fmt.Errorf("store: inserting the draft: %w", err)
	}

	if err := insertParsedIndex(ctx, tx, d.RunID, body); err != nil {
		return err
	}

	for _, ref := range d.Sanitized.References() {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO secret_reference (run_id, path, uri, digest) VALUES ($1, $2, $3, $4)`,
			d.RunID, ref.Path, ref.URI, ref.Digest); err != nil {
			return fmt.Errorf("store: inserting the reference for %s: %w", ref.Path, err)
		}
	}

	if err := appendJournal(ctx, tx, j.Records()); err != nil {
		return err
	}

	// Inside the transaction, before COMMIT.
	if ctrl != nil {
		ctrl.Reach(checkpoint.InDBTxn)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing: %w", err)
	}

	if _, err := j.Append(journal.Record{
		Event:      journal.EventNote,
		Checkpoint: checkpoint.AfterCommit.String(),
		Surface:    "machine_draft",
		Detail:     "committed",
	}); err != nil {
		return fmt.Errorf("store: recording the commit: %w", err)
	}
	if ctrl != nil {
		ctrl.Reach(checkpoint.AfterCommit)
	}
	return nil
}

// insertParsedIndex writes one row per scalar in the sanitized document.
//
// §7.1 names the parsed index separately from the draft, because a system can sanitize the
// document it stores and still index the values it parsed out of the original. Here the index is
// built from the sanitized bytes for exactly that reason: it is the same text, so a reference in
// the draft is a reference in the index, and an experiment that indexed the original would be
// demonstrating the forbidden design rather than the required one.
func insertParsedIndex(ctx context.Context, tx *sql.Tx, runID string, body []byte) error {
	d, err := document.Load(body)
	if err != nil {
		return fmt.Errorf("store: re-parsing the sanitized document for the index: %w", err)
	}
	for _, path := range d.Paths() {
		value, ok := d.Get(path)
		if !ok {
			return fmt.Errorf("store: %s vanished between listing and reading", path)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO parsed_index (run_id, path, value) VALUES ($1, $2, $3)
			 ON CONFLICT (run_id, path) DO UPDATE SET value = EXCLUDED.value`,
			runID, path, value); err != nil {
			return fmt.Errorf("store: indexing %s: %w", path, err)
		}
	}
	return nil
}

// appendJournal mirrors the on-disk journal into the database, letting PostgreSQL stamp its own
// clock and write-ahead log position on each row. Those two columns are observers the prototype
// does not supply and cannot backdate, which is the only reason to duplicate the journal at all.
func appendJournal(ctx context.Context, tx *sql.Tx, records []journal.Record) error {
	for _, r := range records {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ingest_journal
			   (run_id, seq, event, checkpoint, surface, payload_sha256, secret_digests, detail, wall, mono_ns)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (run_id, seq) DO NOTHING`,
			r.RunID, r.Seq, r.Event, nullable(r.Checkpoint), nullable(r.Surface),
			nullable(r.PayloadSHA256), pq.Array(r.Digests), nullable(r.Detail),
			r.Wall, r.MonoNanos); err != nil {
			return fmt.Errorf("store: mirroring journal record %d: %w", r.Seq, err)
		}
	}
	return nil
}

// SaveBaseline records the encrypted baseline beside the draft, so that it reaches the write-ahead
// log and the backups §7.1's prohibition is about — as ciphertext, which is the permitted form.
func (db *DB) SaveBaseline(ctx context.Context, b baseline.Baseline) error {
	if b.Ciphertext == "" || b.InputSHA256 == "" || b.RunID == "" {
		return errors.New("store: the baseline is incomplete; refusing to record it as retained")
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO encrypted_baseline (run_id, key_name, input_sha256, input_bytes, ciphertext)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (run_id) DO UPDATE
		   SET key_name = EXCLUDED.key_name, input_sha256 = EXCLUDED.input_sha256,
		       input_bytes = EXCLUDED.input_bytes, ciphertext = EXCLUDED.ciphertext`,
		b.RunID, b.KeyName, b.InputSHA256, b.InputBytes, b.Ciphertext); err != nil {
		return fmt.Errorf("store: recording the baseline: %w", err)
	}
	return nil
}

// Observation is what the server saw, as opposed to what the prototype recorded.
type Observation struct {
	// Clock is the server's clock_timestamp().
	Clock time.Time
	// LSN is the current write-ahead log position, which advances with every write and cannot be
	// made to go backwards.
	LSN string
}

// Observe reads the server's own clock and write-ahead log position. It is one of the three
// observers that are not the prototype, and the report pairs it against the journal's timestamps.
func (db *DB) Observe(ctx context.Context) (Observation, error) {
	var o Observation
	if err := db.sql.QueryRowContext(ctx,
		`SELECT clock_timestamp(), pg_current_wal_lsn()::text`).Scan(&o.Clock, &o.LSN); err != nil {
		return Observation{}, fmt.Errorf("store: reading the server's clock and log position: %w", err)
	}
	return o, nil
}

// Checkpoint forces a flush of everything in shared buffers to the data directory.
//
// The experiment captures each control both with and without this, so that a clean scan of the
// data directory on the honest path cannot be dismissed as "not flushed yet" and a leak found
// after it cannot be dismissed as "only in memory".
func (db *DB) Checkpoint(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, `CHECKPOINT`); err != nil {
		return fmt.Errorf("store: forcing a checkpoint: %w", err)
	}
	return nil
}

// nullable turns an empty string into a SQL NULL, so an absent field is absent rather than an
// empty string that a later query would have to know to treat as missing.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// redactDSN removes the password from an error that quoted the connection string. lib/pq does not
// normally include it, and relying on that would make this function's absence a silent dependency
// on another project's error formatting.
func redactDSN(err error, dsn string) error {
	text := err.Error()
	for _, field := range strings.Fields(dsn) {
		value, found := strings.CutPrefix(field, "password=")
		if !found || value == "" {
			continue
		}
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	if text == err.Error() {
		return err
	}
	return errors.New(text)
}
