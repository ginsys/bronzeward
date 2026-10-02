package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// standInConfig is the machine configuration the stand-in node serves: one schema secret.
const standInConfig = "version: v1alpha1\nmachine:\n  type: worker\n  token: " + runToken +
	"\ncluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443\n"

var accessCreated = time.Date(2026, 10, 2, 9, 0, 0, 123456000, time.UTC)

// machineEnv is an ingestEnv whose machine (recorded by SMBIOS UUID uuidA) has a talostest
// stand-in as its Talos endpoint, reporting uuidA as Talos prints it (upper case), node ID nodeA,
// the cluster's Talos cluster ID and standInConfig, and whose provider answers the stand-in's
// talosconfig as the cluster's Talos access at version 3.
type machineEnv struct {
	*ingestEnv
	pki       talostest.PKI
	node      *talostest.Node
	clusterID string
	endpoint  string // the endpoint last recorded for a machine, which the access event names
}

func newMachineEnv(t *testing.T) *machineEnv {
	t.Helper()
	me := &machineEnv{ingestEnv: newIngestEnv(t, options{}), pki: talostest.NewPKI(t)}
	me.node = talostest.Serve(t, me.pki)
	me.setEndpoint(t, me.machine, me.node.Endpoint)
	if err := me.db.QueryRow(`SELECT talos_cluster_id FROM cluster WHERE id = $1`, me.cluster).Scan(&me.clusterID); err != nil {
		t.Fatal(err)
	}
	me.setAccess(me.pki.Talosconfig("", "", ""))
	me.node.SetSMBIOSUUID(t, strings.ToUpper(uuidA), true)
	me.node.SetNodeID(t, nodeA)
	me.node.SetClusterID(t, me.clusterID)
	me.node.SetMachineConfig(t, []byte(standInConfig))
	return me
}

func (me *machineEnv) setEndpoint(t *testing.T, machine, endpoint string) {
	t.Helper()
	if _, err := me.db.Exec(`UPDATE machine SET talos_endpoint = $2 WHERE id = $1`, machine, endpoint); err != nil {
		t.Fatal(err)
	}
	me.endpoint = endpoint
}

func (me *machineEnv) accessVersion() provider.TalosAccessVersion {
	return provider.TalosAccessVersion{Path: "access/talos/" + me.cluster, Version: 3, CreatedTime: accessCreated}
}

func (me *machineEnv) setAccess(talosconfig []byte) {
	me.f.mu.Lock()
	defer me.f.mu.Unlock()
	me.f.access, me.f.accessErr = provider.NewTalosAccess(me.accessVersion(), talosconfig), nil
}

// nodeMachine inventories a machine of me's cluster recorded by Talos node ID id, at me's node.
func (me *machineEnv) nodeMachine(t *testing.T, id string) string {
	t.Helper()
	m := decode[machineBody](t, me.do(me.api, nodeMachineCall(me.human("h-author"), "k-node-machine-0123", me.cluster, id)), http.StatusCreated).ID
	me.setEndpoint(t, m, me.node.Endpoint)
	return m
}

// run starts a source machine ingestion of machine and runs it to its end with o.
func (me *machineEnv) run(t *testing.T, machine string, o options) (op string, j job) {
	t.Helper()
	op, j = me.startJob(t, map[string]any{"source": "machine", "document": nil, "machine": machine})
	if j.node == nil {
		t.Fatal("the job carries no node")
	}
	me.runWith(t, o, j)
	return op, j
}

// wantAccessEvent asserts ev is the access record: the version identity and the endpoint dialled.
func (me *machineEnv) wantAccessEvent(t *testing.T, ev map[string]any) {
	t.Helper()
	want := map[string]any{"type": "talos-access", "path": "access/talos/" + me.cluster, "version": float64(3),
		"createdTime": "2026-10-02T09:00:00.123456Z", "endpoint": me.endpoint}
	if fmt.Sprint(ev) != fmt.Sprint(want) {
		t.Fatalf("access event %v; want %v", ev, want)
	}
}

