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
// The ordering requirement is carried by the signature, not by the order of statements here.
// PersistDraft takes a secret.Sanitized, which only extraction and staging's resume path construct.
// That restriction is a test, not the compiler: the constructor is exported because Go cannot
// scope a function to one sibling package, and secret's TestNewSanitizedHasNoUnexpectedCallers is
// what fails if anything else calls it. The zero value is the other gap; it is rejected below.
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
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

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
		// lib/pq v1.10.9 does not parse the DSN here — it implements no OpenConnector, so parsing
		// waits for the first connection, which is redacted below. Redacted here as well so that
		// a driver version that does parse at Open cannot put the password in this error.
		return nil, fmt.Errorf("store: opening the database: %w", redactDSN(err, dsn))
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
	return Open(ctx, fixtureDSN(password))
}

// fixtureDSN is the key/value connection string for the fixtures' database. The password is
// single-quoted with ' and \ backslash-escaped, the form lib/pq's parser reads back exactly.
// Interpolated bare, a password with a space, quote or backslash was mis-parsed, and the parse
// error quoted a remainder of it that redactDSN could not recognise as the password.
func fixtureDSN(password string) string {
	quoted := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(password)
	return fmt.Sprintf("host=%s port=%s user=%s password='%s' dbname=%s sslmode=disable",
		defaultHost, defaultPort, defaultUser, quoted, defaultDatabase)
}

// SQL exposes the handle for the staging package, which owns its own table and its own statements
// but must share this connection: a claim and the draft it becomes have to be visible to each
// other, and two pools would make the experiment's ordering depend on which one a statement
// happened to use.
func (db *DB) SQL() *sql.DB { return db.sql }

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
	// Sanitized is the document. Its type carries the guarantee, and a call-site test in the secret
	// package is what keeps anything but extraction and staging from building one.
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
		// The zero Sanitized can be written by any package without calling the constructor, so the
		// call-site test on NewSanitized cannot see it. This is where it is stopped.
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
		// Past the commit the rows exist whatever happens next, so this failure must not read like
		// the ones above, after which nothing was persisted.
		return fmt.Errorf("%w: %w", ErrCommittedUnrecorded, err)
	}
	if ctrl != nil {
		ctrl.Reach(checkpoint.AfterCommit)
	}
	return nil
}

