package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The statements 0006's tests insert with.
const (
	insertMachineAt = `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, NULL, 'normal', $4, now())`
	insertMachineNoEndpoint = `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, created_at)
		VALUES ($1, $2, $3, NULL, 'normal', now())`
	insertAct = `INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, request_id, epoch, at)
		SELECT $1, $2, 'human', 'api', 'author', 'machine.talos-endpoint', ARRAY[$3], $4, epoch, now() FROM installation_state`
	insertMachineEvent = `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, $3, $4::jsonb, now() FROM installation_state`
	insertEndpointChange = `INSERT INTO machine_endpoint_change (machine, revision, previous_endpoint, new_endpoint, act)
		VALUES ($1, $2, $3, $4, $5)`
)

// endpointRows holds a machine with one recorded endpoint change, inserted by endpointChangeRows.
type endpointRows struct {
	adoption
	act, act2 string
}

func endpointChangeRows(t *testing.T, db *sql.DB) endpointRows {
	t.Helper()
	e := endpointRows{adoption: adoptionRows(t, db), act: id.New(id.Act), act2: id.New(id.Act)}
	mustExec(t, db, insertAct, e.act, e.human, e.machine, id.New(id.Request))
	mustExec(t, db, insertAct, e.act2, e.human, e.machine, id.New(id.Request))
	mustExec(t, db, insertMachineEvent, e.machine, 1, "endpoint-change", `{"previous":"10.55.0.3:50000","new":"10.55.0.4:50000"}`)
	mustExec(t, db, insertEndpointChange, e.machine, 1, "10.55.0.3:50000", "10.55.0.4:50000", e.act)
	// An endpoint-change entry with no record yet, at revision 2.
	mustExec(t, db, insertMachineEvent, e.machine, 2, "endpoint-change", `{}`)
	return e
}

// PA §3.3: every machine has a Talos endpoint, in the form ParseEndpoint returns; the endpoint
// change is one machine timeline entry, names its act, and changes the endpoint.
func TestEndpointConstraints(t *testing.T) {
	db, _ := installed(t)
	e := endpointChangeRows(t, db)
	n := 0
	uuid := func() string { n++; return fmt.Sprintf("3e8d9f4b-5c6a-4b8c-9d2e-%012x", n) }
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"machine without an endpoint", insertMachineNoEndpoint, []any{id.New(id.Machine), e.cluster, uuid()}, "23502"},
		{"endpoint without a port", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "10.55.0.9"}, "23514"},
		{"DNS endpoint", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "node.example.test:50000"}, "23514"},
		{"unbracketed IPv6 endpoint", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "fd00::1:50000"}, "23514"},
		{"endpoint with a scheme", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "https://10.55.0.9:50000"}, "23514"},
		{"endpoint with a zero-led port", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "10.55.0.9:050000"}, "23514"},
		{"endpoint with whitespace", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "10.55.0.9:50000 "}, "23514"},
		{"endpoint with port 65536", insertMachineAt, []any{id.New(id.Machine), e.cluster, uuid(), "10.55.0.9:65536"}, "23514"},
		{"machine event of an unknown kind", insertMachineEvent, []any{e.machine, 3, "drift", `{}`}, "23514"},
		{"machine event revision 0", insertMachineEvent, []any{e.machine, 0, "endpoint-change", `{}`}, "23514"},
		{"machine event entry not an object", insertMachineEvent, []any{e.machine, 3, "endpoint-change", `null`}, "23514"},
		{"second machine event at one revision", insertMachineEvent, []any{e.machine, 1, "endpoint-change", `{}`}, "23505"},
		{"machine event of no machine", insertMachineEvent, []any{id.New(id.Machine), 1, "endpoint-change", `{}`}, "23503"},
		{"endpoint change with no timeline entry", insertEndpointChange, []any{e.machine, 3, "10.55.0.4:50000", "10.55.0.5:50000", e.act2}, "23503"},
		{"endpoint change to the same endpoint", insertEndpointChange, []any{e.machine, 2, "10.55.0.4:50000", "10.55.0.4:50000", e.act2}, "23514"},
		{"endpoint change to a DNS name", insertEndpointChange, []any{e.machine, 2, "10.55.0.4:50000", "node.example.test:50000", e.act2}, "23514"},
		{"endpoint change of no act", insertEndpointChange, []any{e.machine, 2, "10.55.0.4:50000", "10.55.0.5:50000", id.New(id.Act)}, "23503"},
		{"second change by one act", insertEndpointChange, []any{e.machine, 2, "10.55.0.4:50000", "10.55.0.5:50000", e.act}, "23505"},
		{"second endpoint change at one entry", insertEndpointChange, []any{e.machine, 1, "10.55.0.4:50000", "10.55.0.5:50000", e.act2}, "23505"},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	// Controls: each accepted form commits, and so does a change at the free entry.
	for i, ep := range []string{"10.55.0.9:50000", "10.55.0.10:1", "[fd00::9]:50000", "[::ffff:10.55.0.9]:65535"} {
		if _, err := db.Exec(insertMachineAt, id.New(id.Machine), e.cluster, uuid(), ep); err != nil {
			t.Errorf("control %d, endpoint %s: %v", i, ep, err)
		}
	}
	mustExec(t, db, insertEndpointChange, e.machine, 2, "10.55.0.4:50000", "[fd00::9]:50000", e.act2)
}