// wantNodeFailed asserts op failed with code naming subject, its claim abandoned, no generation
// created and the draft unchanged, and its events: started, the access record when the access
// read returned a version, then failed with code and cause.
func (me *machineEnv) wantNodeFailed(t *testing.T, op, claim, etagBefore string, status int, code, cause, subject, id string,
	accessRead bool) {
	t.Helper()
	r := readOp(t, me.db, op)
	if r.state != "failed" || r.result != nil || r.owner.Valid || r.lease.Valid {
		t.Fatalf("operation %+v", r)
	}
	if r.error["type"] != "urn:bronzeward:problem:"+code || r.error["status"] != float64(status) || r.error["title"] != titles[code] ||
		r.error["instance"] != op || r.error[subject] != id {
		t.Fatalf("error %v; want %s naming %s %s", r.error, code, subject, id)
	}
	if _, ok := r.error["cause"]; ok {
		t.Fatalf("the problem carries the cause: %v", r.error)
	}
	evs := events(t, me.db, op)
	want := []string{"started", "failed"}
	if accessRead {
		want = []string{"started", "talos-access", "failed"}
		me.wantAccessEvent(t, evs[1])
	}
	if !slices.Equal(eventTypes(evs), want) {
		t.Fatalf("events %v; want %v", evs, want)
	}
	if last := evs[len(evs)-1]; last["code"] != code || last["cause"] != cause {
		t.Fatalf("terminal event %v; want code %s cause %s", last, code, cause)
	}
	if st, _, payload := claimRow(t, me.db, claim); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	if calls, created := me.f.paths(); calls != 0 || len(created) != 0 {
		t.Fatalf("%d generation creates, %v", calls, created)
	}
	me.draftUnchanged(t, etagBefore)
}

// leaked reports whether any of canaries appears in op's problem and events, the responses the
// environment answered, or the log.
func (me *machineEnv) leaked(t *testing.T, op string, canaries ...string) bool {
	t.Helper()
	var texts []string
	var perr string
	if err := me.db.QueryRow(`SELECT coalesce(error::text, '') FROM operation WHERE id = $1`, op).Scan(&perr); err != nil {
		t.Fatal(err)
	}
	texts = append(texts, perr)
	for _, e := range events(t, me.db, op) {
		texts = append(texts, fmt.Sprint(e))
	}
	texts = append(texts, me.bodies...)
	me.mu.Lock()
	texts = append(texts, me.logs...)
	me.mu.Unlock()
	for _, s := range texts {
		for _, c := range canaries {
			if strings.Contains(s, c) {
				return true
			}
		}
	}
	return false
}

// keyCanaries are the forms the stand-in's client key takes in a talosconfig and in its PEM.
func (me *machineEnv) keyCanaries() []string {
	pem := string(me.pki.KeyPEM)
	lines := strings.Split(pem, "\n")
	return []string{base64.StdEncoding.EncodeToString(me.pki.KeyPEM), lines[1]}
}

// PA §3.3, §8: a source machine ingestion reads the cluster's Talos access, records its version
// and the endpoint dialled as an event, dials the machine's endpoint, compares the identity the
// node reports (its SMBIOS UUID in Talos's upper case against the record's lower case, and the
// cluster ID), reads the machine configuration and ingests it as a document ingestion does: the
// schema secret becomes a generation and T1 records the import base.
func TestMachineIngestion(t *testing.T) {
	me := newMachineEnv(t)
	op, j := me.run(t, me.machine, options{})
	r := readOp(t, me.db, op)
	if r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	evs := events(t, me.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "talos-access", "succeeded"}) {
		t.Fatalf("events %v", evs)
	}
	me.wantAccessEvent(t, evs[1])
	if calls, created := me.f.paths(); calls != 1 || len(created) != 1 || !generationPath.MatchString(created[0]) {
		t.Fatalf("%d generation creates, %v", calls, created)
	}
	var doc string
	if err := me.db.QueryRow(`SELECT document FROM import_base_revision WHERE machine = $1`, me.machine).Scan(&doc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, runToken) || !strings.Contains(doc, "!bwref") {
		t.Fatalf("import base document %q", doc)
	}
	if reads := me.f.accessReads; !slices.Equal(reads, []string{me.cluster}) {
		t.Fatalf("access reads %v", reads)
	}
	if st, _, _ := claimRow(t, me.db, j.claim.ID); st != "released" {
		t.Fatalf("claim %s", st)
	}
	if me.leaked(t, op, me.keyCanaries()...) {
		t.Fatal("the talosconfig's key appears in the record or the log")
	}
}

