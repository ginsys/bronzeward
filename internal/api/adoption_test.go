package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// adoptEnv is a planEnv whose machine has no Applied and no Desired, with an adopt plan of the
// target release allowing a minute-old observation, and a to commit its adoption as the
// controller of the current epoch.
type adoptEnv struct {
	*planEnv
	a                         *API
	pid, approval, assignment string
	uuid, nodeID              sql.NullString
	clusterID                 string
	baseline                  []byte
	expires                   time.Time
	keys                      int
}

func newAdoptEnv(t *testing.T, extra string, before ...func(*planEnv)) *adoptEnv {
	t.Helper()
	ae := &adoptEnv{planEnv: newPlanEnv(t)}
	for _, b := range before {
		b(ae.planEnv)
	}
	mustExec(t, ae.db, `UPDATE machine_state SET applied_release = NULL, applied_digest = NULL, applied_source = NULL,
		baseline_revision = NULL, desired = NULL WHERE machine = $1`, ae.machine)
	b := decode[planBody](t, ae.plan(ae.robot, "k-adopt-0123456789", adoptBody(ae.target.rel, ae.machine,
		`,"maxObservationAgeSeconds":60`+extra)), http.StatusCreated)
	ae.pid, ae.expires = b.ID, b.ExpiresAt
	if err := ae.db.QueryRow(`SELECT p.assignment_revision, m.smbios_uuid::text, m.talos_node_id, c.talos_cluster_id, b.configuration_digest
		FROM plan p JOIN machine m ON m.id = p.machine JOIN cluster c ON c.id = p.cluster
			JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
			JOIN import_base_revision b ON b.id = rm.import_base_revision
		WHERE p.id = $1`, ae.pid).Scan(&ae.assignment, &ae.uuid, &ae.nodeID, &ae.clusterID, &ae.baseline); err != nil {
		t.Fatal(err)
	}
	ae.a = observer(t, ae.env, &fakeExecutor{}, options{})
	return ae
}

func (ae *adoptEnv) key() string {
	ae.keys++
	return "k-adoption-" + string(rune('a'+ae.keys)) + "-0123456789"
}

// approveIt approves the adopt plan through T5a.
func (ae *adoptEnv) approveIt(t *testing.T) {
	t.Helper()
	ae.approval = decode[approvalBody](t, ae.approve(ae.human("h-approver"), ae.key(), ae.pid), http.StatusCreated).ID
}

// obsSeed is one observation of the machine as an observer records it: its start entry, begun age
// ago, and unless startOnly its observation entry, showing these values.
type obsSeed struct {
	age                 time.Duration
	uuid, nodeID        any
	clusterID, evidence string
	digest              []byte
	startOnly           bool
	unread              string // the observation's unread object, "{}" when empty
}

// observed records an observation of the machine showing its identity, the baseline's digest and
// the bound assignment revision, as change leaves them, and answers its id ("" for a start only).
func (ae *adoptEnv) observed(t *testing.T, change func(*obsSeed)) string {
	t.Helper()
	s := obsSeed{uuid: ae.uuid, nodeID: ae.nodeID, clusterID: ae.clusterID, evidence: ae.assignment, digest: ae.baseline,
		unread: "{}"}
	if change != nil {
		change(&s)
	}
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var basis int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, i.epoch, 'observation-started', jsonb_build_object('purpose', 'drift', 'endpoint', m.talos_endpoint::text,
			'controller', 'test'), now() - make_interval(secs => $3)
		FROM installation_state i, machine m WHERE m.id = $1`, ae.machine, basis, s.age.Seconds())
	mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
		SELECT $1, $2, 'drift', talos_endpoint, 'test' FROM machine WHERE id = $1`, ae.machine, basis)
	obs := ""
	if !s.startOnly {
		obs = id.New(id.Observation)
		var rev int64
		if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
			ae.machine).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'drift', 'basis', $4::bigint), now()
			FROM installation_state`, ae.machine, rev, obs, basis)
		mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
				smbios_uuid, talos_node_id, talos_cluster_id, assignment_evidence, configuration_digest, unread, at)
			VALUES ($1, $2, $3, $4, 'access/talos/' || $5, 3, '2026-10-01T00:00:00Z', $6::uuid, $10, $7, NULLIF($8, ''), $9, $11::jsonb, now())`,
			obs, ae.machine, basis, rev, ae.cluster, s.uuid, s.clusterID, s.evidence, s.digest, s.nodeID, s.unread)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return obs
}

