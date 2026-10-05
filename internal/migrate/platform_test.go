package migrate

import (
	"fmt"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The statements the platform tests insert with.
const (
	insertMachinePlatform = `INSERT INTO machine (id, cluster, smbios_uuid, scope_state, talos_endpoint, platform, created_at)
		VALUES ($1, $2, $3, 'normal', '10.55.0.9:50000', $4, now())`
	insertMachineNoPlatform = `INSERT INTO machine (id, cluster, smbios_uuid, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, 'normal', '10.55.0.9:50000', now())`
)

// PA §3.3: every machine records the platform mode its configuration validates in (compilation §6
// step 8), one of metal, container and cloud, with no default; the control drops each check and
// the row it refused commits.
func TestMachinePlatform(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	n := 0
	uuid := func() string { n++; return fmt.Sprintf("6c1d2e3f-4a5b-4c6d-8e7f-%012x", n) }
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"machine without a platform", insertMachineNoPlatform, []any{id.New(id.Machine), a.cluster, uuid()}, "23502"},
		{"unknown platform", insertMachinePlatform, []any{id.New(id.Machine), a.cluster, uuid(), "vm"}, "23514"},
		{"platform in capitals", insertMachinePlatform, []any{id.New(id.Machine), a.cluster, uuid(), "Metal"}, "23514"},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	for _, p := range []string{"metal", "container", "cloud"} {
		if _, err := db.Exec(insertMachinePlatform, id.New(id.Machine), a.cluster, uuid(), p); err != nil {
			t.Errorf("control, platform %s: %v", p, err)
		}
	}
	for _, c := range []struct {
		drop, q string
		args    []any
	}{
		{"ALTER TABLE machine ALTER COLUMN platform DROP NOT NULL", insertMachineNoPlatform, []any{id.New(id.Machine), a.cluster, uuid()}},
		{"ALTER TABLE machine DROP CONSTRAINT machine_platform", insertMachinePlatform, []any{id.New(id.Machine), a.cluster, uuid(), "vm"}},
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
