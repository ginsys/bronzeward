package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// PA §16: every observation purpose records a failed access read without node
// values or provider error text. The success/stale-version controls are in
// TestObservationRecordsTheAccessItRead; drift's node-side stand-in checks are separate.
func TestObservationPurposeAccessFailure(t *testing.T) {
	ae := newAttemptEnv(t, "")
	const sentinel = "synthetic-access-failure-key"
	for _, f := range []observeFor{
		{purpose: "evidence", plan: ae.pid},
		{purpose: "completion", plan: ae.pid, operation: ae.op},
		{purpose: "recovery", plan: ae.pid, operation: ae.op},
		{purpose: "restoration"},
		{purpose: "drift"},
	} {
		for _, c := range []struct {
			name, cause string
			err         error
		}{
			{"absent", "absent", provider.ErrAbsent},
			{"denied", "denied", provider.ErrDenied},
			{"malformed", "malformed", provider.ErrProtocol},
			{"unavailable", "provider-unavailable", provider.ErrUnavailable},
			{"status", "provider-unavailable", provider.ErrStatus},
		} {
			t.Run(f.purpose+"/"+c.name, func(t *testing.T) {
				var dialled bool
				x := &fakeExecutor{err: fmt.Errorf("%s: %w", sentinel, c.err)}
				a := observer(t, ae.env, x, options{dial: func(context.Context, []byte, string) (talos.Reader, error) {
					dialled = true
					return nil, fmt.Errorf("unexpected node dial")
				}})
				got, err := a.observe(t.Context(), ae.machine, f)
				if err != nil || dialled || got.Access != nil || got.Digest != nil || got.AssignmentEvidence != "" {
					t.Fatalf("failed read: err=%v dialled=%t observation=%+v", err, dialled, got)
				}
				var unread, recorded string
				err = ae.db.QueryRow(`SELECT o.unread::text, row_to_json(o)::text || row_to_json(s)::text
					FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
					WHERE o.id = $1 AND s.purpose = $2 AND s.plan IS NOT DISTINCT FROM NULLIF($3, '')
					AND s.operation IS NOT DISTINCT FROM NULLIF($4, '')
					AND o.access_path IS NULL AND o.access_version IS NULL AND o.access_created IS NULL
					AND o.smbios_uuid IS NULL AND o.talos_node_id IS NULL AND o.talos_cluster_id IS NULL
					AND o.assignment_evidence IS NULL AND o.running_version IS NULL AND o.configuration_digest IS NULL
					AND o.resource_version IS NULL AND o.health IS NULL`, got.ID, f.purpose, f.plan, f.operation).Scan(&unread, &recorded)
				if err != nil {
					t.Fatal(err)
				}
				var reasons map[string]map[string]string
				if err := json.Unmarshal([]byte(unread), &reasons); err != nil || fmt.Sprint(reasons) != fmt.Sprint(unreadAll(c.cause, false)) {
					t.Fatalf("unread=%s err=%v", unread, err)
				}
				if strings.Contains(recorded, sentinel) || ae.logged(sentinel) ||
					count(t, ae.db, `SELECT count(*) FROM machine_event WHERE entry::text LIKE '%' || $1 || '%'`, sentinel) != 0 {
					t.Fatal("provider error text reached the observation, timeline or logs")
				}
			})
		}
	}
}
