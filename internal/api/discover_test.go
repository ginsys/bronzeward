package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/talos/signer"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// discoveryEnv is an env with no signer identity (a discovery reads no provider) and an author.
type discoveryEnv struct {
	*env
	author string
	base   [3]int // the acts, first-key rows and idempotency records the environment starts with
}

func newDiscoveryEnv(t *testing.T, o options, maxCert time.Duration) *discoveryEnv {
	t.Helper()
	e := newEnvWith(t, deps{ing: &fakeIngester{latest: 1}, maxCert: maxCert}, o)
	d := &discoveryEnv{env: e, author: e.human("h-author")}
	d.base[0], d.base[1], d.base[2] = d.rows(t)
	return d
}

// discoveryNode is a Talos stand-in of the cluster "c-disc" trusting ca, listing members. It holds a
// control plane configuration with ca's key, so every kind of first key can be read through it.
func discoveryNode(t *testing.T, ca *x509.PEMEncodedCertificateAndKey, members ...talostest.Member) (*talostest.Node, talostest.PKI) {
	t.Helper()
	pki := talostest.NewPKIFromCA(t, ca)
	n := talostest.Serve(t, pki)
	n.SetMachineConfig(t, []byte(controlPlaneConfig(ca)))
	n.SetNodeID(t, "n1")
	n.SetClusterID(t, "c-disc")
	n.SetMembers(t, members...)
	return n, pki
}

// memberPort listens on 127.0.0.1:50000, the port a discovery dials each member at, accepts every
// connection and answers none of them, as a member that hangs does. The count is the connections made.
func memberPort(t *testing.T) *atomic.Int32 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:50000")
	if err != nil {
		t.Fatalf("a discovery dials its members at port 50000, and this test needs 127.0.0.1:50000: %v", err)
	}
	var accepted atomic.Int32
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			t.Cleanup(func() { c.Close() })
		}
	}()
	return &accepted
}

func discoveryCall(tok, kind, document, endpoint string) call {
	b, err := json.Marshal(map[string]string{"kind": kind, "document": document, "endpoint": endpoint})
	if err != nil {
		panic(err)
	}
	return call{method: "POST", path: prefix + "/discoveries", token: tok, body: string(b)}
}

// rows counts what a discovery must not write: acts, first-key rows and idempotency records. The
// environment starts with an act of its own, so a test compares with noWrites and not with zero.
func (d *discoveryEnv) rows(t *testing.T) (acts, firstKeys, records int) {
	t.Helper()
	return count(t, d.db, "SELECT count(*) FROM act"), count(t, d.db, "SELECT count(*) FROM first_key_act"),
		count(t, d.db, "SELECT count(*) FROM idempotency_record")
}

// noWrites fails the test when a call changed any of the three counts since the environment began.
func (d *discoveryEnv) noWrites(t *testing.T, after string) {
	t.Helper()
	acts, keys, records := d.rows(t)
	if got := [3]int{acts, keys, records}; got != d.base {
		t.Fatalf("%d acts, %d first-key rows and %d idempotency records after %s; the environment started with %v", acts, keys, records, after, d.base)
	}
}

// The two members of the stand-ins below are dialled at 127.0.0.2:50000 and 127.0.0.3:50000, which
// refuse at once: they are listed unanswered, and the deadline is not the cause.
var refusingMembers = []talostest.Member{
	{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"127.0.0.2"}, ControlPlanePort: 6443},
	{NodeID: "n2", Hostname: "w-1", Addresses: []string{"127.0.0.3"}},
}

