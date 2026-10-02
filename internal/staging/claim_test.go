package staging

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
)

var timers = Timers{Lease: 15 * time.Second, AbsoluteExpiry: 10 * time.Minute}

type fixture struct {
	db                      *sql.DB
	human, cluster, machine string
}

func setup(t *testing.T) fixture {
	t.Helper()
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
	f := fixture{db: db, human: id.New(id.Principal), cluster: id.New(id.Cluster), machine: id.New(id.Machine)}
	exec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, f.human)
	exec(t, db, `INSERT INTO cluster (id, name, endpoint, contract, created_at) VALUES ($1, 'office', 'https://cp.example.test:6443', 'v1.13', now())`, f.cluster)
	exec(t, db, `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, '0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b', 'SN-1', 'normal', '10.55.0.3:50000', now())`, f.machine, f.cluster)
	return f
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func currentEpoch(t *testing.T, db *sql.DB) string {
	t.Helper()
	var ep string
	if err := db.QueryRow("SELECT epoch FROM installation_state").Scan(&ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

// newEpoch enters a new epoch, as a restore does (persistence-api.md §12).
func newEpoch(t *testing.T, db *sql.DB) {
	t.Helper()
	ep := id.New(id.Epoch)
	exec(t, db, `INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())`, ep)
	exec(t, db, `UPDATE installation_state SET epoch = $1`, ep)
}

// create makes a held claim of mode and, beside it, its running ingest operation on a draft of
// its own (one running ingest per draft revision), in one transaction as T11 does.
func (f fixture) create(t *testing.T, mode string) (Owner, Claim) {
	t.Helper()
	o := Owner{ID: "a/4242/start-1", Epoch: currentEpoch(t, f.db)}
	c := Claim{ID: id.New(id.Ingestion), Mode: mode, Cluster: f.cluster, Machine: f.machine, Gen: 1}
	draft := id.New(id.Draft)
	exec(t, f.db, `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, 'import', 'open', 1, 'aaaaaaaaaaaaaaaaaaaaaaaaaa', now())`, draft, f.cluster)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := Create(t.Context(), tx, o, timers, c, f.human, "k-ingest-"+c.ID[4:20]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, draft,
		draft_revision, ingestion, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'ingest', 'running', $2, $3, 1, $2, lease_until, $4, 1, $5, $6, 'human', 'author', now()
		FROM staging_claim WHERE id = $5`, id.New(id.Operation), o.Epoch, o.ID, draft, c.ID, f.human); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return o, c
}

type row struct {
	state           string
	gen             int64
	lease, expires  time.Time
	payload, digest []byte
}

func (f fixture) row(t *testing.T, claim string) row {
	t.Helper()
	var r row
	if err := f.db.QueryRow(`SELECT state, owner_gen, lease_until, expires_at, payload, payload_digest FROM staging_claim WHERE id = $1`,
		claim).Scan(&r.state, &r.gen, &r.lease, &r.expires, &r.payload, &r.digest); err != nil {
		t.Fatal(err)
	}
	return r
}

func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Compilation §3.2 / persistence-api §5.1: every owner statement is fenced on the owner, its
// generation, its epoch being current, a live state, a live lease and the absolute expiry. Each
// row breaks one term; each statement must refuse it and leave the claim as it was.
func TestOwnerFenceMatrix(t *testing.T) {
	sum := sha256.Sum256([]byte("envelope"))
	statements := map[string]func(ctx context.Context, db *sql.DB, o Owner, c Claim) error{
		"Heartbeat": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return Heartbeat(ctx, db, o, c, timers.Lease)
		},
		"StorePayload": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return StorePayload(ctx, db, o, c, []byte("ct"), sum)
		},
		"Release": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return inTx(ctx, db, func(tx *sql.Tx) error { return Release(ctx, tx, o, c) })
		},
		"Abandon": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return inTx(ctx, db, func(tx *sql.Tx) error { return Abandon(ctx, tx, o, c) })
		},
	}
	f := setup(t)
	for _, tc := range []struct {
		name  string
		tweak func(t *testing.T, o *Owner, c *Claim)
		want  error
	}{
		{"another owner", func(_ *testing.T, o *Owner, _ *Claim) { o.ID = "b/1/start-2" }, ErrFenced},
		{"another generation", func(_ *testing.T, _ *Owner, c *Claim) { c.Gen = 2 }, ErrFenced},
		{"another owner epoch", func(t *testing.T, o *Owner, _ *Claim) { o.Epoch = id.New(id.Epoch) }, ErrEpochSuperseded},
		{"a superseded epoch", func(t *testing.T, _ *Owner, _ *Claim) { newEpoch(t, f.db) }, ErrEpochSuperseded},
		// A claim from an earlier epoch is never resumed, even by an owner holding the current one.
		{"a claim of an earlier epoch", func(t *testing.T, o *Owner, _ *Claim) {
			newEpoch(t, f.db)
			o.Epoch = currentEpoch(t, f.db)
		}, ErrFenced},
		{"a lapsed lease", func(t *testing.T, _ *Owner, c *Claim) {
			exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
		}, ErrFenced},
		{"an expired claim", func(t *testing.T, _ *Owner, c *Claim) {
			exec(t, f.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second', lease_until = now() - interval '2 seconds' WHERE id = $1`, c.ID)
		}, ErrFenced},
		{"an expired claim with a live lease", func(t *testing.T, _ *Owner, c *Claim) {
			exec(t, f.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second' WHERE id = $1`, c.ID)
		}, ErrFenced},
		{"a released claim", func(t *testing.T, _ *Owner, c *Claim) {
			exec(t, f.db, `UPDATE staging_claim SET state = 'released' WHERE id = $1`, c.ID)
		}, ErrFenced},
	} {
		for name, stmt := range statements {
			o, c := f.create(t, "encrypted")
			tc.tweak(t, &o, &c)
			before := f.row(t, c.ID)
			if err := stmt(t.Context(), f.db, o, c); !errors.Is(err, tc.want) {
				t.Errorf("%s, %s: %v; want %v", tc.name, name, err, tc.want)
			}
			if after := f.row(t, c.ID); after.state != before.state || !after.lease.Equal(before.lease) || after.payload != nil {
				t.Errorf("%s, %s: the claim changed: %+v -> %+v", tc.name, name, before, after)
			}
		}
	}
	// Control: the unbroken owner passes every statement.
	for name, stmt := range statements {
		o, c := f.create(t, "encrypted")
		if err := stmt(t.Context(), f.db, o, c); err != nil {
			t.Errorf("control, %s: %v", name, err)
		}
	}
}