// T6 (persistence-api.md §5; execution-recovery.md §6.3 step 4): an approved adopt plan whose
// latest observation, begun after the approval and within the plan's age, shows the machine's
// identity, the baseline's digest and the bound assignment revision is committed to a completed
// adopt operation with its adoption record: one adoption entry naming the plan, operation,
// approval, observation, controller and the five comparisons, then one Applied-change entry. The
// machine's Applied becomes the release, with the baseline's digest and source adoption, its
// Desired the release, and its baseline revision 1. A later start whose observation is not
// recorded does not count; a freeze does not block.
func TestAdoption(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	obs := ae.observed(t, nil)
	ae.observed(t, func(s *obsSeed) { s.startOnly = true })
	mustExec(t, ae.db, `UPDATE machine SET frozen = true WHERE id = $1`, ae.machine)
	r, err := ae.a.commitAdoption(context.Background(), ae.pid)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan != ae.pid || r.Observation != obs || id.MustHave(r.Operation, id.Operation) != nil {
		t.Fatalf("adopted %+v, observation %s", r, obs)
	}
	ep := epoch(t, ae.db)
	var at time.Time
	if err := ae.db.QueryRow(`SELECT e.at FROM machine_event e
			JOIN adoption_record a ON a.machine = e.machine AND a.revision = e.revision
			JOIN operation o ON o.id = a.operation
			JOIN plan_state s ON s.plan = a.plan
			JOIN machine_state ms ON ms.machine = a.machine
		WHERE e.machine = $1 AND e.revision = $2 AND e.kind = 'adoption' AND e.epoch = $3
			AND e.entry = jsonb_build_object('plan', $4::text, 'operation', $5::text, 'approval', $6::text, 'observation', $7::text,
				'controller', $8::text, 'comparisons', jsonb_build_array('4.1', '4.2', '4.3', '4.4', '4.5'))
			AND a.plan = $4 AND a.operation = $5 AND a.approval = $6 AND a.observation = $7 AND a.epoch = $3 AND a.at = e.at
			AND a.baseline_digest = $9 AND a.cluster = $10
			AND o.kind = 'adopt' AND o.state = 'completed' AND o.epoch = $3 AND o.plan = $4 AND o.machine = $1 AND o.created_at = e.at
			AND o.result = jsonb_build_object('adoptionRecord', $4::text)
			AND s.state = 'committed' AND s.operation = $5 AND s.updated_at = e.at
			AND ms.desired = $11 AND ms.applied_release = $11 AND ms.applied_digest = $9 AND ms.applied_source = 'adoption'
			AND ms.baseline_revision = 1`,
		ae.machine, r.Revision, ep, ae.pid, r.Operation, ae.approval, obs, ae.a.d.owner.ID, ae.baseline, ae.cluster,
		ae.target.rel).Scan(&at); err != nil {
		t.Fatalf("adoption record: %v", err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'applied-change'
		AND epoch = $3 AND at = $4 AND entry = jsonb_build_object('from', NULL::text, 'to', $5::text, 'digest', $6::text,
			'source', 'adoption', 'plan', $7::text)`,
		ae.machine, r.Revision+1, ep, at, ae.target.rel, hex.EncodeToString(ae.baseline), ae.pid); n != 1 {
		t.Fatalf("%d Applied-change entries", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine WHERE id = $1 AND revision_counter = $2`, ae.machine, r.Revision+1); n != 1 {
		t.Fatal("the revision counter is not the Applied-change entry's")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal'`); n != 0 {
		t.Fatalf("%d refusal entries", n)
	}
}

// A machine in recovery mode adopts only once its scope is released in the current epoch (§7.5).
func TestAdoptionRecoveryModeReleased(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	ae.observed(t, nil)
	mustExec(t, ae.db, `UPDATE installation_state SET recovery_mode = true`)
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, ae.machine)
	if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
		t.Fatal(err)
	}
}

