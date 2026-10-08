package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/migrate"
)

func configFile(t *testing.T, dsn string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bronzeward.yaml")
	body := "listen: 127.0.0.1:0\ndatabase:\n  dsn: " + dsn + "\nauth:\n  oidc:\n    issuer: https://idp.test\n    audience: bronzeward\n" +
		"execution: {maxTransportDeadline: 5m}\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeRefusesAnUnmigratedDatabase(t *testing.T) {
	_, dsn := dbtest.New(t)
	errc := make(chan error, 1)
	go func() { errc <- serve([]string{"-config", configFile(t, dsn)}) }()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "run bronzeward migrate") {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve started on a database without a schema")
	}
}

// serve reads each identity's token file at startup and refuses to start, naming the field, when
// one cannot be read: ingestion's, publication's compiler and metadata identities' (compilation
// §1) and the executor's (persistence-api §3.3). The tokens are synthetic.
func TestServeReadsEachTokenFile(t *testing.T) {
	_, dsn, _, _, _ := sweepFixtureDSN(t)
	for _, missing := range []string{"ingestionTokenFile", "compilerTokenFile", "metadataTokenFile", "executorTokenFile"} {
		t.Run(missing, func(t *testing.T) {
			dir := t.TempDir()
			body := "listen: 127.0.0.1:0\ndatabase:\n  dsn: " + dsn + "\nauth:\n  oidc:\n    issuer: https://idp.test\n    audience: bronzeward\n" +
				"execution: {maxTransportDeadline: 5m}\nprovider:\n  address: http://127.0.0.1:1\n" +
				"  keys: {baseline: bw-baseline, staging: bw-staging, digest: bw-digest, artifact: bw-artifact}\n" +
				"ingestion: {instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}\n"
			for _, f := range []string{"ingestionTokenFile", "compilerTokenFile", "metadataTokenFile", "executorTokenFile"} {
				if f != missing {
					writeFile(t, filepath.Join(dir, f), "synthetic-"+f+"\n", 0o600)
				}
			}
			body = strings.Replace(body, "  keys:", "  ingestionTokenFile: "+filepath.Join(dir, "ingestionTokenFile")+
				"\n  compilerTokenFile: "+filepath.Join(dir, "compilerTokenFile")+
				"\n  metadataTokenFile: "+filepath.Join(dir, "metadataTokenFile")+
				"\n  executorTokenFile: "+filepath.Join(dir, "executorTokenFile")+"\n  keys:", 1)
			cfg := filepath.Join(dir, "bronzeward.yaml")
			writeFile(t, cfg, body, 0o600)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			err := serveContext(ctx, []string{"-config", cfg})
			if err == nil || !strings.Contains(err.Error(), "provider."+missing) {
				t.Fatalf("serve: %v; want a refusal naming provider.%s", err, missing)
			}
		})
	}
}

// newerMigration records a migration this binary does not know, as a newer binary's migrate would.
func newerMigration(t *testing.T, x interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) int {
	t.Helper()
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	next := len(ms) + 1
	if _, err := x.ExecContext(context.Background(),
		"INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES ($1, 'newer', $2, now())",
		next, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestMigrateCommandRefusesANewerSchema(t *testing.T) {
	db, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	if err := runMigrate([]string{"-config", cfg}, io.Discard); err != nil {
		t.Fatal(err)
	}
	next := newerMigration(t, db)
	var out bytes.Buffer
	err := runMigrate([]string{"-config", cfg}, &out)
	if err == nil || !strings.Contains(err.Error(), "["+strconv.Itoa(next)+"]") {
		t.Fatalf("migrate on a newer schema: %v, printed %q", err, out.String())
	}
}

// A newer binary's migrate commits its migration after this run's preflight found none unknown:
// the run must still fail rather than report the schema at its own version. The other session
// holds installation_state, so this run is past its preflight and waiting in Install when the
// newer migration is committed.
func TestMigrateCommandRefusesANewerMigrationCommittedDuringItsRun(t *testing.T) {
	db, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	if err := runMigrate([]string{"-config", cfg}, io.Discard); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE installation_state IN EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	errc := make(chan error, 1)
	go func() { errc <- runMigrate([]string{"-config", cfg}, &out) }()
	for deadline := time.Now().Add(15 * time.Second); ; {
		var waiting int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%installation_state%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-errc:
			t.Fatalf("migrate ended before it reached Install: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("migrate never waited on installation_state")
		}
		time.Sleep(20 * time.Millisecond)
	}
	next := newerMigration(t, tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "["+strconv.Itoa(next)+"]") {
			t.Fatalf("migrate: %v, printed %q; want a refusal naming migration %d", err, out.String(), next)
		}
		if strings.Contains(out.String(), "schema is at version") {
			t.Fatalf("migrate printed %q", out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("migrate did not finish")
	}
}

func TestMigrateCommandTwice(t *testing.T) {
	_, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	var first, second bytes.Buffer
	if err := runMigrate([]string{"-config", cfg}, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "installation epoch ep_") {
		t.Fatalf("first run printed %q", first.String())
	}
	if err := runMigrate([]string{"-config", cfg}, &second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "applied []") || strings.Contains(second.String(), "installation epoch") {
		t.Fatalf("second run printed %q", second.String())
	}
}