// PA §9.2: each kind of first key finds the cluster's identity, version, contract and members, with an
// answer that holds no byte of the document or the key. The call needs no signer, no Idempotency-Key and
// no cluster, and it records nothing: no act, no first-key row, no idempotency record, and a log line
// that names the principal and the count and no endpoint or member address.
func TestDiscoveryKinds(t *testing.T) {
	for _, kind := range []string{"machine-config", "secrets-bundle", "talosconfig"} {
		t.Run(kind, func(t *testing.T) {
			d := newDiscoveryEnv(t, options{}, 0)
			ca := newTalosCA(t)
			n, pki := discoveryNode(t, ca, refusingMembers...)
			doc := map[string]string{"machine-config": controlPlaneConfig(ca), "secrets-bundle": secretsBundle(ca),
				"talosconfig": string(pki.Talosconfig("a:1", "", ""))}[kind]
			rec := d.do(d.api, discoveryCall(d.author, kind, doc, n.Endpoint))
			b := decode[discoveryBody](t, rec, http.StatusOK)
			if b.ClusterID != "c-disc" || b.Version != "v1.14.5" || b.Contract != "v1.14" || b.Endpoint != n.Endpoint || !b.Complete ||
				b.KubernetesEndpoint != "https://127.0.0.2:6443" || len(b.Members) != 2 {
				t.Fatalf("answer %+v", b)
			}
			if m := b.Members[0]; m.NodeID != "n1" || m.Hostname != "cp-1" || m.Address != "127.0.0.2" || m.Role != "controlplane" || m.Answered || m.SMBIOSUUID != "" || m.Platform != "" {
				t.Errorf("the control plane member: %+v", m)
			}
			if m := b.Members[1]; m.NodeID != "n2" || m.Role != "worker" || m.Answered {
				t.Errorf("the worker member: %+v", m)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
			}
			for _, secret := range []string{b64(ca.Key), string(ca.Key), strings.TrimSpace(strings.Split(string(ca.Key), "\n")[1])} {
				if strings.Contains(rec.Body.String(), secret) || d.logged(secret) {
					t.Fatalf("the answer or the log holds key material: %s", rec.Body)
				}
			}
			if !d.logged("200 discovery by ") || !d.logged("2 members, complete true") {
				t.Errorf("no log line for the call: %v", d.logs)
			}
			for _, value := range []string{n.Endpoint, "127.0.0.2", "127.0.0.3", "c-disc"} {
				if d.logged(value) {
					t.Errorf("the log holds %q", value)
				}
			}
			d.noWrites(t, "a discovery")
		})
	}
}

// §9.2, §10.3: author, human only; a refusal of the request comes before any dial. A body outside the
// route's members (a Talos context among them), a missing or malformed endpoint, an unknown kind, an
// empty document and a query are 400.
func TestDiscoveryInputRefusals(t *testing.T) {
	d := newDiscoveryEnv(t, options{}, 0)
	ca := newTalosCA(t)
	n, _ := discoveryNode(t, ca, refusingMembers...)
	doc := controlPlaneConfig(ca)
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	raw := func(body string) call {
		return call{method: "POST", path: prefix + "/discoveries", token: d.author, body: body}
	}
	withQuery := discoveryCall(d.author, "machine-config", doc, n.Endpoint)
	withQuery.path += "?talosContext=x"
	for name, c := range map[string]struct {
		call   call
		status int
		code   string
	}{
		"viewer":            {discoveryCall(d.human("h-viewer"), "machine-config", doc, n.Endpoint), 403, "forbidden"},
		"automation":        {discoveryCall(d.robot, "machine-config", doc, n.Endpoint), 403, "forbidden"},
		"no endpoint":       {discoveryCall(d.author, "machine-config", doc, ""), 400, "invalid-request"},
		"DNS endpoint":      {discoveryCall(d.author, "machine-config", doc, "node.example.test:50000"), 400, "invalid-request"},
		"unknown kind":      {discoveryCall(d.author, "kubeconfig", doc, n.Endpoint), 400, "invalid-request"},
		"empty document":    {discoveryCall(d.author, "machine-config", "", n.Endpoint), 400, "invalid-request"},
		"no document":       {raw(`{"kind":"machine-config","endpoint":"` + n.Endpoint + `"}`), 400, "invalid-request"},
		"document not text": {raw(`{"kind":"machine-config","document":1,"endpoint":"` + n.Endpoint + `"}`), 400, "invalid-request"},
		"a Talos context":   {raw(`{"kind":"machine-config","document":` + q(doc) + `,"endpoint":"` + n.Endpoint + `","talosContext":"x"}`), 400, "invalid-request"},
		"a query":           {withQuery, 400, "invalid-request"},
	} {
		rec := d.do(d.api, c.call)
		wantProblem(t, rec, c.status, c.code)
		if strings.Contains(rec.Body.String(), b64(ca.Key)) {
			t.Errorf("%s: the problem quotes the document", name)
		}
	}
	if seen := n.Seen(); len(seen) != 0 {
		t.Fatalf("%d requests reached the node after refusals", len(seen))
	}
	d.noWrites(t, "refusals")
}

