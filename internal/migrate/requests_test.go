package migrate

import (
	"context"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// PA §7.1, §10.4: what the schema itself refuses in 0003.
func TestRequestConstraints(t *testing.T) {
	db, _ := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	human, other, service := id.New(id.Principal), id.New(id.Principal), id.New(id.Principal)
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, human)
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'bob', now())`, other)
	mustExec(t, db, `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'ci', $2, now())`, service, human)
	act := id.New(id.Act)
	mustExec(t, db, `INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, idempotency_key, request_id, epoch, at)
		SELECT $1, $2, 'human', 'api', 'recovery-admin', 'identity.revoke', '{}', 'k0123456789abcdef', $3, epoch, now()
		FROM installation_state`, act, human, id.New(id.Request))

	const key = "k0123456789abcdef"
	fp := make([]byte, 32)
	insertRecord := `INSERT INTO idempotency_record (principal, key, fingerprint, request_id, epoch, status, body, created_at)
		SELECT $1, $2, $3, $4, epoch, $5, '{}', now() FROM installation_state`
	mustExec(t, db, insertRecord, human, key, fp, id.New(id.Request), 201)
	insertRevocation := `INSERT INTO identity_revocation (identity, revoked_by, role, reason, act, epoch, at)
		SELECT $1, $2, $3, $4, $5, epoch, now() FROM installation_state`
	mustExec(t, db, insertRevocation, service, human, "recovery-admin", "left", act)

	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"15-character key", insertRecord, []any{human, "k0123456789abcd", fp, id.New(id.Request), 201}, "23514"},
		{"key with a dot", insertRecord, []any{human, "k0123456789abcd.", fp, id.New(id.Request), 201}, "23514"},
		{"31-byte fingerprint", insertRecord, []any{human, "k0123456789abcdeX", fp[:31], id.New(id.Request), 201}, "23514"},
		{"status 99", insertRecord, []any{human, "k0123456789abcdeX", fp, id.New(id.Request), 99}, "23514"},
		{"second record for a key", insertRecord, []any{human, key, fp, id.New(id.Request), 201}, "23505"},
		{"record of no principal", insertRecord, []any{id.New(id.Principal), key, fp, id.New(id.Request), 201}, "23503"},
		{"revocation by a service", insertRevocation, []any{other, service, "recovery-admin", "x", id.New(id.Act)}, "23503"},
		{"revocation under approver", insertRevocation, []any{other, human, "approver", "x", id.New(id.Act)}, "23514"},
		{"blank reason", insertRevocation, []any{other, human, "recovery-admin", "  ", id.New(id.Act)}, "23514"},
		{"revocation citing no act", insertRevocation, []any{other, human, "recovery-admin", "x", id.New(id.Act)}, "23503"},
		{"second revocation of an identity", insertRevocation, []any{service, human, "recovery-admin", "x", id.New(id.Act)}, "23505"},
		{"two revocations citing one act", insertRevocation, []any{other, human, "recovery-admin", "x", act}, "23505"},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	for _, q := range []string{
		"UPDATE idempotency_record SET status = 200", "DELETE FROM idempotency_record",
		"UPDATE identity_revocation SET reason = 'y'", "DELETE FROM identity_revocation",
		"TRUNCATE idempotency_record", "TRUNCATE identity_revocation",
	} {
		if _, err := db.Exec(q); sqlState(err) != "BW001" {
			t.Errorf("%s: %v; want the immutability trigger (BW001)", q, err)
		}
	}
}
