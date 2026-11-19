package api

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// keyStore is the signer the observation tests run on: the real provider.Signer over a kvStandIn
// that holds one Talos CA, with each cluster's first-key act naming the version it was written as.
// A node that trusts the same CA (talostest.NewPKIFromCA) accepts the certificates signed from it.
type keyStore struct {
	kv *kvStandIn
	sg *provider.Signer
	ca *x509.PEMEncodedCertificateAndKey
}

func newKeyStore(t *testing.T, ca *x509.PEMEncodedCertificateAndKey) *keyStore {
	t.Helper()
	kv := &kvStandIn{versions: map[string][]kvVersion{}, status: map[string]int{}}
	return &keyStore{kv: kv, sg: newSignerOver(t, kv), ca: ca}
}

// clientKey is the client key a talosconfig document holds, as the document writes it (base64), for
// a scan that no record or log holds the key a certificate was signed with.
func clientKey(tc []byte) string {
	for _, l := range strings.Split(string(tc), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "key: "); ok {
			return v
		}
	}
	return ""
}

// sameVersion is whether two CA key version identities are the same path, version and created_time.
func sameVersion(a, b provider.CAKeyVersion) bool {
	return a.Path == b.Path && a.Version == b.Version && a.CreatedTime.Equal(b.CreatedTime)
}

// signedFrom is whether x names a certificate (a serial and an end) signed from CA key version v.
func (x *observedAccess) signedFrom(v provider.CAKeyVersion) bool {
	return x != nil && x.Path == v.Path && x.Version == v.Version && x.CreatedTime.Equal(v.CreatedTime) && x.Serial != "" &&
		x.NotAfter.After(time.Now())
}

// dropFirstKeyActs removes every first-key act of cluster, so that no act names a CA key version.
func dropFirstKeyActs(t *testing.T, db *sql.DB, cluster string) {
	t.Helper()
	mustExec(t, db, `ALTER TABLE first_key_act DISABLE TRIGGER immutable_rows`)
	mustExec(t, db, `DELETE FROM first_key_act WHERE cluster = $1`, cluster)
	mustExec(t, db, `ALTER TABLE first_key_act ENABLE TRIGGER immutable_rows`)
}

// latestActVersion is the CA key version the cluster's latest first-key act names.
func latestActVersion(t *testing.T, db *sql.DB, cluster string) provider.CAKeyVersion {
	t.Helper()
	var v provider.CAKeyVersion
	if err := db.QueryRow(`SELECT k.key_path, k.key_version, k.key_created_time FROM first_key_act k JOIN act a ON a.id = k.act
		WHERE k.cluster = $1 ORDER BY a.seq DESC LIMIT 1`, cluster).Scan(&v.Path, &v.Version, &v.CreatedTime); err != nil {
		t.Fatal(err)
	}
	return v
}

// recordedFrom is whether observation obs is stored with the access x names, a certificate signed from
// CA key version v: key path, version, created_time, serial and NotAfter, each as x holds it.
func recordedFrom(t *testing.T, db *sql.DB, obs string, x *observedAccess, v provider.CAKeyVersion) bool {
	t.Helper()
	if !x.signedFrom(v) {
		return false
	}
	return count(t, db, `SELECT count(*) FROM observation WHERE id = $1 AND access_path = $2 AND access_version = $3
		AND access_created::timestamptz = $4 AND access_serial = $5 AND access_not_after = $6`,
		obs, v.Path, v.Version, v.CreatedTime, x.Serial, x.NotAfter) == 1
}

// seed writes the next version of cluster's CA key and records the first-key act that names it, as
// the first-key route does, and answers the version it wrote.
func (k *keyStore) seed(t *testing.T, db *sql.DB, cluster string) provider.CAKeyVersion {
	t.Helper()
	return k.seedCreated(t, db, cluster, time.Time{})
}

// seedCreated is seed for a version the store holds with created_time created (the store's own
// clock when zero); the act records it to the microsecond, as its column does.
func (k *keyStore) seedCreated(t *testing.T, db *sql.DB, cluster string, created time.Time) provider.CAKeyVersion {
	t.Helper()
	path := "access/talos-ca/" + cluster
	k.kv.put(path, string(k.ca.Crt), string(k.ca.Key))
	if !created.IsZero() {
		k.kv.mu.Lock()
		k.kv.versions[path][len(k.kv.versions[path])-1].created = created
		k.kv.mu.Unlock()
	}
	n, v := k.kv.latest(path)
	act := id.New(id.Act)
	mustExec(t, db, `INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
		SELECT $1, p.id, p.kind, 'tool', 'test', '{}', i.epoch, now() FROM principal p, installation_state i ORDER BY p.id LIMIT 1`, act)
	mustExec(t, db, `INSERT INTO first_key_act (act, cluster, kind, replaced, ca_fingerprint, key_path, key_version, key_created_time)
		VALUES ($1, $2, 'machine-config', $3, repeat('a', 64), $4, $5, $6)`, act, cluster, n > 1, path, n, v.created)
	return provider.CAKeyVersion{Path: path, Version: n, CreatedTime: v.created}
}