// A machine registered by its Talos node ID (choice §10.26) is identified by it and no SMBIOS UUID
// (execution-recovery.md §3.2 comparison 3): an observation showing another node ID, or a UUID
// beside its own node ID, is refused by 4.4; one showing its own node ID alone is adopted.
func TestAdoptionNodeIdentity(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		change func(*obsSeed)
	}{
		{"another node ID", func(s *obsSeed) { s.nodeID = "node-b" }},
		{"a UUID reported", func(s *obsSeed) { s.uuid = "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAdoptEnv(t, "", func(p *planEnv) { // the machine as registered by node ID, before any plan names it
				mustExec(t, p.db, `ALTER TABLE machine DISABLE TRIGGER identity`)
				mustExec(t, p.db, `UPDATE machine SET smbios_uuid = NULL, talos_node_id = 'node-a' WHERE id = $1`, p.machine)
				mustExec(t, p.db, `ALTER TABLE machine ENABLE TRIGGER identity`)
			})
			ae.approveIt(t)
			ae.observed(t, c.change)
			_, err := ae.a.commitAdoption(context.Background(), ae.pid)
			ae.wantRefused(t, err, 0, "4.4", "the latest observation shows another machine identity")
			ae.observed(t, nil)
			if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// T6 reads the time after every lock, its machine state's included (persistence-api.md §5 rule
// 4): a commitment that waited on a publication holding every machine state of the cluster judges
// the plan's expiry when it resumes.
func TestAdoptionWaitsForMachineState(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, `,"expiresInSeconds":3`)
	ae.approveIt(t)
	ae.observed(t, nil)
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `SELECT 1 FROM machine_state s JOIN machine m ON m.id = s.machine WHERE m.cluster = $1
		ORDER BY s.machine FOR UPDATE OF s`, ae.cluster)
	done := make(chan error, 1)
	go func() {
		_, err := ae.a.commitAdoption(context.Background(), ae.pid)
		done <- err
	}()
	waitForLockWaits(t, ae.db, 1)
	time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ae.wantRefused(t, <-done, 0, "4.2", "the plan has expired")
}

