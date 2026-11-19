package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// kvStandIn is an in-memory KV v2 holding versions by path, spoken to by the real provider.Signer
// over HTTP. It enforces check-and-set as OpenBao does. before runs ahead of a request without the
// lock held, so a test can move the path between the signer's read and its write.
type kvStandIn struct {
	mu       sync.Mutex
	versions map[string][]kvVersion
	writes   int
	before   func(method, path string)
	status   map[string]int // a method's forced status, 0 for none
}

type kvVersion struct {
	ca, key string
	created time.Time
}

func (k *kvStandIn) put(path, ca, key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.versions[path] = append(k.versions[path], kvVersion{ca, key, time.Now().UTC().Truncate(time.Second)})
}

func (k *kvStandIn) latest(path string) (int, kvVersion) {
	k.mu.Lock()
	defer k.mu.Unlock()
	vs := k.versions[path]
	if len(vs) == 0 {
		return 0, kvVersion{}
	}
	return len(vs), vs[len(vs)-1]
}

func (k *kvStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")
	if k.before != nil {
		k.before(r.Method, path)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if s := k.status[r.Method]; s != 0 {
		w.WriteHeader(s)
		return
	}
	vs := k.versions[path]
	switch r.Method {
	case http.MethodGet:
		if len(vs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[]}`)
			return
		}
		v := vs[len(vs)-1]
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data":     map[string]string{"ca": v.ca, "key": v.key},
			"metadata": map[string]any{"version": len(vs), "created_time": v.created.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": false},
		}})
	case http.MethodPost:
		var in struct {
			Options struct{ CAS int } `json:"options"`
			Data    struct{ CA, Key string }
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if in.Options.CAS != len(vs) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errors":["check-and-set parameter did not match the current version"]}`)
			return
		}
		k.writes++
		v := kvVersion{in.Data.CA, in.Data.Key, time.Now().UTC().Truncate(time.Second)}
		k.versions[path] = append(vs, v)
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": len(vs) + 1, "created_time": v.created.Format(time.RFC3339Nano)}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// firstKeyEnv is an env with a signer over a kvStandIn, one cluster and its author.
type firstKeyEnv struct {
	*env
	kv      *kvStandIn
	author  string
	cluster string
	path    string
}

func newFirstKeyEnv(t *testing.T, withSigner bool) *firstKeyEnv {
	t.Helper()
	kv := &kvStandIn{versions: map[string][]kvVersion{}, status: map[string]int{}}
	srv := httptest.NewServer(kv)
	t.Cleanup(srv.Close)
	d := deps{ing: &fakeIngester{latest: 1}}
	if withSigner {
		tokenFile := filepath.Join(t.TempDir(), "signer.token")
		if err := os.WriteFile(tokenFile, []byte("s.signer-test-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tok, err := provider.ReadTokenFile(tokenFile)
		if err != nil {
			t.Fatal(err)
		}
		if d.sg, err = provider.NewSigner(srv.URL, tok); err != nil {
			t.Fatal(err)
		}
	}
	e := newEnvWith(t, d, options{})
	f := &firstKeyEnv{env: e, kv: kv, author: e.human("h-author")}
	f.cluster = e.createCluster(e.api, f.author, "k-fk-cluster-0123456789")
	f.path = "access/talos-ca/" + f.cluster
	return f
}

func newTalosCA(t *testing.T) *x509.PEMEncodedCertificateAndKey {
	t.Helper()
	ca, err := secrets.NewTalosCA(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &x509.PEMEncodedCertificateAndKey{Crt: ca.CrtPEM, Key: ca.KeyPEM}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func controlPlaneConfig(ca *x509.PEMEncodedCertificateAndKey) string {
	return "version: v1alpha1\nmachine:\n  type: controlplane\n  ca:\n    crt: " + b64(ca.Crt) + "\n    key: " + b64(ca.Key) + "\n"
}

func workerConfig(ca *x509.PEMEncodedCertificateAndKey) string {
	return "version: v1alpha1\nmachine:\n  type: worker\n  ca:\n    crt: " + b64(ca.Crt) + "\n"
}

func secretsBundle(ca *x509.PEMEncodedCertificateAndKey) string {
	return "certs:\n  os:\n    crt: " + b64(ca.Crt) + "\n    key: " + b64(ca.Key) + "\n"
}

// trustingNode is a Talos stand-in trusting ca, holding a control plane configuration with ca's key.
func trustingNode(t *testing.T, ca *x509.PEMEncodedCertificateAndKey) (*talostest.Node, talostest.PKI) {
	t.Helper()
	pki := talostest.NewPKIFromCA(t, ca)
	n := talostest.Serve(t, pki)
	n.SetMachineConfig(t, []byte(controlPlaneConfig(ca)))
	return n, pki
}

func firstKeyCall(tok, key, cluster, kind, document, endpoint string) call {
	b, err := json.Marshal(map[string]string{"kind": kind, "document": document, "endpoint": endpoint})
	if err != nil {
		panic(err)
	}
	return call{method: "POST", path: prefix + "/clusters/" + cluster + "/first-keys", token: tok, key: key, body: string(b)}
}

// firstKeyRows counts the acts and FirstKeyAct rows for a cluster.
func firstKeyRows(t *testing.T, f *firstKeyEnv) (acts, rows int) {
	t.Helper()
	return count(t, f.db, "SELECT count(*) FROM act WHERE action = 'cluster.first-key'"),
		count(t, f.db, "SELECT count(*) FROM first_key_act WHERE cluster = $1", f.cluster)
}

// PA §3.4: each of the three first keys, tried at the endpoint, stores the cluster's CA key and
// records one act with its FirstKeyAct row (author, the cluster, the kind, not a replacement, the
// CA certificate's fingerprint and the key's version identity), and answers the version. The answer,
// the act's rows and the log hold no byte of the document or the key.
func TestFirstKeyKinds(t *testing.T) {
	for _, kind := range []string{"machine-config", "secrets-bundle", "talosconfig"} {
		t.Run(kind, func(t *testing.T) {
			f := newFirstKeyEnv(t, true)
			ca := newTalosCA(t)
			n, pki := trustingNode(t, ca)
			doc := map[string]string{"machine-config": controlPlaneConfig(ca), "secrets-bundle": secretsBundle(ca),
				"talosconfig": string(pki.Talosconfig("a:1", "", ""))}[kind]
			rec := f.do(f.api, firstKeyCall(f.author, "k-fk-1-0123456789", f.cluster, kind, doc, n.Endpoint))
			b := decode[firstKeyBody](t, rec, http.StatusOK)
			ver, stored := f.kv.latest(f.path)
			if b.Cluster != f.cluster || b.Kind != kind || b.Replaced || b.CAKeyVersion.Version != 1 || ver != 1 ||
				!b.CAKeyVersion.CreatedTime.Equal(stored.created) {
				t.Fatalf("answer %+v, stored version %d at %v", b, ver, stored.created)
			}
			if stored.ca != string(ca.Crt) || stored.key != string(ca.Key) {
				t.Fatal("the stored key is not the document's CA")
			}
			for _, secret := range []string{b64(ca.Key), string(ca.Key), strings.TrimSpace(strings.Split(string(ca.Key), "\n")[1])} {
				if strings.Contains(rec.Body.String(), secret) || f.logged(secret) {
					t.Fatalf("the answer or the log holds key material: %s", rec.Body)
				}
			}
			if acts, rows := firstKeyRows(t, f); acts != 1 || rows != 1 {
				t.Fatalf("%d acts, %d rows", acts, rows)
			}
			fp, ok := caFingerprint(provider.NewCAKey(ca.Crt, ca.Key))
			if !ok {
				t.Fatal("no fingerprint")
			}
			if n := count(t, f.db, `SELECT count(*) FROM first_key_act k JOIN act a ON a.id = k.act
				WHERE k.cluster = $1 AND k.kind = $2 AND NOT k.replaced AND k.ca_fingerprint = $3 AND k.key_path = $4
				AND k.key_version = 1 AND k.key_created_time = $5 AND a.role = 'author' AND a.via = 'api'
				AND a.subjects = ARRAY[$1] AND a.principal_kind = 'human'`,
				f.cluster, kind, fp, f.path, stored.created); n != 1 {
				t.Fatalf("%d matching act and row", n)
			}
		})
	}
}

// PA §3.4: a second first key replaces the first as the next version, and the answer says so. The old
// version stays in the provider as history.
func TestFirstKeyReplacement(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	ca1, ca2 := newTalosCA(t), newTalosCA(t)
	n1, _ := trustingNode(t, ca1)
	n2, _ := trustingNode(t, ca2)
	decode[firstKeyBody](t, f.do(f.api, firstKeyCall(f.author, "k-fk-r1-0123456789", f.cluster, "machine-config", controlPlaneConfig(ca1), n1.Endpoint)), http.StatusOK)
	b := decode[firstKeyBody](t, f.do(f.api, firstKeyCall(f.author, "k-fk-r2-0123456789", f.cluster, "secrets-bundle", secretsBundle(ca2), n2.Endpoint)), http.StatusOK)
	if !b.Replaced || b.CAKeyVersion.Version != 2 {
		t.Fatalf("answer %+v", b)
	}
	ver, stored := f.kv.latest(f.path)
	if ver != 2 || stored.ca != string(ca2.Crt) || f.kv.versions[f.path][0].ca != string(ca1.Crt) {
		t.Fatalf("%d versions; the history is not kept", ver)
	}
	if n := count(t, f.db, "SELECT count(*) FROM first_key_act WHERE cluster = $1 AND replaced AND key_version = 2", f.cluster); n != 1 {
		t.Fatalf("%d replacement rows", n)
	}
}

// PA §3.4, §7.1: a replay under the same key answers the same body and writes nothing more, to the
// provider or the database; the same key with another document is refused.
func TestFirstKeyReplay(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	ca := newTalosCA(t)
	n, _ := trustingNode(t, ca)
	c := firstKeyCall(f.author, "k-fk-replay-0123456789", f.cluster, "machine-config", controlPlaneConfig(ca), n.Endpoint)
	first := decode[firstKeyBody](t, f.do(f.api, c), http.StatusOK)
	if again := decode[firstKeyBody](t, f.do(f.api, c), http.StatusOK); again != first {
		t.Fatalf("replay %+v; want %+v", again, first)
	}
	if acts, rows := firstKeyRows(t, f); acts != 1 || rows != 1 || f.kv.writes != 1 {
		t.Fatalf("%d acts, %d rows, %d provider writes", acts, rows, f.kv.writes)
	}
	other := firstKeyCall(f.author, "k-fk-replay-0123456789", f.cluster, "machine-config", controlPlaneConfig(newTalosCA(t)), n.Endpoint)
	if rec := f.do(f.api, other); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusConflict {
		t.Fatalf("the same key with another document: %d %s", rec.Code, rec.Body)
	}
	if f.kv.writes != 1 {
		t.Fatalf("%d provider writes", f.kv.writes)
	}
}

// PA §3.4: a key is stored only after a node accepted a certificate signed from it, so a document whose
// CA the node does not trust is 503 talos-access-unavailable with nothing written, and a stored key
// stays as it was. The problem quotes no byte of the document.
func TestFirstKeyTryRefusal(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	good := newTalosCA(t)
	n, _ := trustingNode(t, good)
	decode[firstKeyBody](t, f.do(f.api, firstKeyCall(f.author, "k-fk-t1-0123456789", f.cluster, "machine-config", controlPlaneConfig(good), n.Endpoint)), http.StatusOK)
	unrelated := newTalosCA(t)
	rec := f.do(f.api, firstKeyCall(f.author, "k-fk-t2-0123456789", f.cluster, "machine-config", controlPlaneConfig(unrelated), n.Endpoint))
	wantProblem(t, rec, http.StatusServiceUnavailable, "talos-access-unavailable")
	if strings.Contains(rec.Body.String(), b64(unrelated.Key)) {
		t.Fatalf("the problem holds the document: %s", rec.Body)
	}
	if ver, stored := f.kv.latest(f.path); ver != 1 || stored.ca != string(good.Crt) || f.kv.writes != 1 {
		t.Fatalf("stored version %d, %d writes: the refused key moved the stored one", ver, f.kv.writes)
	}
	if acts, rows := firstKeyRows(t, f); acts != 1 || rows != 1 {
		t.Fatalf("%d acts, %d rows after the refused try", acts, rows)
	}
	// An endpoint nobody answers at is the same refusal.
	dead := httptest.NewServer(http.NotFoundHandler())
	endpoint := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()
	wantProblem(t, f.do(f.api, firstKeyCall(f.author, "k-fk-t3-0123456789", f.cluster, "secrets-bundle", secretsBundle(good), endpoint)),
		http.StatusServiceUnavailable, "talos-access-unavailable")
	if f.kv.writes != 1 {
		t.Fatalf("%d writes", f.kv.writes)
	}
}

// PA §3.4: the signer's own refusals are 422 validation-failed with a rule, and write nothing.
func TestFirstKeyRuleRefusals(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	ca := newTalosCA(t)
	n, pki := trustingNode(t, ca)
	workerNode := talostest.Serve(t, pki)
	workerNode.SetMachineConfig(t, []byte(workerConfig(ca)))
	const marker = "zq-marker-5d1e"
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
		rec := f.do(f.api, firstKeyCall(f.author, "k-fk-rule-0123456789", f.cluster, c.kind, c.doc, c.endpoint))
		doc := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
		if doc["rule"] != c.rule {
			t.Errorf("%s: rule %v; want %s", name, doc["rule"], c.rule)
		}
		if strings.Contains(rec.Body.String(), marker) || strings.Contains(rec.Body.String(), b64(ca.Key)) {
			t.Errorf("%s: the problem quotes the document: %s", name, rec.Body)
		}
	}
	if acts, rows := firstKeyRows(t, f); acts != 0 || rows != 0 || f.kv.writes != 0 {
		t.Fatalf("%d acts, %d rows, %d writes after refusals", acts, rows, f.kv.writes)
	}
}

// §9.2, §10.3, §3.4: author, human only. A body outside the route's members, a missing or malformed
// endpoint, an unknown kind and an empty document are 400 and write nothing; an unknown cluster is 404.
func TestFirstKeyInputRefusals(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	ca := newTalosCA(t)
	n, _ := trustingNode(t, ca)
	doc := controlPlaneConfig(ca)
	raw := func(tok, cluster, body string) call {
		return call{method: "POST", path: prefix + "/clusters/" + cluster + "/first-keys", token: tok, key: "k-fk-in-aaaaaaaaaa", body: body}
	}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	for name, c := range map[string]struct {
		call   call
		status int
		code   string
	}{
		"viewer":            {firstKeyCall(f.human("h-viewer"), "k-fk-in-0123456789", f.cluster, "machine-config", doc, n.Endpoint), 403, "forbidden"},
		"automation":        {firstKeyCall(f.robot, "k-fk-in-0123456789", f.cluster, "machine-config", doc, n.Endpoint), 403, "forbidden"},
		"no endpoint":       {firstKeyCall(f.author, "k-fk-in-0123456789", f.cluster, "machine-config", doc, ""), 400, "invalid-request"},
		"DNS endpoint":      {firstKeyCall(f.author, "k-fk-in-0123456789", f.cluster, "machine-config", doc, "node.example.test:50000"), 400, "invalid-request"},
		"unknown kind":      {firstKeyCall(f.author, "k-fk-in-0123456789", f.cluster, "kubeconfig", doc, n.Endpoint), 400, "invalid-request"},
		"empty document":    {firstKeyCall(f.author, "k-fk-in-0123456789", f.cluster, "machine-config", "", n.Endpoint), 400, "invalid-request"},
		"no document":       {raw(f.author, f.cluster, `{"kind":"machine-config","endpoint":"`+n.Endpoint+`"}`), 400, "invalid-request"},
		"document not text": {raw(f.author, f.cluster, `{"kind":"machine-config","document":1,"endpoint":"`+n.Endpoint+`"}`), 400, "invalid-request"},
		"document with NUL": {raw(f.author, f.cluster, `{"kind":"machine-config","document":"a\u0000b","endpoint":"`+n.Endpoint+`"}`), 400, "invalid-request"},
		"unknown member":    {raw(f.author, f.cluster, `{"kind":"machine-config","document":`+q(doc)+`,"endpoint":"`+n.Endpoint+`","x":1}`), 400, "invalid-request"},
		"unknown cluster":   {firstKeyCall(f.author, "k-fk-in-0123456789", id.New(id.Cluster), "machine-config", doc, n.Endpoint), 404, "not-found"},
		"not a cluster id":  {firstKeyCall(f.author, "k-fk-in-0123456789", id.New(id.Machine), "machine-config", doc, n.Endpoint), 404, "not-found"},
	} {
		rec := f.do(f.api, c.call)
		wantProblem(t, rec, c.status, c.code)
		if strings.Contains(rec.Body.String(), b64(ca.Key)) {
			t.Errorf("%s: the problem quotes the document", name)
		}
	}
	if acts, rows := firstKeyRows(t, f); acts != 0 || rows != 0 || f.kv.writes != 0 {
		t.Fatalf("%d acts, %d rows, %d writes after refusals", acts, rows, f.kv.writes)
	}
}

// §3.4: a server with no signer identity refuses a first key with 503 dependency-unavailable.
func TestFirstKeyNoSigner(t *testing.T) {
	f := newFirstKeyEnv(t, false)
	ca := newTalosCA(t)
	n, _ := trustingNode(t, ca)
	wantProblem(t, f.do(f.api, firstKeyCall(f.author, "k-fk-ns-0123456789", f.cluster, "machine-config", controlPlaneConfig(ca), n.Endpoint)),
		http.StatusServiceUnavailable, "dependency-unavailable")
	if acts, rows := firstKeyRows(t, f); acts != 0 || rows != 0 {
		t.Fatalf("%d acts, %d rows", acts, rows)
	}
}

// §3.4: two supplies racing for one cluster cannot both write. The write carries the version read, so a
// path that moved between the read and the write is 409 conflict with nothing recorded, and the other
// supply's key stays.
func TestFirstKeyRaceIsConflict(t *testing.T) {
	f := newFirstKeyEnv(t, true)
	ca, rival := newTalosCA(t), newTalosCA(t)
	n, _ := trustingNode(t, ca)
	var once sync.Once
	f.kv.before = func(method, path string) {
		if method == http.MethodPost {
			once.Do(func() { f.kv.put(path, string(rival.Crt), string(rival.Key)) })
		}
	}
	wantProblem(t, f.do(f.api, firstKeyCall(f.author, "k-fk-race-0123456789", f.cluster, "machine-config", controlPlaneConfig(ca), n.Endpoint)),
		http.StatusConflict, "conflict")
	if ver, stored := f.kv.latest(f.path); ver != 1 || stored.ca != string(rival.Crt) || f.kv.writes != 0 {
		t.Fatalf("stored version %d, %d writes: the loser wrote", ver, f.kv.writes)
	}
	if acts, rows := firstKeyRows(t, f); acts != 0 || rows != 0 {
		t.Fatalf("%d acts, %d rows", acts, rows)
	}
}

// §3.4: a provider that cannot be read or written is 503 dependency-unavailable; nothing is recorded.
func TestFirstKeyProviderFailure(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s %d", method, status), func(t *testing.T) {
				f := newFirstKeyEnv(t, true)
				f.kv.status[method] = status
				ca := newTalosCA(t)
				n, _ := trustingNode(t, ca)
				wantProblem(t, f.do(f.api, firstKeyCall(f.author, "k-fk-pf-0123456789", f.cluster, "machine-config", controlPlaneConfig(ca), n.Endpoint)),
					http.StatusServiceUnavailable, "dependency-unavailable")
				if acts, rows := firstKeyRows(t, f); acts != 0 || rows != 0 || f.kv.writes != 0 {
					t.Fatalf("%d acts, %d rows, %d writes", acts, rows, f.kv.writes)
				}
			})
		}
	}
}

// The document type prints a placeholder under every verb and does not marshal, so a log line or an
// encoder that reaches it holds none of it.
func TestFirstKeyDocumentRedacts(t *testing.T) {
	var d firstKeyDocument
	if err := json.Unmarshal([]byte(`"secret-text"`), &d); err != nil {
		t.Fatal(err)
	}
	in := &firstKeyInput{Kind: "machine-config", Document: d, Endpoint: "10.0.0.1:50000"}
	for _, v := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		if got := fmt.Sprintf(v, d) + fmt.Sprintf(v, in) + fmt.Sprintf(v, &in.Document); strings.Contains(got, "secret") {
			t.Errorf("%s prints the document: %s", v, got)
		}
	}
	if b, err := json.Marshal(in); err == nil || strings.Contains(string(b), "secret") {
		t.Errorf("marshal: %q, %v", b, err)
	}
	for name, bad := range map[string]string{"empty": `""`, "not a string": `1`, "NUL": `"a\u0000"`, "replacement": "\"a�\"", "lone surrogate": `"\ud800"`} {
		var d firstKeyDocument
		if err := json.Unmarshal([]byte(bad), &d); err == nil || d.size() != 0 {
			t.Errorf("%s accepted", name)
		}
	}
}