// The heartbeat extends to now() + lease, never past the absolute expiry, and the operation's
// lease follows the claim's.
func TestHeartbeat(t *testing.T) {
	f := setup(t)
	o, c := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '1 second' WHERE id = $1`, c.ID)
	before := f.row(t, c.ID)
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); err != nil {
		t.Fatal(err)
	}
	after := f.row(t, c.ID)
	if !after.lease.After(before.lease.Add(10 * time.Second)) {
		t.Fatalf("lease %s -> %s; want about 15s from now", before.lease, after.lease)
	}
	exec(t, f.db, `UPDATE staging_claim SET expires_at = now() + interval '2 seconds' WHERE id = $1`, c.ID)
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); err != nil {
		t.Fatal(err)
	}
	capped := f.row(t, c.ID)
	var op time.Time
	if err := f.db.QueryRow(`SELECT lease_until FROM operation WHERE ingestion = $1`, c.ID).Scan(&op); err != nil {
		t.Fatal(err)
	}
	if !capped.lease.Equal(capped.expires) || !op.Equal(capped.lease) {
		t.Fatalf("lease %s, expiry %s, operation lease %s; want all equal", capped.lease, capped.expires, op)
	}
}

func TestCreate(t *testing.T) {
	f := setup(t)
	_, c := f.create(t, "encrypted")
	r := f.row(t, c.ID)
	if r.state != "held" || r.gen != 1 || !r.lease.Before(r.expires) || r.expires.Sub(r.lease) < 9*time.Minute || r.payload != nil {
		t.Fatalf("created %+v", r)
	}
	stale := Owner{ID: "a/4242/start-1", Epoch: currentEpoch(t, f.db)}
	newEpoch(t, f.db)
	gone := Claim{ID: id.New(id.Ingestion), Mode: "transient", Cluster: f.cluster, Machine: f.machine, Gen: 1}
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		return Create(t.Context(), tx, stale, timers, gone, f.human, "k-stale-0123456789")
	})
	if !errors.Is(err, ErrEpochSuperseded) {
		t.Fatalf("a stale epoch: %v", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM staging_claim WHERE id = $1`, gone.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d rows, %v; want none", n, err)
	}
	for name, c := range map[string]Claim{
		"generation 2":   {ID: id.New(id.Ingestion), Mode: "transient", Cluster: f.cluster, Machine: f.machine, Gen: 2},
		"an empty owner": {ID: id.New(id.Ingestion), Mode: "transient", Cluster: f.cluster, Machine: f.machine, Gen: 1},
	} {
		o := Owner{ID: "a/4242/start-1", Epoch: currentEpoch(t, f.db)}
		if name == "an empty owner" {
			o.ID = ""
		}
		if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Create(t.Context(), tx, o, timers, c, f.human, "k-other-0123456789") }); err == nil {
			t.Errorf("%s: created", name)
		}
	}
	for name, tm := range map[string]Timers{"no lease": {AbsoluteExpiry: time.Minute}, "lease past expiry": {Lease: time.Hour, AbsoluteExpiry: time.Minute}} {
		c := Claim{ID: id.New(id.Ingestion), Mode: "transient", Cluster: f.cluster, Machine: f.machine, Gen: 1}
		o := Owner{ID: "a/4242/start-1", Epoch: currentEpoch(t, f.db)}
		if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Create(t.Context(), tx, o, tm, c, f.human, "k-timer-0123456789") }); err == nil {
			t.Errorf("%s: created", name)
		}
	}
}

