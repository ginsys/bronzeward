package main

import (
	"context"
	"database/sql"
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
	if _, err := tx.Exec(`UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
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

// compilation §3.5: the sweep writes due claims abandoned before the server serves, and then
// periodically until the server stops.
func TestStartSweep(t *testing.T) {
	db, _ := dbtest.New(t)
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
	human, cluster, machine := id.New(id.Principal), id.New(id.Cluster), id.New(id.Machine)
	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, []any{human}},
		{`INSERT INTO cluster (id, name, endpoint, contract, created_at) VALUES ($1, 'office', 'https://cp.example.test:6443', 'v1.13', now())`,
			[]any{cluster}},
		{`INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
			VALUES ($1, $2, '0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b', 'SN-1', 'normal', '10.55.0.3:50000', now())`, []any{machine, cluster}},
	} {
		if _, err := db.Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
	first := dueClaim(t, db, human, cluster, machine)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := startSweep(t.Context(), ctx, db, 20*time.Millisecond, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
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