// ErrCommittedUnrecorded is PersistDraft's one failure after which the draft, its parsed index and
// its references are committed: the journal note recording the commit could not be written. Every
// other error from PersistDraft means nothing was persisted. The note cannot share the
// transaction, because the journal is a file.
var ErrCommittedUnrecorded = errors.New("store: the draft is committed, but the commit could not be recorded in the journal")

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
		// A nil slice reaches PostgreSQL as NULL, and a supplied NULL overrides the column's
		// DEFAULT '{}' rather than falling back to it — so the NOT NULL constraint rejects every
		// record that lists no digest, which is most of them. The empty array is passed explicitly.
		digests := r.Digests
		if digests == nil {
			digests = []string{}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ingest_journal
			   (run_id, seq, event, checkpoint, surface, payload_sha256, secret_digests, detail, wall, mono_ns)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (run_id, seq) DO NOTHING`,
			r.RunID, r.Seq, r.Event, nullable(r.Checkpoint), nullable(r.Surface),
			nullable(r.PayloadSHA256), pq.Array(digests), nullable(r.Detail),
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
//
// Both forms lib/pq accepts are covered, since DSNEnv overrides the whole connection string: the
// key/value form, where the password is a password= field and may be single-quoted, and the URL
// form, where it is the userinfo password or a password query parameter. The first version only
// knew unquoted key/value fields, so a URL-form DSN's password survived into the error unredacted.
// Each value is also replaced in its URL-escaped spelling, which is how an error quoting the URL
// would carry it.
func redactDSN(err error, dsn string) error {
	text := err.Error()
	for _, value := range dsnPasswords(dsn) {
		text = strings.ReplaceAll(text, value, "[redacted]")
		if escaped := url.QueryEscape(value); escaped != value {
			text = strings.ReplaceAll(text, escaped, "[redacted]")
		}
		if escaped := url.PathEscape(value); escaped != value {
			text = strings.ReplaceAll(text, escaped, "[redacted]")
		}
	}
	if text == err.Error() {
		return err
	}
	return errors.New(text)
}

// roughURLPasswords extracts passwords from a URL-form DSN that net/url cannot parse — typically
// because the password itself holds a '/', '?', '#' or space. It over-collects rather than
// under-collects: a redaction that also blanks a fragment of the host is harmless, a missed
// password is a leak.
func roughURLPasswords(dsn string) []string {
	var out []string
	rest := dsn[strings.Index(dsn, "://")+len("://"):]
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		if _, pw, found := strings.Cut(rest[:at], ":"); found && pw != "" {
			out = append(out, pw)
			if decoded, err := url.PathUnescape(pw); err == nil && decoded != pw {
				out = append(out, decoded)
			}
		}
	}
	if _, q, found := strings.Cut(rest, "?"); found {
		out = append(out, queryPasswords(q)...)
	}
	return out
}

// queryPasswords reads every password= value from a raw query string as text, both as written
// and unescaped. url.Values is not used: its parser silently drops a pair whose value holds an
// invalid percent escape, and url.Parse does not validate the query, so such a password reached
// neither this function's caller nor the rough fallback and was left unredacted.
func queryPasswords(rawQuery string) []string {
	var out []string
	for _, kv := range strings.Split(rawQuery, "&") {
		if pw, ok := strings.CutPrefix(kv, "password="); ok && pw != "" {
			out = append(out, pw)
			if decoded, err := url.QueryUnescape(pw); err == nil && decoded != pw {
				out = append(out, decoded)
			}
		}
	}
	return out
}

// dsnPasswords returns every password a connection string carries, in either lib/pq form.
func dsnPasswords(dsn string) []string {
	var out []string
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			// A DSN net/url refuses is the case most likely to be quoted back whole in an error,
			// so giving up here left exactly that one unredacted. The userinfo is recovered by
			// hand instead: everything after the scheme up to the last '@', and after its first ':'.
			return roughURLPasswords(dsn)
		}
		if pw, ok := u.User.Password(); ok && pw != "" {
			out = append(out, pw)
		}
		return append(out, queryPasswords(u.RawQuery)...)
	}
	// Key/value form, scanned the way lib/pq's parseOpts scans it (conn.go in v1.10.9): whitespace
	// may surround '=', a value may be single-quoted, and a backslash escapes the next character in
	// either form. The first version split on whitespace and knew no escapes, so for
	// password='foo\'bar' it found foo\ and let the real password through.
	//
	// Both spellings of each password are returned: the value lib/pq uses, and the raw text it was
	// written as, since an error that quotes the connection string carries the raw one.
	rs := []rune(dsn)
	i := 0
	skip := func() {
		for i < len(rs) && unicode.IsSpace(rs[i]) {
			i++
		}
	}
	for {
		skip()
		if i >= len(rs) {
			return out
		}
		start := i
		for i < len(rs) && rs[i] != '=' && !unicode.IsSpace(rs[i]) {
			i++
		}
		key := string(rs[start:i])
		skip()
		if i >= len(rs) || rs[i] != '=' {
			// Malformed. lib/pq refuses the string, and its error may quote the whole of it, so the
			// scan carries on from the next token rather than stopping: returning here left every
			// password written after the bad token unredacted.
			continue
		}
		i++
		skip()
		rawStart := i
		var value []rune
		if i < len(rs) && rs[i] == '\'' {
			i++
			for i < len(rs) && rs[i] != '\'' {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				value = append(value, rs[i])
				i++
			}
			if i < len(rs) {
				i++ // the closing quote
			}
		} else {
			for i < len(rs) && !unicode.IsSpace(rs[i]) {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				value = append(value, rs[i])
				i++
			}
		}
		if key == "password" && len(value) > 0 {
			out = append(out, string(value))
			if raw := string(rs[rawStart:i]); raw != string(value) {
				out = append(out, raw)
			}
		}
	}
}