// §3.3 controls: a machine recorded by Talos node ID whose node has no SystemInformation
// resource is accepted, and a machine recorded by SMBIOS UUID whose node reports a matching UUID
// and cluster ID but another node ID is accepted (the node ID is not its key).
func TestMachineIngestionIdentityControls(t *testing.T) {
	t.Run("node ID machine, no systeminformation", func(t *testing.T) {
		me := newMachineEnv(t)
		m := me.nodeMachine(t, nodeA)
		me.node.SetSMBIOSUUID(t, "", false)
		op, _ := me.run(t, m, options{})
		if r := readOp(t, me.db, op); r.state != "succeeded" {
			t.Fatalf("operation %+v", r)
		}
	})
	t.Run("UUID machine, another node ID", func(t *testing.T) {
		me := newMachineEnv(t)
		me.node.SetNodeID(t, "another-node-id-0123456789")
		op, _ := me.run(t, me.machine, options{})
		if r := readOp(t, me.db, op); r.state != "succeeded" {
			t.Fatalf("operation %+v", r)
		}
	})
}

// PA §3.3, §16: a node that is not the recorded machine, or whose identity read fails on an
// accepted connection, fails the ingestion 409 machine-identity-mismatch naming the machine, with
// the access record kept and nothing read kept: no generation, no draft change.
func TestMachineIdentityMismatch(t *testing.T) {
	for name, c := range map[string]struct {
		byNodeID bool
		set      func(t *testing.T, me *machineEnv)
		cause    string
	}{
		"another SMBIOS UUID": {false, func(t *testing.T, me *machineEnv) {
			me.node.SetSMBIOSUUID(t, "1C6B7D2F-3A4E-4F60-9BAC-1D2E3F4A5B6C", true)
		}, "smbios-uuid"},
		"no systeminformation for a UUID machine": {false, func(t *testing.T, me *machineEnv) {
			me.node.SetSMBIOSUUID(t, "", false)
		}, "smbios-uuid"},
		"an empty UUID for a UUID machine": {false, func(t *testing.T, me *machineEnv) {
			me.node.SetSMBIOSUUID(t, "", true)
		}, "smbios-uuid"},
		"a UUID reported for a node ID machine": {true, func(*testing.T, *machineEnv) {}, "smbios-uuid"},
		"another node ID for a node ID machine": {true, func(t *testing.T, me *machineEnv) {
			me.node.SetSMBIOSUUID(t, "", false)
			me.node.SetNodeID(t, "another-node-id-0123456789")
		}, "node-id"},
		"another cluster ID": {false, func(t *testing.T, me *machineEnv) {
			me.node.SetClusterID(t, talosClusterID("another cluster"))
		}, "cluster-id"},
		"a failing node identity read": {false, func(_ *testing.T, me *machineEnv) {
			me.node.Fail(talostest.IdentityType, codes.Internal)
		}, "identity-read"},
		"a failing systeminformation read for a node ID machine": {true, func(_ *testing.T, me *machineEnv) {
			me.node.Fail(talostest.SystemInformationType, codes.PermissionDenied)
		}, "identity-read"},
		"no cluster information": {false, func(_ *testing.T, me *machineEnv) {
			me.node.Fail(talostest.InfoType, codes.NotFound)
		}, "identity-read"},
	} {
		t.Run(name, func(t *testing.T) {
			me := newMachineEnv(t)
			m := me.machine
			if c.byNodeID {
				m = me.nodeMachine(t, nodeA)
			}
			c.set(t, me)
			before := me.currentETag(t)
			op, j := me.run(t, m, options{})
			me.wantNodeFailed(t, op, j.claim.ID, before, http.StatusConflict, "machine-identity-mismatch", c.cause, "machine", m, true)
			if me.leaked(t, op, append(me.keyCanaries(), "stand-in", runToken)...) {
				t.Fatal("the talosconfig, the node's text or the configuration appears in the record or the log")
			}
		})
	}
}

// silentEndpoint is a TCP listener that accepts and never answers: an endpoint that does not answer.
func silentEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { <-done; c.Close() }()
		}
	}()
	return l.Addr().String()
}

