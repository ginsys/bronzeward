package migrate

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

const insertToken = `INSERT INTO automation_token (id, owner, secret_sha256, roles, epoch, issued_at, expires_at)
	SELECT $1, $2, sha256('x'), $3::text[], epoch, now(), now() + $4::interval FROM installation_state`

// PA §10, §10.2, §10.5: what the schema itself refuses, whatever the code does.
func TestAuthenticationConstraints(t *testing.T) {
	db, _ := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	human, service := id.New(id.Principal), id.New(id.Principal)
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, human)
	mustExec(t, db, `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'ci', $2, now())`, service, human)
	insertAct := `INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, request_id, epoch, at)
		SELECT $1, $2, $3, $4, $5, 'x', '{}', $6, epoch, now() FROM installation_state`
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"human without sub", `INSERT INTO principal (id, kind, iss, created_at) VALUES ($1, 'human', 'https://idp.test', now())`, []any{id.New(id.Principal)}, "23514"},
		{"human with empty sub", `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', '', now())`, []any{id.New(id.Principal)}, "23514"},
		{"human with a responsible", `INSERT INTO principal (id, kind, iss, sub, responsible, created_at) VALUES ($1, 'human', 'https://idp.test', 'bob', $2, now())`, []any{id.New(id.Principal), human}, "23514"},
		{"service without responsible", `INSERT INTO principal (id, kind, name, created_at) VALUES ($1, 'service', 'x', now())`, []any{id.New(id.Principal)}, "23514"},
		{"second row for one subject", `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, []any{id.New(id.Principal)}, "23505"},
		{"service responsible for a service", `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'x', $2, now())`, []any{id.New(id.Principal), service}, "23503"},
		{"second service of one name", `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'ci', $2, now())`, []any{id.New(id.Principal), human}, "23505"},
		{"token owned by a human", insertToken, []any{id.New(id.Token), human, "{author}", "30 days"}, "23503"},
		{"approver token", insertToken, []any{id.New(id.Token), service, "{approver}", "30 days"}, "23514"},
		{"recovery-admin token", insertToken, []any{id.New(id.Token), service, "{author,recovery-admin}", "30 days"}, "23514"},
		{"token without roles", insertToken, []any{id.New(id.Token), service, "{}", "30 days"}, "23514"},
		{"91-day token", insertToken, []any{id.New(id.Token), service, "{author}", "2184 hours"}, "23514"},
		{"tool act with a role", insertAct, []any{id.New(id.Act), human, "human", "tool", "author", nil}, "23514"},
		{"api act without request", insertAct, []any{id.New(id.Act), human, "human", "api", "author", nil}, "23514"},
		{"act whose kind is not its principal's", insertAct, []any{id.New(id.Act), human, "service", "tool", nil, nil}, "23503"},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	// §10's first-use insert: a second insert for one subject is a no-op, not an error.
	res, err := db.Exec(`INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())
		ON CONFLICT (iss, sub) DO NOTHING`, id.New(id.Principal))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("ON CONFLICT (iss, sub): %d rows", n)
	}
}

// §10.2: one unrevoked token per identity, expired or not; 90 days is the maximum, inclusive.
func TestOneUnrevokedToken(t *testing.T) {
	db, _ := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	human, service := id.New(id.Principal), id.New(id.Principal)
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, human)
	mustExec(t, db, `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'ci', $2, now())`, service, human)
	first := id.New(id.Token)
	mustExec(t, db, insertToken, first, service, "{author,publisher}", "2160 hours")
	if _, err := db.Exec(insertToken, id.New(id.Token), service, "{author}", "1 day"); sqlState(err) != "23505" {
		t.Fatalf("second unrevoked token: %v; want 23505", err)
	}
	mustExec(t, db, `UPDATE automation_token SET revoked_at = now() WHERE id = $1`, first)
	mustExec(t, db, insertToken, id.New(id.Token), service, "{author}", "1 day")
}
