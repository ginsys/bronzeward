package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
)

func installed(t *testing.T) (*sql.DB, []Migration) {
	t.Helper()
	db, ms := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db, ms
}

func wantCheck(t *testing.T, db *sql.DB, ms []Migration, parts ...string) {
	t.Helper()
	err := Check(context.Background(), db, ms)
	if err == nil {
		t.Fatalf("Check passed; want an error with %q", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("Check: %v; want it to contain %q", err, p)
		}
	}
}

func TestCheckPasses(t *testing.T) {
	db, ms := installed(t)
	if err := Check(context.Background(), db, ms); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNoSchema(t *testing.T) {
	db, _ := dbtest.New(t)
	ms, _ := Embedded()
	wantCheck(t, db, ms, "run bronzeward migrate")
}

func TestCheckNotInstalled(t *testing.T) {
	db, ms := migrated(t)
	wantCheck(t, db, ms, "no installation", "run bronzeward migrate")
}

func TestCheckOlderSchema(t *testing.T) {
	db, ms := installed(t)
	next := len(ms) + 1
	newer := append(ms, mig(next, "CREATE TABLE extra (x integer)"))
	wantCheck(t, db, newer, "["+strconv.Itoa(next)+"]", "older", "run bronzeward migrate")
}

func TestCheckNewerSchema(t *testing.T) {
	db, ms := installed(t)
	next := len(ms) + 1
	if _, err := Apply(context.Background(), db, append(ms, mig(next, "CREATE TABLE extra (x integer)"))); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, db, ms, "["+strconv.Itoa(next)+"]", "newer")
}

// Migrate stopped after Apply, before Install: the migrations are all there, schema_version is not.
func TestCheckStaleSchemaVersion(t *testing.T) {
	db, ms := installed(t)
	newer := append(ms, mig(len(ms)+1, "CREATE TABLE extra (x integer)"))
	if _, err := Apply(context.Background(), db, newer); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, db, newer, "schema version "+strconv.Itoa(len(ms)), "run bronzeward migrate")
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), db, newer); err != nil {
		t.Fatalf("after Install: %v", err)
	}
}

func TestCheckEditedMigration(t *testing.T) {
	db, ms := installed(t)
	edited := append([]Migration(nil), ms...)
	edited[0].Checksum = strings.Repeat("0", 64)
	wantCheck(t, db, edited, "[1]", "edited")
}

// A migration file renamed without a content change keeps its version and checksum; the name
// recorded in schema_migrations still differs from the binary's, so both Check and Apply refuse.
func TestRenamedMigrationRefused(t *testing.T) {
	db, ms := installed(t)
	renamed := append([]Migration(nil), ms...)
	renamed[0].Name = "renamed"
	for _, c := range []struct {
		op    string
		err   error
		parts []string
	}{
		{"Check", Check(context.Background(), db, renamed), []string{"[1]", "edited"}},
		{"Apply", applyErr(db, renamed), []string{"migration 1", ms[0].Name, "renamed", "edited"}},
	} {
		if c.err == nil {
			t.Errorf("%s accepted a renamed migration", c.op)
			continue
		}
		for _, p := range c.parts {
			if !strings.Contains(c.err.Error(), p) {
				t.Errorf("%s: %v; want it to contain %q", c.op, c.err, p)
			}
		}
	}
}

func applyErr(db *sql.DB, ms []Migration) error {
	_, err := Apply(context.Background(), db, ms)
	return err
}