// §9.2: the one POST that only reads takes no Idempotency-Key and no expected epoch. A call that sends
// both is not refused for them, and a second call with the same key is read again, not replayed: it
// reaches the node as the first did.
func TestDiscoveryTakesNoKeyOrEpoch(t *testing.T) {
	d := newDiscoveryEnv(t, options{}, 0)
	ca := newTalosCA(t)
	n, _ := discoveryNode(t, ca, refusingMembers...)
	c := discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), n.Endpoint)
	c.key, c.expected = "k-disc-0123456789abcd", id.New(id.Epoch)
	decode[discoveryBody](t, d.do(d.api, c), http.StatusOK)
	first := len(n.Seen())
	decode[discoveryBody](t, d.do(d.api, c), http.StatusOK)
	if second := len(n.Seen()); first == 0 || second <= first {
		t.Fatalf("the node saw %d requests after the first call and %d after the second: the second was replayed", first, second)
	}
	d.noWrites(t, "two calls with a key and an epoch")
}

// §3.4: the first key's own rules are 422 here too, each with its rule and no byte of the document.
func TestDiscoveryRuleRefusals(t *testing.T) {
	d := newDiscoveryEnv(t, options{}, 0)
	ca := newTalosCA(t)
	n, pki := discoveryNode(t, ca, refusingMembers...)
	workerNode := talostest.Serve(t, pki)
	workerNode.SetMachineConfig(t, []byte(workerConfig(ca)))
	const marker = "zq-marker-9c2e"
	for name, c := range map[string]struct {
		kind, doc, endpoint, rule string
	}{
		"garbage machine configuration": {"machine-config", marker, n.Endpoint, "first-key-unusable"},
		"garbage secrets bundle":        {"secrets-bundle", marker, n.Endpoint, "first-key-unusable"},
		"garbage talosconfig":           {"talosconfig", marker, n.Endpoint, "first-key-unusable"},
		"worker configuration":          {"machine-config", workerConfig(ca), n.Endpoint, "first-key-no-ca-key"},
		"bundle with no key":            {"secrets-bundle", "certs:\n  os:\n    crt: " + b64(ca.Crt) + "\n", n.Endpoint, "first-key-no-ca-key"},
		"talosconfig at a worker":       {"talosconfig", string(pki.Talosconfig("a:1", "", "")), workerNode.Endpoint, "first-key-endpoint-no-control-plane"},
	} {
		rec := d.do(d.api, discoveryCall(d.author, c.kind, c.doc, c.endpoint))
		doc := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
		if doc["rule"] != c.rule {
			t.Errorf("%s: rule %v; want %s", name, doc["rule"], c.rule)
		}
		if strings.Contains(rec.Body.String(), marker) || strings.Contains(rec.Body.String(), b64(ca.Key)) {
			t.Errorf("%s: the problem quotes the document: %s", name, rec.Body)
		}
	}
	d.noWrites(t, "refusals")
}

// §9.2: an endpoint that does not answer, and an endpoint that refuses a certificate signed from the key,
// are the same 503 talos-access-unavailable with the same text: a refused key is not told apart from an
// unreachable endpoint.
func TestDiscoveryUnreachableAndRefusedKey(t *testing.T) {
	d := newDiscoveryEnv(t, options{}, 0)
	ca := newTalosCA(t)
	other := newTalosCA(t)
	n, _ := discoveryNode(t, other, refusingMembers...) // trusts another CA: it refuses ca's certificate
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().String()
	l.Close()
	var details []any
	for name, endpoint := range map[string]string{"unreachable": closed, "refused key": n.Endpoint} {
		rec := d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), endpoint))
		doc := wantProblem(t, rec, http.StatusServiceUnavailable, "talos-access-unavailable")
		details = append(details, doc["detail"])
		if strings.Contains(rec.Body.String(), b64(ca.Key)) || strings.Contains(rec.Body.String(), endpoint) {
			t.Errorf("%s: the problem quotes the document or the endpoint: %s", name, rec.Body)
		}
	}
	if len(details) != 2 || details[0] != details[1] {
		t.Fatalf("the two refusals differ: %v", details)
	}
	d.noWrites(t, "refusals")
}