// Only an encrypted claim holds a payload; release and abandonment clear it with its digest.
func TestPayload(t *testing.T) {
	f := setup(t)
	sum := sha256.Sum256([]byte("envelope"))
	o, c := f.create(t, "transient")
	if err := StorePayload(t.Context(), f.db, o, c, []byte("ct"), sum); err == nil {
		t.Fatal("a transient claim took a payload")
	}
	// The statement itself fences the mode, not only the caller's Claim value.
	c.Mode = "encrypted"
	if err := StorePayload(t.Context(), f.db, o, c, []byte("ct"), sum); err == nil || f.row(t, c.ID).payload != nil {
		t.Fatalf("a transient claim took a payload under a claimed mode: %v", err)
	}
	for _, end := range []struct {
		state string
		fn    func(context.Context, *sql.Tx, Owner, Claim) error
	}{{"released", Release}, {"abandoned", Abandon}} {
		o, c := f.create(t, "encrypted")
		if err := StorePayload(t.Context(), f.db, o, c, []byte("ct"), sum); err != nil {
			t.Fatal(err)
		}
		if r := f.row(t, c.ID); string(r.payload) != "ct" || [32]byte(r.digest) != sum {
			t.Fatalf("stored %+v", r)
		}
		if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return end.fn(t.Context(), tx, o, c) }); err != nil {
			t.Fatal(err)
		}
		if r := f.row(t, c.ID); r.state != end.state || r.payload != nil || r.digest != nil {
			t.Fatalf("%s: %+v", end.state, r)
		}
	}
}