// The control for 0006's checks: with each dropped, the row TestEndpointConstraints expects it to
// refuse commits, so it is that check, not another, that refuses.
func TestEndpointConstraintControl(t *testing.T) {
	db, _ := installed(t)
	e := endpointChangeRows(t, db)
	for _, c := range []struct {
		drop, q string
		args    []any
	}{
		{"ALTER TABLE machine ALTER COLUMN talos_endpoint DROP NOT NULL", insertMachineNoEndpoint,
			[]any{id.New(id.Machine), e.cluster, "4f9e0a5c-6d7b-4c9d-8e3f-5a6b7c8d9e0f"}},
		{"ALTER DOMAIN talos_endpoint DROP CONSTRAINT talos_endpoint_form", insertMachineAt,
			[]any{id.New(id.Machine), e.cluster, "4f9e0a5c-6d7b-4c9d-8e3f-5a6b7c8d9e0f", "node.example.test:50000"}},
		{"ALTER TABLE machine_endpoint_change DROP CONSTRAINT machine_endpoint_change_changes", insertEndpointChange,
			[]any{e.machine, 2, "10.55.0.4:50000", "10.55.0.4:50000", e.act2}},
		{"ALTER TABLE machine_endpoint_change DROP CONSTRAINT machine_endpoint_change_entry", insertEndpointChange,
			[]any{e.machine, 3, "10.55.0.4:50000", "10.55.0.5:50000", e.act2}},
		{"ALTER TABLE machine_endpoint_change DROP CONSTRAINT machine_endpoint_change_pkey", insertEndpointChange,
			[]any{e.machine, 1, "10.55.0.4:50000", "10.55.0.5:50000", e.act2}},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.drop); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(c.q, c.args...); err != nil {
				t.Errorf("after %s: %v; want it to commit", c.drop, err)
			}
		}()
	}
}

// Both 0006 tables are immutable (PA §3: TimelineEvent and MachineEndpointChange), with the
// control that drops each trigger and must then succeed.
func TestEndpointImmutableTables(t *testing.T) {
	db, _ := installed(t)
	endpointChangeRows(t, db)
	for _, table := range []string{"machine_endpoint_change", "machine_event"} {
		for _, stmt := range []string{"UPDATE " + table + " SET revision = revision", "DELETE FROM " + table, "TRUNCATE " + table + " CASCADE"} {
			if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
				t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
			}
		}
		if count(t, db, table) == 0 {
			t.Errorf("%s: a refused statement removed its rows", table)
		}
	}
	for _, table := range []string{"machine_endpoint_change", "machine_event"} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			mustTx := func(q string) {
				if _, err := tx.Exec(q); err != nil {
					t.Errorf("%s without the trigger: %v", q, err)
				}
			}
			mustTx("DROP TRIGGER immutable_rows ON " + table)
			if table == "machine_event" {
				mustTx("DROP TRIGGER immutable_rows ON machine_endpoint_change")
				mustTx("DELETE FROM machine_endpoint_change")
			}
			mustTx("UPDATE " + table + " SET revision = revision")
			mustTx("DELETE FROM " + table)
		}()
	}
}

// §11 rule 6: 0006 supports no earlier database. On an installation at 0005 holding a machine it
// fails, PostgreSQL refusing the NOT NULL column, and leaves the installation at 0005; the control
// upgrades the same installation without the machine.
func TestEndpointMigrationRefusesMachines(t *testing.T) {
	ctx := context.Background()
	ms, err := Embedded()
	if err != nil || len(ms) < 6 || ms[5].Version != 6 {
		t.Fatalf("embedded migrations: %v; want 0006 sixth", err)
	}
	for _, withMachine := range []bool{true, false} {
		db, _ := dbtest.New(t)
		if _, err := Apply(ctx, db, ms[:5]); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Install(ctx, db); err != nil {
			t.Fatal(err)
		}
		if withMachine {
			cl := id.New(id.Cluster)
			mustExec(t, db, insertClusterNoID, cl)
			mustExec(t, db, insertMachineNoEndpoint, id.New(id.Machine), cl, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b")
		}
		got, err := Apply(ctx, db, ms)
		var top int
		if err := db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&top); err != nil {
			t.Fatal(err)
		}
		switch {
		case withMachine && (err == nil || !strings.Contains(err.Error(), "talos_endpoint") || len(got) != 0 || top != 5):
			t.Errorf("with a machine: applied %v, %v, at %d; want 0006 refused and the installation at 0005", got, err, top)
		case !withMachine && (err != nil || top < 6):
			t.Errorf("without a machine: applied %v, %v, at %d; want the upgrade to apply", got, err, top)
		}
	}
}