// §9.2: a cluster that lists more than 128 members is 422 validation-failed with rule too-many-members and
// the limit, before any member is dialled; one at the limit is read whole.
func TestDiscoveryTooManyMembers(t *testing.T) {
	d := newDiscoveryEnv(t, options{}, 0)
	dialled := memberPort(t)
	ca := newTalosCA(t)
	members := func(n int) []talostest.Member {
		var ms []talostest.Member
		for i := range n {
			ms = append(ms, talostest.Member{NodeID: "n" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Hostname: "h", Addresses: []string{"127.0.0.1"}})
		}
		return ms
	}
	over, _ := discoveryNode(t, ca, members(129)...)
	doc := wantProblem(t, d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), over.Endpoint)),
		http.StatusUnprocessableEntity, "validation-failed")
	if doc["rule"] != "too-many-members" || doc["limit"] != float64(128) {
		t.Fatalf("problem %v; want rule too-many-members and limit 128", doc)
	}
	if got := dialled.Load(); got != 0 {
		t.Fatalf("%d members were dialled before the refusal", got)
	}
	at, _ := discoveryNode(t, ca, members(128)...)
	b := decode[discoveryBody](t, d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), at.Endpoint)), http.StatusOK)
	if len(b.Members) != 128 {
		t.Fatalf("%d members at the limit", len(b.Members))
	}
}

// §9.2: at the deadline the members read so far are answered, the unread ones listed unanswered, and
// complete is false, so a client does not read an unread member as down. A call whose members all
// refused at once is complete.
func TestDiscoveryDeadlineKeepsTheMembersRead(t *testing.T) {
	// The node timeout bounds the extraction and nothing else: the walk lasts the 600 ms deadline, six times the node timeout, and is still answered.
	d := newDiscoveryEnv(t, options{nodeTimeout: 100 * time.Millisecond, discoverWithin: 600 * time.Millisecond}, 0)
	memberPort(t) // 127.0.0.1:50000 accepts and never answers, so a read of that member waits for the deadline
	ca := newTalosCA(t)
	n, _ := discoveryNode(t, ca,
		talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"127.0.0.1"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n2", Hostname: "w-1", Addresses: []string{"127.0.0.2"}})
	start := time.Now()
	b := decode[discoveryBody](t, d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), n.Endpoint)), http.StatusOK)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the call took %v: the deadline did not bound it", took)
	}
	if b.ClusterID != "c-disc" || b.Complete || len(b.Members) != 2 || b.Members[0].Answered || b.Members[1].Answered {
		t.Fatalf("answer %+v; want the identity, two members unanswered and complete false", b)
	}
	if !d.logged("complete false") {
		t.Errorf("the log line does not say the walk was cut: %v", d.logs)
	}
}

// §9.2: a deadline that passes before the cluster's identity is read is 503 talos-access-unavailable.
func TestDiscoveryDeadlineBeforeTheIdentity(t *testing.T) {
	d := newDiscoveryEnv(t, options{discoverWithin: 300 * time.Millisecond}, 0)
	ca := newTalosCA(t)
	n, _ := discoveryNode(t, ca, refusingMembers...)
	n.StallVersion(true)
	wantProblem(t, d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), n.Endpoint)),
		http.StatusServiceUnavailable, "talos-access-unavailable")
}

// §9.2: the reader certificate is valid for the deadline plus the signer's two seconds of rounding slack,
// so it must fit the deployment's maximum less that slack: one nanosecond short is 503
// dependency-unavailable with nothing dialled.
func TestDiscoveryCertificateLife(t *testing.T) {
	within := 20 * time.Second
	ca := newTalosCA(t)
	for _, c := range []struct {
		name    string
		maxCert time.Duration
		status  int
	}{
		{"the deadline and the slack fit", within + 2*signer.RoundingSlack, http.StatusOK},
		{"one nanosecond short", within + 2*signer.RoundingSlack - 1, http.StatusServiceUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newDiscoveryEnv(t, options{discoverWithin: within}, c.maxCert)
			n, _ := discoveryNode(t, ca, refusingMembers...)
			rec := d.do(d.api, discoveryCall(d.author, "machine-config", controlPlaneConfig(ca), n.Endpoint))
			if c.status == http.StatusOK {
				decode[discoveryBody](t, rec, http.StatusOK)
				return
			}
			wantProblem(t, rec, http.StatusServiceUnavailable, "dependency-unavailable")
			if seen := n.Seen(); len(seen) != 0 {
				t.Fatalf("%d requests reached the node", len(seen))
			}
		})
	}
}
