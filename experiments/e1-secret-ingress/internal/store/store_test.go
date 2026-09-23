package store

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/baseline"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
	"github.com/lib/pq"
)

// These tests cover what can be established without a live PostgreSQL: the guards that run before
// any statement is sent, and the schema's own shape. The writes themselves are exercised by the
// experiment's matrix run against the investigation fixtures, which is also the only place they
// could be exercised meaningfully — the whole point of using a database is that the bytes reach
// the heap, the write-ahead log and the backups, and no in-process substitute has those.

func openJournal(t *testing.T) *journal.Journal {
	t.Helper()
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

// TestPersistDraftRefusesAnUnextractedDocument is the guard on the zero value. Building a
// secret.Sanitized around a raw configuration is kept out by the secret package's call-site test,
// not by the compiler; the zero value is the gap that test cannot see, since any package can write
// it without calling anything, and that is what this rejects.
//
// It runs before any statement is sent, so a nil database is enough to prove the order.
func TestPersistDraftRefusesAnUnextractedDocument(t *testing.T) {
	db := &DB{}
	j := openJournal(t)

	err := db.PersistDraft(context.Background(), Draft{
		RunID:     "run-test",
		Source:    "import",
		Sanitized: secret.Sanitized{}, // the zero value: never went through extraction
	}, j, nil)

	if err == nil {
		t.Fatal("PersistDraft accepted a document that did not come from extraction")
	}
	if !strings.Contains(err.Error(), "did not come from extraction") {
		t.Errorf("the error does not explain the refusal: %v", err)
	}
	// Nothing may have been journalled either: a write record for a write that was refused would
	// make the journal claim a persistence that never happened.
	if len(j.Records()) != 0 {
		t.Errorf("the refused write left %d journal records", len(j.Records()))
	}
}

// TestPersistDraftRequiresARunIDAndAJournal covers the other two guards, both of which run before
// any statement is sent.
func TestPersistDraftRequiresARunIDAndAJournal(t *testing.T) {
	db := &DB{}
	valid := secret.NewSanitized([]byte("machine: {}\n"), nil)

	if err := db.PersistDraft(context.Background(), Draft{Sanitized: valid}, openJournal(t), nil); err == nil {
		t.Error("PersistDraft accepted a draft with no run id")
	}
	if err := db.PersistDraft(context.Background(), Draft{RunID: "r", Sanitized: valid}, nil, nil); err == nil {
		t.Error("PersistDraft accepted a draft with no journal")
	} else if !strings.Contains(err.Error(), "after extraction") {
		t.Errorf("the error does not say why a journal is required: %v", err)
	}
}

// TestSaveBaselineRefusesAnIncompleteRecord checks the database is not told a configuration was
// retained when the record cannot establish that it was.
func TestSaveBaselineRefusesAnIncompleteRecord(t *testing.T) {
	db := &DB{}
	for name, b := range map[string]baseline.Baseline{
		"empty":           {},
		"no ciphertext":   {RunID: "r", InputSHA256: "abc"},
		"no input digest": {RunID: "r", Ciphertext: "vault:v1:x"},
		"no run id":       {InputSHA256: "abc", Ciphertext: "vault:v1:x"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.SaveBaseline(context.Background(), b); err == nil {
				t.Fatal("SaveBaseline accepted an incomplete record")
			}
		})
	}
}

// TestOpenRejectsAnEmptyDSN checks the failure happens at connection time rather than on the first
// write, halfway through a run.
func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Error("Open accepted an empty connection string")
	}
}

