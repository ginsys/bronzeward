package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// A machine's role is its latest import base's value, served as `role` (ginsys/bronzeward#1096, choice
// §17.49, API revision 14): null for a machine with no import base or one whose revision stored none,
// and a later import's value over an earlier one's. The column refuses any other text, so the read
// can only answer one of the two names or null.
func TestMachineRoleRead(t *testing.T) {
	e := newEnv(t, options{})
	author, viewer := e.human("h-author"), e.human("h-viewer")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	uuids := []string{uuidA, "1c6b7d2f-3a4e-4f6a-9b0c-1d2e3f4a5b6c", "2d7c8e30-4b5f-4a7b-8c1d-2e3f4a5b6c7d", "3e8d9f41-5c60-4b8c-9d2e-3f4a5b6c7d8e"}
	machines := make([]string, len(uuids))
	for i, u := range uuids {
		machines[i] = decode[machineBody](t, e.do(e.api, machineCall(author, "k-machine-"+strconv.Itoa(i)+"-0123456789", cl, u)), http.StatusCreated).ID
	}
	at := time.Now().Add(-time.Hour)
	revise := func(machine string, role any) {
		t.Helper()
		at = at.Add(time.Minute)
		mustExec(t, e.db, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
			baseline_digest_key, configuration_digest, machine_role, created_at, author) VALUES ($1, $2, 'machine: {}', '[]', '\x01', $3, 'k:1', $3, $4, $5,
			(SELECT min(id) FROM principal))`, id.New(id.ImportBase), machine, make([]byte, 32), role, at)
	}
	revise(machines[1], "worker")
	revise(machines[2], "worker") // a re-import of a node whose type changed: the latest revision answers
	revise(machines[2], "controlplane")
	revise(machines[3], "controlplane") // an import before the role was stored sits under a later one without
	revise(machines[3], nil)
	for i, want := range []*string{nil, ptr("worker"), ptr("controlplane"), nil} {
		got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + machines[i], token: viewer}), http.StatusOK).Role
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("machine %d: role %s, want %s", i, show(got), show(want))
		}
	}
	list := decode[struct{ Items []machineBody }](t, e.do(e.api, call{method: "GET", path: prefix + "/machines", token: viewer}), http.StatusOK)
	roles := map[string]*string{}
	for _, m := range list.Items {
		roles[m.ID] = m.Role
	}
	if roles[machines[1]] == nil || *roles[machines[1]] != "worker" || roles[machines[0]] != nil {
		t.Errorf("the machine list answers %v", roles)
	}
	for _, bad := range []string{"init", "Worker", ""} {
		if _, err := e.db.Exec(`INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
			baseline_digest_key, configuration_digest, machine_role, created_at, author) VALUES ($1, $2, 'machine: {}', '[]', '\x01', $3, 'k:1', $3, $4, now(),
			(SELECT min(id) FROM principal))`, id.New(id.ImportBase), machines[0], make([]byte, 32), bad); err == nil {
			t.Errorf("the column accepted the role %q", bad)
		}
	}
}

func show(s *string) string {
	if s == nil {
		return "null"
	}
	return strconv.Quote(*s)
}