// PA §3.3, §16: each cause of talos-access-unavailable fails the ingestion 503 naming the cluster
// (a provider-side cause) or the machine (a node-side one), the claim abandoned, no generation and
// the draft unchanged; the access record is kept exactly when the access read returned a version,
// and no part of the talosconfig appears in the problem, the events, the responses or the log.
func TestMachineAccessUnavailable(t *testing.T) {
	for name, c := range map[string]struct {
		set        func(t *testing.T, me *machineEnv) (canaries []string)
		cause      string
		subject    string
		accessRead bool
	}{
		"absent secret": {func(_ *testing.T, me *machineEnv) []string {
			me.f.accessErr = fmt.Errorf("provider: GET: %w", provider.ErrAbsent)
			return nil
		}, "absent", "cluster", false},
		"read denied": {func(_ *testing.T, me *machineEnv) []string {
			me.f.accessErr = fmt.Errorf("provider: GET: %w", provider.ErrDenied)
			return nil
		}, "denied", "cluster", false},
		"provider sealed or partitioned": {func(_ *testing.T, me *machineEnv) []string {
			me.f.accessErr = fmt.Errorf("provider: GET: %w", provider.ErrUnavailable)
			return nil
		}, "provider-unavailable", "cluster", false},
		"malformed provider answer": {func(_ *testing.T, me *machineEnv) []string {
			me.f.accessErr = fmt.Errorf("provider: GET: %w", provider.ErrProtocol)
			return nil
		}, "malformed", "cluster", false},
		"malformed talosconfig with a synthetic key": {func(_ *testing.T, me *machineEnv) []string {
			bad := strings.Replace(string(me.pki.Talosconfig("", "", "")), "ca: ", "ca: not-base64-", 1)
			me.setAccess([]byte(bad))
			return []string{"not-base64-"}
		}, "talosconfig", "cluster", true},
		"a credential Talos refuses": {func(_ *testing.T, me *machineEnv) []string {
			other := talostest.NewPKI(t)
			me.setAccess(other.Talosconfig("", "", ""))
			return []string{base64.StdEncoding.EncodeToString(other.KeyPEM)}
		}, "node-unreachable", "machine", true},
		"an endpoint that does not answer": {func(t *testing.T, me *machineEnv) []string {
			me.setEndpoint(t, me.machine, silentEndpoint(t))
			return nil
		}, "node-unreachable", "machine", true},
		"a configuration read refused": {func(_ *testing.T, me *machineEnv) []string {
			me.node.Fail(talostest.MachineConfigType, codes.PermissionDenied)
			return []string{runToken}
		}, "machine-config", "machine", true},
	} {
		t.Run(name, func(t *testing.T) {
			me := newMachineEnv(t)
			canaries := append(c.set(t, me), me.keyCanaries()...)
			before := me.currentETag(t)
			op, j := me.startJob(t, map[string]any{"source": "machine", "document": nil})
			me.runWith(t, options{nodeTimeout: 500 * time.Millisecond}, j)
			id := me.cluster
			if c.subject == "machine" {
				id = me.machine
			}
			me.wantNodeFailed(t, op, j.claim.ID, before, http.StatusServiceUnavailable, "talos-access-unavailable", c.cause,
				c.subject, id, c.accessRead)
			if me.leaked(t, op, canaries...) {
				t.Fatal("the talosconfig or a read value appears in the record or the log")
			}
		})
	}
}

// §16's pass-through control: a dial whose error quotes the talosconfig is reported by its cause
// alone, and under quoteErrors, which logs the error text, the same scan catches the key, so the
// scan above can fail.
func TestMachineAccessErrorTextControl(t *testing.T) {
	quoting := func(_ context.Context, tc []byte, _ string) (talos.Reader, error) {
		return nil, errors.New("talos: cannot parse " + string(tc))
	}
	for _, quote := range []bool{false, true} {
		t.Run(fmt.Sprint("quoteErrors=", quote), func(t *testing.T) {
			me := newMachineEnv(t)
			op, j := me.startJob(t, map[string]any{"source": "machine", "document": nil})
			me.runWith(t, options{dial: quoting, quoteErrors: quote}, j)
			if r := readOp(t, me.db, op); r.state != "failed" {
				t.Fatalf("operation %+v", r)
			}
			if got := me.leaked(t, op, me.keyCanaries()...); got != quote {
				t.Fatalf("leaked %v with quoteErrors %v", got, quote)
			}
		})
	}
}

// §5.1: the access event commits under the claim's full fence, so a run whose claim lapsed (its
// lease, here, before the heartbeat noticed) records nothing and never dials the node: the
// operation keeps only its start, and the claim stays for the sweep.
func TestMachineAccessEventFencedByTheClaim(t *testing.T) {
	me := newMachineEnv(t)
	op, j := me.startJob(t, map[string]any{"source": "machine", "document": nil})
	mustExec(t, me.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, j.claim.ID)
	me.runWith(t, options{}, j)
	if evs := events(t, me.db, op); !slices.Equal(eventTypes(evs), []string{"started"}) {
		t.Fatalf("events %v", evs)
	}
	if seen := me.node.Seen(); len(seen) != 0 {
		t.Fatalf("%d node requests after the claim lapsed", len(seen))
	}
	if st, _, _ := claimRow(t, me.db, j.claim.ID); st != "held" {
		t.Fatalf("claim %s", st)
	}
	if calls, _ := me.f.paths(); calls != 0 {
		t.Fatalf("%d generation creates", calls)
	}
}