// wantRefused holds err to a refusal by requirement comparison with cause, recorded as one
// refusal entry naming T6, the comparison, the plan, the cause and the controller, and nothing
// else written: no adoption record, no further operation, Applied unchanged.
func (ae *adoptEnv) wantRefused(t *testing.T, err error, operations int, comparison, cause string) {
	t.Helper()
	var refused *adoptionRefused
	if !errors.As(err, &refused) || refused.Comparison != comparison || refused.Cause != cause {
		t.Fatalf("got %v, want a refusal by %s: %s", err, comparison, cause)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal' AND epoch = (SELECT epoch FROM
		installation_state) AND entry = jsonb_build_object('transaction', 'T6', 'comparison', $2::text, 'plan', $3::text,
			'cause', $4::text, 'controller', $5::text)`, ae.machine, comparison, ae.pid, cause, ae.a.d.owner.ID); n != 1 {
		t.Fatalf("%d refusal entries", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine WHERE id = $1
		AND revision_counter = (SELECT max(revision) FROM machine_event WHERE machine = $1)`, ae.machine); n != 1 {
		t.Fatal("the revision counter is not the last entry's")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM adoption_record WHERE plan = $1`, ae.pid); n != operations {
		t.Fatalf("%d adoption records", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE plan = $1`, ae.pid); n != operations {
		t.Fatalf("%d operations for the plan", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind IN ('adoption', 'applied-change')`,
		ae.machine); n != 2*operations {
		t.Fatalf("%d adoption and Applied-change entries", n)
	}
}

// T6's refusals (execution-recovery.md §6.3 step 4): each requirement that fails is a refusal
// entry recorded after the refused transaction, and nothing else is written.
func TestAdoptionRefusals(t *testing.T) {
	t.Parallel()
	ready := func(t *testing.T, ae *adoptEnv) {
		ae.approveIt(t)
		ae.observed(t, nil)
	}
	obsWith := func(change func(*obsSeed)) func(*testing.T, *adoptEnv) {
		return func(t *testing.T, ae *adoptEnv) {
			ae.approveIt(t)
			ae.observed(t, change)
		}
	}
	for _, c := range []struct {
		name, extra, comparison, cause string
		setup                          func(*testing.T, *adoptEnv)
	}{
		{"proposed", "", "4.2", "the plan is proposed", func(t *testing.T, ae *adoptEnv) { ae.observed(t, nil) }},
		{"approval revoked", "", "4.2", "the plan is revoked", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			if rec := ae.revokeApproval(ae.human("h-recovery"), ae.key(), ae.approval, `{"reason":"wrong"}`); rec.Code != http.StatusCreated {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		}},
		{"approver revoked", "", "4.2", "the approver's identity is revoked", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			revocationOf(t, revoke(ae.env, ae.human("h-recovery"), ae.key(),
				`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`))
		}},
		{"expired", `,"expiresInSeconds":1`, "4.2", "the plan has expired", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
		}},
		{"expired unapproved", `,"expiresInSeconds":1`, "4.2", "the plan has expired", func(t *testing.T, ae *adoptEnv) {
			ae.observed(t, nil)
			time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
		}},
		{"approver revoked before expiry", `,"expiresInSeconds":2`, "4.2", "the approver's identity is revoked",
			func(t *testing.T, ae *adoptEnv) {
				ready(t, ae)
				revocationOf(t, revoke(ae.env, ae.human("h-recovery"), ae.key(),
					`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`))
				time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
			}},
		{"approver revoked after expiry", `,"expiresInSeconds":1`, "4.2", "the plan has expired", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
			revocationOf(t, revoke(ae.env, ae.human("h-recovery"), ae.key(),
				`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`))
		}},
		{"assignment changed", "", "4.3", "the machine's assignment revision changed", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			rev := id.New(id.AssignmentRevision)
			mustExec(t, ae.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
				rev, ae.cluster, ae.machine, ae.seed)
			mustExec(t, ae.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 2 WHERE machine = $1`, ae.machine, rev)
		}},
		{"Desired changed", "", "4.3", "the machine's Desired release changed", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			mustExec(t, ae.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, ae.machine, ae.target.rel)
		}},
		{"Applied recorded", "", "4.3", "the machine has an Applied release", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			mustExec(t, ae.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
				baseline_revision = 1 WHERE machine = $1`, ae.machine, ae.applied.rel, ae.appliedDigest)
		}},
		{"no observation", "", "4.4", "the machine has no recorded observation", func(t *testing.T, ae *adoptEnv) { ae.approveIt(t) }},
		{"start only", "", "4.4", "the machine has no recorded observation",
			obsWith(func(s *obsSeed) { s.startOnly = true })},
		{"observed before the approval", "", "4.4", "the latest observation began before the approval",
			func(t *testing.T, ae *adoptEnv) {
				ae.observed(t, nil)
				ae.approveIt(t)
			}},
		{"too old", "", "4.4", "the latest observation is older than the plan allows",
			obsWith(func(s *obsSeed) { s.age = 2 * time.Minute })},
		{"another machine", "", "4.4", "the latest observation shows another machine identity",
			obsWith(func(s *obsSeed) { s.uuid = "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b" })},
		{"no identity", "", "4.4", "the latest observation shows another machine identity",
			obsWith(func(s *obsSeed) { s.uuid = nil })},
		{"another cluster", "", "4.4", "the latest observation shows another machine identity",
			obsWith(func(s *obsSeed) { s.clusterID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" })},
		{"another digest", "", "4.4", "the latest observation's configuration digest is not the baseline's",
			obsWith(func(s *obsSeed) { s.digest = bytes.Repeat([]byte{9}, 32) })},
		{"an earlier observation matched", "", "4.4", "the latest observation's configuration digest is not the baseline's",
			func(t *testing.T, ae *adoptEnv) {
				ready(t, ae)
				ae.observed(t, func(s *obsSeed) { s.digest = bytes.Repeat([]byte{9}, 32) })
			}},
		{"no digest", "", "4.4", "the latest observation's configuration digest is not the baseline's",
			obsWith(func(s *obsSeed) { s.digest = nil })},
		{"another assignment", "", "4.4", "the latest observation shows another assignment revision",
			obsWith(func(s *obsSeed) { s.evidence = id.New(id.AssignmentRevision) })},
		{"scope blocked", "", "4.5", "the machine scope is blocked", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			mustExec(t, ae.db, `UPDATE machine SET scope_state = 'blocked' WHERE id = $1`, ae.machine)
		}},
		{"recovery mode", "", "4.5", "recovery mode is in effect and the scope is not released", func(t *testing.T, ae *adoptEnv) {
			ready(t, ae)
			mustExec(t, ae.db, `UPDATE installation_state SET recovery_mode = true`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAdoptEnv(t, c.extra)
			c.setup(t, ae)
			_, err := ae.a.commitAdoption(context.Background(), ae.pid)
			ae.wantRefused(t, err, 0, c.comparison, c.cause)
		})
	}
}

// A second commitment of an adopted plan is refused by 4.1, the plan already having its operation.
func TestAdoptionSecondCommitment(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	ae.observed(t, nil)
	if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
		t.Fatal(err)
	}
	_, err := ae.a.commitAdoption(context.Background(), ae.pid)
	ae.wantAlreadyAdopted(t, err)
}

// Two attempts racing on one plan, as two controller instances' loops do: the machine's lock orders
// them, one records the adoption and the other finds it recorded, so no refusal follows it.
func TestAdoptionConcurrentCommitment(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	ae.observed(t, nil)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := ae.a.commitAdoption(context.Background(), ae.pid)
			errs <- err
		}()
	}
	var failed error
	for range 2 {
		if err := <-errs; err != nil {
			if failed != nil {
				t.Fatalf("both attempts failed: %v; %v", failed, err)
			}
			failed = err
		}
	}
	ae.wantAlreadyAdopted(t, failed)
}

// wantAlreadyAdopted checks that err found the plan's adoption already recorded: not a refusal, no
// refusal entry, and the one adopt operation left.
func (ae *adoptEnv) wantAlreadyAdopted(t *testing.T, err error) {
	t.Helper()
	var refused *adoptionRefused
	if !errors.Is(err, errAlreadyAdopted) || errors.As(err, &refused) {
		t.Fatalf("got %v, want the adoption already recorded", err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ae.machine); n != 0 {
		t.Fatalf("%d refusal entries", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE plan = $1`, ae.pid); n != 1 {
		t.Fatalf("%d operations", n)
	}
}

