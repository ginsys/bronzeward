package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/staging"
)

// dueClaim inserts a transient claim whose lease has lapsed, with its running ingest operation.
func dueClaim(t *testing.T, db *sql.DB, human, cluster, machine string) string {
	t.Helper()
	return insertClaim(t, db, human, cluster, machine, true)
}

// insertClaim inserts a transient claim with its running ingest operation; lapsed sets its lease
// in the past, so the claim is due.
func insertClaim(t *testing.T, db *sql.DB, human, cluster, machine string, lapsed bool) string {
	t.Helper()
	var epoch string
	if err := db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	o := staging.Owner{ID: "a/4242/start-1", Epoch: epoch}
	c := staging.Claim{ID: id.New(id.Ingestion), Mode: "transient", Cluster: cluster, Machine: machine, Gen: 1}
	draft := id.New(id.Draft)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, 'import', 'open', 1, 'aaaaaaaaaaaaaaaaaaaaaaaaaa', now())`, draft, cluster); err != nil {
		t.Fatal(err)
	}
	timers := staging.Timers{Lease: 15 * time.Second, AbsoluteExpiry: 10 * time.Minute}
	if err := staging.Create(t.Context(), tx, o, timers, c, human, "k-ingest-"+c.ID[4:20]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, draft,
		draft_revision, ingestion, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'ingest', 'running', owner_epoch, owner, 1, owner_epoch, lease_until, $2, 1, $3, $4, 'human', 'author', now()
		FROM staging_claim WHERE id = $3`, id.New(id.Operation), draft, c.ID, human); err != nil {
		t.Fatal(err)
	}
	if lapsed {
		if _, err := tx.Exec(`UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func claimState(t *testing.T, db *sql.DB, claim string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT state FROM staging_claim WHERE id = $1`, claim).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// sweepFixture is a migrated, installed database with one human, cluster and machine.
func sweepFixture(t *testing.T) (db *sql.DB, human, cluster, machine string) {
	t.Helper()
	db, _, human, cluster, machine = sweepFixtureDSN(t)
	return db, human, cluster, machine
}

// sweepFixtureDSN is sweepFixture with the database's DSN.
func sweepFixtureDSN(t *testing.T) (db *sql.DB, dsn, human, cluster, machine string) {
	t.Helper()
	db, dsn = dbtest.New(t)
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(t.Context(), db, ms); err != nil {
		t.Fatal(err)
	}
	if _, _, err := migrate.Install(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	human, cluster, machine = id.New(id.Principal), id.New(id.Cluster), id.New(id.Machine)
	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, []any{human}},
		{`INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
			VALUES ($1, 'office', 'https://cp.example.test:6443', 'v1.13', '8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuyzCfGIRVSL0=', now())`,
			[]any{cluster}},
		{`INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
			VALUES ($1, $2, '0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b', 'SN-1', 'normal', '10.55.0.3:50000', now())`, []any{machine, cluster}},
	} {
		if _, err := db.Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
	return db, dsn, human, cluster, machine
}

// compilation §3.5: the sweep writes due claims abandoned before the server serves, and then
// periodically until the server stops.
func TestStartSweep(t *testing.T) {
	db, human, cluster, machine := sweepFixture(t)
	first := dueClaim(t, db, human, cluster, machine)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	startSweep(t.Context(), ctx, db, 20*time.Millisecond, func(string, ...any) {})
	if s := claimState(t, db, first); s != "abandoned" {
		t.Fatalf("after the startup sweep the claim is %s", s)
	}
	second := dueClaim(t, db, human, cluster, machine)
	deadline := time.Now().Add(5 * time.Second)
	for claimState(t, db, second) != "abandoned" {
		if time.Now().After(deadline) {
			t.Fatal("no periodic sweep abandoned the second claim")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A claim whose abandonment fails at startup is logged, not fatal: reads already treat it as
// abandoned, and the server must still start and sweep the rest.
func TestStartSweepLogsAFailure(t *testing.T) {
	db, human, cluster, machine := sweepFixture(t)
	stuck := dueClaim(t, db, human, cluster, machine)
	// An event the operation's last_event does not count: the terminal event's number is taken.
	if _, err := db.Exec(`INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		SELECT id, 1, epoch, 'ingest', '{"type":"started"}', now() FROM operation WHERE ingestion = $1`, stuck); err != nil {
		t.Fatal(err)
	}
	other := dueClaim(t, db, human, cluster, machine)
	var logged []string
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	startSweep(t.Context(), ctx, db, time.Hour, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if s := claimState(t, db, other); s != "abandoned" {
		t.Errorf("the other due claim is %s", s)
	}
	if len(logged) == 0 || !strings.Contains(strings.Join(logged, "\n"), stuck) {
		t.Errorf("logged %q, want the stuck claim's error", logged)
	}
}

// waitAbandoned waits until claim's stored state is abandoned.
func waitAbandoned(t *testing.T, db *sql.DB, claim, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for claimState(t, db, claim) != "abandoned" {
		if time.Now().After(deadline) {
			t.Fatalf("no sweep abandoned %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A server without an ingestion block creates no claims, but another instance on the same
// database may and then stop, so this one sweeps too: at startup and then every fallbackSweep
// (compilation §3.5).
func TestServeSweepsWithoutIngestion(t *testing.T) {
	db, dsn, human, cluster, machine := sweepFixtureDSN(t)
	before := dueClaim(t, db, human, cluster, machine)
	old := fallbackSweep
	fallbackSweep = 20 * time.Millisecond
	t.Cleanup(func() { fallbackSweep = old })
	cfg := configFile(t, dsn) // before the goroutine: its t.Fatal must not leave errc unsent
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- serveContext(ctx, []string{"-config", cfg}) }()
	defer func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	waitAbandoned(t, db, before, "the claim due at startup")
	after := dueClaim(t, db, human, cluster, machine)
	waitAbandoned(t, db, after, "the claim another instance left after startup")
}
