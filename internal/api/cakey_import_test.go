package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// An import offers machine.ca.key to the signer and keeps a verdict, never the key
// (compilation.md §2.3 step 3a, persistence-api.md §16).

type caImportEnv struct {
	*ingestEnv
	ca string // the stored key's base64 form, synthetic
}

// newCAImportEnv is an ingestion environment whose signer holds a stored key and a first-key act for
// its cluster.
func newCAImportEnv(t *testing.T) *caImportEnv {
	t.Helper()
	ie := newIngestEnv(t, options{})
	ie.d.sg = ie.signer()
	ie.api = ie.build(ie.captured)
	return &caImportEnv{ingestEnv: ie, ca: b64(ie.keys.ca.Key)}
}

func controlPlaneDoc(key string) string {
	return "version: v1alpha1\nmachine:\n  type: controlplane\n  token: " + runToken + "\n  ca:\n    key: " + key + "\n"
}

// importKey runs one import of a control plane document holding key to its end.
func (ce *caImportEnv) importKey(t *testing.T, key string) (string, job) {
	t.Helper()
	op, j := ce.startJob(t, map[string]any{"document": controlPlaneDoc(key)})
	ce.api.runIngest(t.Context(), j)
	return op, j
}

func verdictEvent(t *testing.T, ce *caImportEnv, op string) map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range events(t, ce.db, op) {
		if e["type"] == "ca-key-match" {
			out = append(out, e)
		}
	}
	if len(out) != 1 {
		t.Fatalf("%d ca-key-match events in %v", len(out), events(t, ce.db, op))
	}
	return out[0]
}

// refusedImport asserts op failed with code, nothing of it kept and no generation created.
func (ce *caImportEnv) refusedImport(t *testing.T, op, claim string, status int, code, verdict string) map[string]any {
	t.Helper()
	r := readOp(t, ce.db, op)
	if r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:"+code || r.error["status"] != float64(status) {
		t.Fatalf("operation %+v", r)
	}
	if got := eventTypes(events(t, ce.db, op)); !slices.Equal(got, []string{"started", "ca-key-match", "failed"}) {
		t.Fatalf("events %v", got)
	}
	if got := verdictEvent(t, ce, op)["verdict"]; got != verdict {
		t.Fatalf("verdict %v, want %s", got, verdict)
	}
	if st, _, payload := claimRow(t, ce.db, claim); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	if ce.f.calls != 0 {
		t.Fatalf("%d generations created", ce.f.calls)
	}
	return r.error
}

// scanned asserts that neither the offered value, in either form, nor a digest of one is in the
// database, the logs or a response; control is a text the same scan must find.
func (ce *caImportEnv) scanned(t *testing.T, offered, control string) {
	t.Helper()
	pem, err := base64.StdEncoding.DecodeString(offered)
	if err != nil {
		t.Fatal(err)
	}
	// The PEM body's first full line, so that a row's escaped newlines cannot hide the key.
	lines := strings.Split(string(pem), "\n")
	for _, s := range []string{offered, lines[1]} {
		assertAbsent(t, ce.ingestEnv, s)
	}
	for _, v := range []string{offered, string(pem)} {
		sum := sha256.Sum256([]byte(v))
		for _, d := range []string{hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(sum[:])} {
			assertAbsent(t, ce.ingestEnv, d)
		}
	}
	if len(tablesHolding(t, ce.db, control)) == 0 {
		t.Fatalf("control: the scan does not find %q", control)
	}
}