// An observation the record relies on that left the identity, the configuration or the assignment
// evidence unread is no evidence either way (§6.3 step 4, choice §10.27): the record rolls back
// without a refusal entry, even when an earlier observation was complete, and a later complete
// observation commits it.
func TestAdoptionUnreadEvidence(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		change func(*obsSeed)
	}{
		{"identity", func(s *obsSeed) { s.uuid, s.nodeID, s.unread = nil, nil, `{"identity":{"cause":"identity-read"}}` }},
		{"configuration", func(s *obsSeed) { s.digest, s.unread = nil, `{"configuration":{"cause":"machine-config"}}` }},
		{"assignmentEvidence", func(s *obsSeed) { s.evidence, s.unread = "", `{"assignmentEvidence":{"cause":"denied"}}` }},
		{"identity older than the plan allows", func(s *obsSeed) { // a read that waited out the node's timeout
			s.uuid, s.nodeID, s.unread, s.age = nil, nil, `{"identity":{"cause":"node-unreachable"}}`, 2*time.Minute
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAdoptEnv(t, "")
			ae.approveIt(t)
			ae.observed(t, nil)
			ae.observed(t, c.change)
			_, err := ae.a.commitAdoption(context.Background(), ae.pid)
			var refused *adoptionRefused
			if !errors.Is(err, errEvidenceUnread) || errors.As(err, &refused) {
				t.Fatalf("got %v, want the evidence unread and no refusal", err)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ae.machine); n != 0 {
				t.Fatalf("%d refusal entries", n)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE plan = $1`, ae.pid); n != 0 {
				t.Fatalf("%d operations for the plan", n)
			}
			ae.observed(t, nil)
			if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An apply-config operation holding the machine scope refuses the adoption by 4.5.
func TestAdoptionScopeHeld(t *testing.T) {
	t.Parallel()
	var other string
	ae := newAdoptEnv(t, "", func(p *planEnv) {
		other = decode[planBody](t, p.plan(p.robot, "k-apply-0123456789", applyBody(p.target.rel, p.machine, "")), http.StatusCreated).ID
		decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-apply-0123", other), http.StatusCreated)
	})
	ae.approveIt(t)
	ae.observed(t, nil)
	ae.commitAs(t, other)
	_, err := ae.a.commitAdoption(context.Background(), ae.pid)
	ae.wantRefused(t, err, 0, "4.5", "an operation holds the machine scope")
}

// An approval of an earlier epoch is refused by 4.2 in the current one; a controller of an
// earlier epoch records nothing, not even a refusal entry.
func TestAdoptionEpochs(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	stale := ae.a
	newEpoch(t, ae.db)
	ae.observed(t, nil)
	if _, err := stale.commitAdoption(context.Background(), ae.pid); !errors.Is(err, errNotController) {
		t.Fatalf("stale controller: %v", err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal'`); n != 0 {
		t.Fatalf("%d refusal entries by a stale controller", n)
	}
	ae.a = observer(t, ae.env, &fakeExecutor{}, options{})
	_, err := ae.a.commitAdoption(context.Background(), ae.pid)
	ae.wantRefused(t, err, 0, "4.2", "the approval is of an earlier epoch")
}

// A commitment that arrives while a revocation of the approver's identity holds its principal waits
// for it on the principal's FOR SHARE, and then is refused by 4.2.
func TestAdoptionWaitsForRevocation(t *testing.T) {
	t.Parallel()
	ae := newAdoptEnv(t, "")
	ae.approveIt(t)
	ae.observed(t, nil)
	approver := ae.principalOf("h-approver")
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `UPDATE principal SET revoked = true WHERE id = $1`, approver)
	done := make(chan error, 1)
	go func() {
		_, err := ae.a.commitAdoption(context.Background(), ae.pid)
		done <- err
	}()
	waitForLockWaits(t, ae.db, 1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ae.wantRefused(t, <-done, 0, "4.2", "the approver's identity is revoked")
}

// A revocation of the approval, or of the approver's identity, that arrives while the commitment
// holds its locks waits for it, so it cannot pass between the commitment's checks and its commit:
// the plan is adopted under an approval that was in force, and the revocation finds it committed.
func TestAdoptionRevocationsWait(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		revoke func(ae *adoptEnv) *httptest.ResponseRecorder
	}{
		{"approval", func(ae *adoptEnv) *httptest.ResponseRecorder {
			return ae.revokeApproval(ae.human("h-recovery"), "k-revoke-approval-0123", ae.approval, `{"reason":"wrong"}`)
		}},
		{"identity", func(ae *adoptEnv) *httptest.ResponseRecorder {
			return revoke(ae.env, ae.human("h-recovery"), "k-revoke-identity-0123",
				`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAdoptEnv(t, "")
			ae.approveIt(t)
			ae.observed(t, nil)
			ae.principalOf("h-approver")
			done := make(chan *httptest.ResponseRecorder, 1)
			ae.a = observer(t, ae.env, &fakeExecutor{}, options{commit: func(tx *sql.Tx) error {
				go func() { done <- c.revoke(ae) }()
				waitForLockWaits(t, ae.db, 1)
				return tx.Commit()
			}})
			if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
				t.Fatal(err)
			}
			rec := <-done
			var adoptedAt time.Time
			if err := ae.db.QueryRow(`SELECT at FROM adoption_record WHERE plan = $1`, ae.pid).Scan(&adoptedAt); err != nil {
				t.Fatal(err)
			}
			if b := decode[struct {
				At time.Time `json:"at"`
			}](t, rec, http.StatusCreated); !b.At.After(adoptedAt) {
				t.Fatalf("revocation at %s, adoption at %s", b.At, adoptedAt)
			}
			if n := count(t, ae.db, `SELECT (SELECT count(*) FROM approval_revocation WHERE approval = $1)
				+ (SELECT count(*) FROM identity_revocation WHERE identity = $2)`, ae.approval, ae.principalOf("h-approver")); n != 1 {
				t.Fatalf("%d revocation records", n)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'committed'`, ae.pid); n != 1 {
				t.Fatalf("plan not committed after the revocation (%d %s)", rec.Code, rec.Body)
			}
		})
	}
}
