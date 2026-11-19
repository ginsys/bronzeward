package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos"
)

// PA §16: every observation purpose records a failed access read without node
// values or provider error text. The success/stale-version controls are in
// TestObservationRecordsTheAccessItRead; drift's node-side stand-in checks are separate.
func TestObservationPurposeAccessFailure(t *testing.T) {
	ae := newAttemptEnv(t, "")
	p := publishFixture(t, newDraftEnv(t))
	const sentinel = "synthetic-access-failure-key"
	for _, f := range []observeFor{
		{purpose: "evidence", plan: ae.pid},
		{purpose: "completion", plan: ae.pid, operation: ae.op},
		{purpose: "recovery", plan: ae.pid, operation: ae.op},
		{purpose: "restoration"},
		{purpose: "drift"},
		{purpose: "publication", operation: p.job.op, generation: p.job.gen},
	} {
		e, machine, cluster, owner := ae.env, ae.machine, ae.cluster, ae.a.d.owner
		if f.purpose == "publication" {
			// Publication observations are fenced by the running job's actual owner.
			e, machine, cluster, owner = p.env, p.machine, p.cluster, p.owner
		}
		sg := e.signer()
		e.keys.kv.sentinel = sentinel
		kv := e.keys.kv
		fault := func(f func()) func() {
			return func() {
				kv.mu.Lock()
				defer kv.mu.Unlock()
				f()
			}
		}
		for _, c := range []struct {
			name, cause string
			set         func()
		}{
			{"denied", "denied", fault(func() { kv.status["GET"] = http.StatusForbidden })},
			{"malformed", "malformed", fault(func() { kv.garble = true })},
			{"unavailable", "provider-unavailable", fault(func() { kv.status["GET"] = http.StatusServiceUnavailable })},
			{"status", "provider-unavailable", fault(func() { kv.status["GET"] = http.StatusTeapot })},
			{"version gone", "version-gone", func() {
				kv.destroy(latestActVersion(t, e.db, cluster).Path, latestActVersion(t, e.db, cluster).Version)
			}},
			{"version written again", "version-changed", fault(func() {
				v := latestActVersion(t, e.db, cluster)
				kv.versions[v.Path][v.Version-1].created = v.CreatedTime.Add(time.Hour)
			})},
			// Last: no act names a key. A later act and version restore the cluster for the next case.
			{"absent", "absent", func() {
				e.noSeed = true // the refusal is proven with no act, and signer() seeds none
				dropFirstKeyActs(t, e.db, cluster)
			}},
		} {
			t.Run(f.purpose+"/"+c.name, func(t *testing.T) {
				// Undo the fault whatever the case, then re-seed an act with a fresh key version.
				t.Cleanup(func() {
					fault(func() {
						delete(kv.status, "GET")
						kv.garble = false
					})()
					e.keys.seed(t, e.db, cluster)
				})
				c.set()
				var dialled bool
				d := e.d
				d.exe, d.owner, d.sg = &fakeExecutor{}, owner, sg
				a := e.buildWith(d, options{dial: func(context.Context, []byte, string) (talos.Reader, error) {
					dialled = true
					return nil, fmt.Errorf("unexpected node dial")
				}})
				got, err := a.observe(t.Context(), machine, f)
				if err != nil || dialled || got.Access != nil || got.Digest != nil || got.AssignmentEvidence != "" {
					t.Fatalf("failed read: err=%v dialled=%t observation=%+v", err, dialled, got)
				}
				var unread, recorded string
				err = e.db.QueryRow(`SELECT o.unread::text, row_to_json(o)::text || row_to_json(s)::text
					FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
					WHERE o.id = $1 AND s.purpose = $2 AND s.plan IS NOT DISTINCT FROM NULLIF($3, '')
					AND s.operation IS NOT DISTINCT FROM NULLIF($4, '')
					AND s.publication_generation IS NOT DISTINCT FROM NULLIF($5::bigint, 0) AND s.controller = $6
					AND o.access_path IS NULL AND o.access_version IS NULL AND o.access_created IS NULL AND o.access_serial IS NULL AND o.access_not_after IS NULL
					AND o.smbios_uuid IS NULL AND o.talos_node_id IS NULL AND o.talos_cluster_id IS NULL
					AND o.assignment_evidence IS NULL AND o.running_version IS NULL AND o.configuration_digest IS NULL
					AND o.resource_version IS NULL AND o.health IS NULL`, got.ID, f.purpose, f.plan, f.operation, f.generation, owner.ID).Scan(&unread, &recorded)
				if err != nil {
					t.Fatal(err)
				}
				var reasons map[string]map[string]string
				if err := json.Unmarshal([]byte(unread), &reasons); err != nil || fmt.Sprint(reasons) != fmt.Sprint(unreadAll(c.cause, false)) {
					t.Fatalf("unread=%s err=%v", unread, err)
				}
				if strings.Contains(recorded, sentinel) || e.logged(sentinel) ||
					count(t, e.db, `SELECT count(*) FROM machine_event WHERE entry::text LIKE '%' || $1 || '%'`, sentinel) != 0 {
					t.Fatal("provider error text reached the observation, timeline or logs")
				}
			})
		}
	}
}