func TestImportMatchesTheStoredCAKey(t *testing.T) {
	ce := newCAImportEnv(t)
	op, _ := ce.importKey(t, ce.ca)
	r := readOp(t, ce.db, op)
	if r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	ibr := r.result["importBaseRevision"].(string)
	act := latestActVersion(t, ce.db, ce.cluster)
	e := verdictEvent(t, ce, op)
	if e["verdict"] != "match" || e["path"] != act.Path || e["version"] != float64(act.Version) || e["createdTime"] == nil {
		t.Fatalf("event %v", e)
	}
	var doc string
	if err := ce.db.QueryRow(`SELECT document FROM import_base_revision WHERE id = $1`, ibr).Scan(&doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "key: !bwref "+ingest.CAKeyName) {
		t.Fatalf("document:\n%s", doc)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM import_base_reference WHERE revision = $1 AND name = $2 AND kind = 'talos-ca-key'
		AND version IS NULL AND generation IS NULL`, ibr, ingest.CAKeyName); n != 1 {
		t.Fatalf("%d typed rows", n)
	}
	// The token's generation alone.
	if ce.f.calls != 1 {
		t.Fatalf("%d generations created", ce.f.calls)
	}
	ce.scanned(t, ce.ca, act.Path)
}

// Another cluster's key and the stored key altered by one byte are refused before any provider
// write; the byte-for-byte forms are provider's TestCAKeyHolds.
func TestImportRefusesAnotherCAKey(t *testing.T) {
	for name, alter := range map[string]func(t *testing.T, stored []byte) []byte{
		"another cluster's key": func(t *testing.T, _ []byte) []byte { return newTalosCA(t).Key },
		"one byte altered": func(_ *testing.T, stored []byte) []byte {
			out := slices.Clone(stored)
			out[len(out)/2] ^= 1
			return out
		},
		"no trailing newline": func(_ *testing.T, stored []byte) []byte { return []byte(strings.TrimSuffix(string(stored), "\n")) },
	} {
		t.Run(name, func(t *testing.T) {
			ce := newCAImportEnv(t)
			offered := b64(alter(t, ce.keys.ca.Key))
			etag := ce.currentETag(t)
			op, j := ce.importKey(t, offered)
			problem := ce.refusedImport(t, op, j.claim.ID, http.StatusUnprocessableEntity, "validation-failed", "mismatch")
			if problem["rule"] != string(ingest.RuleCAKeyMismatch) {
				t.Fatalf("problem %v", problem)
			}
			ce.draftUnchanged(t, etag)
			ce.scanned(t, offered, latestActVersion(t, ce.db, ce.cluster).Path)
			if ce.logged(ce.ca) {
				t.Error("the log holds the stored key")
			}
		})
	}
}

// The mutation control (persistence-api.md §16): with a comparison that is always true, another key
// is accepted.
func TestImportCAKeyComparisonControl(t *testing.T) {
	was := sameCAKey
	sameCAKey = func(provider.CAKey, string) bool { return true }
	t.Cleanup(func() { sameCAKey = was })
	ce := newCAImportEnv(t)
	op, _ := ce.importKey(t, b64(newTalosCA(t).Key))
	if r := readOp(t, ce.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
}

func TestImportNeedsAReadableStoredCAKey(t *testing.T) {
	for name, c := range map[string]struct {
		broken  func(t *testing.T, ce *caImportEnv)
		verdict string
		cause   string
		read    bool // the event names the version that was read
	}{
		"no first-key act": {func(t *testing.T, ce *caImportEnv) { dropFirstKeyActs(t, ce.db, ce.cluster) }, "no key", "absent", false},
		"the version soft-deleted": {func(t *testing.T, ce *caImportEnv) {
			v := latestActVersion(t, ce.db, ce.cluster)
			ce.keys.kv.destroy(v.Path, v.Version)
		}, "no key", "version-gone", false},
		"the provider sealed": {func(_ *testing.T, ce *caImportEnv) {
			ce.keys.kv.mu.Lock()
			ce.keys.kv.status[http.MethodGet] = http.StatusServiceUnavailable
			ce.keys.kv.mu.Unlock()
		}, "unreadable", "provider-unavailable", false},
		// The same path and version number, written at another time than the act records.
		"the version written again since the act": {func(t *testing.T, ce *caImportEnv) {
			path := latestActVersion(t, ce.db, ce.cluster).Path
			ce.keys.kv.mu.Lock()
			defer ce.keys.kv.mu.Unlock()
			vs := ce.keys.kv.versions[path]
			vs[len(vs)-1].created = vs[len(vs)-1].created.Add(time.Hour)
		}, "unreadable", "version-changed", true},
	} {
		t.Run(name, func(t *testing.T) {
			ce := newCAImportEnv(t)
			c.broken(t, ce)
			op, j := ce.importKey(t, ce.ca)
			ce.refusedImport(t, op, j.claim.ID, http.StatusServiceUnavailable, "talos-access-unavailable", c.verdict)
			if e := verdictEvent(t, ce, op); (e["path"] != nil) != c.read || (e["version"] != nil) != c.read || (e["createdTime"] != nil) != c.read {
				t.Fatalf("the verdict's version, wanted %v: %v", c.read, e)
			}
			if got := events(t, ce.db, op)[2]["cause"]; got != c.cause {
				t.Fatalf("cause %v, want %s", got, c.cause)
			}
		})
	}
}

func TestImportOfAWorkerOffersNoCAKey(t *testing.T) {
	ce := newCAImportEnv(t)
	op, j := ce.startJob(t, map[string]any{"document": "version: v1alpha1\nmachine:\n  type: worker\n  token: " + runToken + "\n"})
	ce.api.runIngest(t.Context(), j)
	if r := readOp(t, ce.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	if slices.Contains(eventTypes(events(t, ce.db, op)), "ca-key-match") || len(ce.keys.kv.readVersions()) != 0 {
		t.Fatal("a worker import asked the signer")
	}
}