// TestOpenFromEnvNamesTheMissingVariable checks an operator is told which variable to set rather
// than being handed a connection refusal.
func TestOpenFromEnvNamesTheMissingVariable(t *testing.T) {
	t.Setenv(DSNEnv, "")
	t.Setenv(PasswordEnv, "")

	_, err := OpenFromEnv(context.Background())
	if err == nil {
		t.Fatal("OpenFromEnv succeeded with no password set")
	}
	if !strings.Contains(err.Error(), PasswordEnv) {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

// TestRedactDSNRemovesThePassword covers the guard against a connection error quoting the
// credential. lib/pq does not normally include it, and depending on that would make this
// function's absence a silent dependency on another project's error formatting.
func TestRedactDSNRemovesThePassword(t *testing.T) {
	const password = "E1-DSN-PASSWORD-MUST-NOT-APPEAR"
	dsn := "host=127.0.0.1 port=55432 user=bronzeward password=" + password + " dbname=bronzeward sslmode=disable"

	leaky := errors.New("dial failed for " + dsn)
	got := redactDSN(leaky, dsn)

	if strings.Contains(got.Error(), password) {
		t.Errorf("the password survived redaction: %v", got)
	}
	if !strings.Contains(got.Error(), "[redacted]") {
		t.Errorf("the redaction marker is missing: %v", got)
	}
	// The rest must survive, or the error stops being actionable.
	if !strings.Contains(got.Error(), "host=127.0.0.1") {
		t.Errorf("redaction removed more than the password: %v", got)
	}

	// An error that never held the password is returned unchanged, so the common case allocates
	// nothing and the original error's type and wrapping are preserved.
	plain := errors.New("connection refused")
	if redactDSN(plain, dsn) != plain {
		t.Error("an error without the password was rewritten anyway")
	}
}

// TestFixtureDSNQuotesThePassword checks the password lib/pq's own parser reads out of the built
// connection string is the one that went in, for the characters bare interpolation broke on. The
// parsed value is read from the connector by reflection, because lib/pq exposes no accessor; that
// is the parser the connection uses, rather than this package's copy of its rules.
func TestFixtureDSNQuotesThePassword(t *testing.T) {
	for _, password := range []string{
		"BWSYNTH-plain",
		"has a space",
		"it's quoted",
		`back\slash`,
		`\' both`,
	} {
		dsn := fixtureDSN(password)
		c, err := pq.NewConnector(dsn)
		if err != nil {
			t.Errorf("lib/pq refused the DSN for %q: %v", password, err)
			continue
		}
		got := reflect.ValueOf(c).Elem().FieldByName("opts").MapIndex(reflect.ValueOf("password"))
		if !got.IsValid() || got.String() != password {
			t.Errorf("lib/pq read the password %q back as %v", password, got)
		}
		if pws := dsnPasswords(dsn); len(pws) == 0 || pws[0] != password {
			t.Errorf("dsnPasswords found %q for %q, so redaction would miss it", pws, password)
		}
	}
}

// TestRedactDSNCoversEveryFormLibPQAccepts extends the redaction to the shapes the first version
// missed. DSNEnv replaces the whole connection string, so an operator may supply either form, and a
// URL-form password used to reach the returned error untouched.
func TestRedactDSNCoversEveryFormLibPQAccepts(t *testing.T) {
	const password = "E1-DSN p@ss/word-MUST-NOT-APPEAR"
	for name, dsn := range map[string]string{
		"url userinfo":  "postgres://bronzeward:" + url.PathEscape(password) + "@127.0.0.1:55432/bronzeward?sslmode=disable",
		"url query":     "postgresql://127.0.0.1:55432/bronzeward?user=bronzeward&password=" + url.QueryEscape(password),
		"quoted keyval": "host=127.0.0.1 user=bronzeward password='" + password + "' dbname=bronzeward",
	} {
		t.Run(name, func(t *testing.T) {
			// The error quotes both the raw password and the DSN as given, so each spelling a
			// driver could plausibly echo is present.
			leaky := errors.New("dial failed for " + dsn + " using " + password)
			got := redactDSN(leaky, dsn).Error()
			for _, spelling := range []string{password, url.PathEscape(password), url.QueryEscape(password)} {
				if strings.Contains(got, spelling) {
					t.Errorf("the password survived redaction as %q: %s", spelling, got)
				}
			}
			if !strings.Contains(got, "127.0.0.1") {
				t.Errorf("redaction removed more than the password: %s", got)
			}
		})
	}

	// lib/pq's escaping and spacing rules. Each DSN spells the password foo'bar or "foo bar" in a
	// way the first scanner misread; both the value and its raw spelling must be found.
	for dsn, want := range map[string][]string{
		`host=h password='foo\'bar' dbname=d`:  {`foo'bar`, `'foo\'bar'`},
		`host=h password = 'foo bar' dbname=d`: {`foo bar`, `'foo bar'`},
		`host=h password=foo\ bar dbname=d`:    {`foo bar`, `foo\ bar`},
		// A malformed token before the password: the scan used to stop there and find nothing.
		`host=h badtoken password=foo-bar dbname=d`: {`foo-bar`},
		`badtoken`: nil,
	} {
		got := dsnPasswords(dsn)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("dsnPasswords(%q) = %q, want %q", dsn, got, want)
		}
		leaky := errors.New("bad connection string " + dsn)
		if text := redactDSN(leaky, dsn).Error(); strings.Contains(text, "foo") {
			t.Errorf("the password survived redaction of %q: %s", dsn, text)
		}
	}

	// A URL-form DSN net/url refuses: the raw password holds a space and a '#', so url.Parse fails
	// and the first version returned no password at all for exactly the DSN most likely to be
	// quoted back whole.
	const raw = "p#ss word"
	broken := "postgres://bronzeward:" + raw + "@127.0.0.1:55432/bronzeward"
	if _, err := url.Parse(broken); err == nil {
		t.Fatalf("the test's DSN parses, so it does not exercise the fallback: %q", broken)
	}
	if text := redactDSN(errors.New("cannot parse "+broken), broken).Error(); strings.Contains(text, raw) {
		t.Errorf("an unparseable URL DSN's password survived redaction: %s", text)
	}

	// sslpassword= is a different key. Treating its value as the password would not leak anything,
	// but it would show the scan matching a key by suffix, which is how it would miss one too.
	if got := dsnPasswords("host=h sslpassword=keyphrase dbname=d"); len(got) != 0 {
		t.Errorf("dsnPasswords took sslpassword= for password=: %q", got)
	}
}

// TestRedactDSNHandlesADSNWithoutAPassword checks the degenerate input does not panic or produce a
// nonsense replacement.
func TestRedactDSNHandlesADSNWithoutAPassword(t *testing.T) {
	err := errors.New("connection refused")
	for _, dsn := range []string{"", "host=127.0.0.1 dbname=bronzeward", "password="} {
		if got := redactDSN(err, dsn); got != err {
			t.Errorf("redactDSN with dsn %q rewrote an error it should not have: %v", dsn, got)
		}
	}
}

// TestSchemaCoversEverySurfaceTheDesignNames checks the embedded schema actually creates the
// tables the boundary map refers to. A missing table would make one surface silently unmeasured,
// and a leak scan finding nothing there would be reported as clean.
func TestSchemaCoversEverySurfaceTheDesignNames(t *testing.T) {
	if schema == "" {
		t.Fatal("schema.sql was not embedded")
	}
	for _, table := range []string{
		"machine_draft",      // the ordinary plaintext draft
		"parsed_index",       // the parsed index, named separately by §7.1
		"secret_reference",   // where each secret went
		"ingest_journal",     // the ordering journal, with the server's own clock and log position
		"encrypted_baseline", // the permitted form of retaining the observed configuration
	} {
		if !strings.Contains(schema, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("schema.sql does not create %s", table)
		}
	}

	// Idempotence is what stands in for a migration framework here, so every statement must carry
	// the guard rather than most of them.
	if n := strings.Count(schema, "CREATE TABLE"); n != strings.Count(schema, "CREATE TABLE IF NOT EXISTS") {
		t.Errorf("%d CREATE TABLE statements but only %d are idempotent",
			n, strings.Count(schema, "CREATE TABLE IF NOT EXISTS"))
	}

	// The journal's server-side columns are the reason the journal is mirrored into the database
	// at all. now() would be the transaction's start time, which the prototype's own clock could
	// silently agree with; clock_timestamp() is read when the row is written.
	//
	// Matched on the column name and the function rather than on the whole line, so that
	// realigning the SQL does not fail a test about semantics.
	for column, function := range map[string]string{
		"server_clock": "clock_timestamp()",
		"server_lsn":   "pg_current_wal_lsn()",
	} {
		line, ok := lineWith(schema, column)
		if !ok {
			t.Errorf("ingest_journal has no %s column", column)
			continue
		}
		if !strings.Contains(line, "DEFAULT "+function) {
			t.Errorf("%s does not default to %s: %q", column, function, line)
		}
		if !strings.Contains(line, "NOT NULL") {
			t.Errorf("%s is nullable, so a row could carry no observation at all: %q", column, line)
		}
	}
	if strings.Contains(schema, "DEFAULT now()") {
		t.Error("the schema uses now(), which is the transaction's start time rather than an independent observation")
	}
}

// lineWith returns the first line of body containing needle.
func lineWith(body, needle string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			return line, true
		}
	}
	return "", false
}
