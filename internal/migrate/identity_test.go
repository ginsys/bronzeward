package migrate

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The identity keys (persistence-api.md §7.3; execution and recovery choice §10.26).
const (
	insertNodeMachine = `INSERT INTO machine (id, cluster, talos_node_id, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, 'normal', '10.55.0.4:50000', now())`
	insertKeyedMachine = `INSERT INTO machine (id, cluster, smbios_uuid, talos_node_id, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, $4, 'normal', '10.55.0.5:50000', now())`
	insertClusterID = `INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
		VALUES ($1, 'x', 'https://x.test', 'v1.13', $2, now())`
	insertClusterNoID = `INSERT INTO cluster (id, name, endpoint, contract, created_at) VALUES ($1, 'x', 'https://x.test', 'v1.13', now())`
	nodeID            = "7x1SuC8Ege5BGXdAfTEff5iQnlWZLfv9h1LGMxA2pYkC"
	clusterID         = "8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0="
)

func TestMachineIdentityConstraints(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	mustExec(t, db, insertNodeMachine, id.New(id.Machine), a.cluster, nodeID)
	mustExec(t, db, insertClusterID, id.New(id.Cluster), clusterID)
	// SMBIOS's nil and all-ones values are accepted as a UUID (§7.3), each once.
	mustExec(t, db, insertMachine, id.New(id.Machine), a.cluster, "00000000-0000-0000-0000-000000000000", nil, "normal")
	mustExec(t, db, insertMachine, id.New(id.Machine), a.cluster, "ffffffff-ffff-ffff-ffff-ffffffffffff", nil, "normal")
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"both keys", insertKeyedMachine, []any{id.New(id.Machine), a.cluster, "3e8d9f4b-5c6a-4b8c-9d2e-3f4a5b6c7d8e", "otherNode"}, "23514"},
		{"neither key", insertKeyedMachine, []any{id.New(id.Machine), a.cluster, nil, nil}, "23514"},
		{"second machine with one node ID", insertNodeMachine, []any{id.New(id.Machine), a.other, nodeID}, "23505"},
		{"node ID with a space", insertNodeMachine, []any{id.New(id.Machine), a.cluster, "node one"}, "23514"},
		{"empty node ID", insertNodeMachine, []any{id.New(id.Machine), a.cluster, ""}, "23514"},
		{"second nil UUID", insertMachine, []any{id.New(id.Machine), a.other, "00000000-0000-0000-0000-000000000000", nil, "normal"}, "23505"},
		{"cluster without a Talos cluster ID", insertClusterNoID, []any{id.New(id.Cluster)}, "23502"},
		{"second cluster with one Talos cluster ID", insertClusterID, []any{id.New(id.Cluster), clusterID}, "23505"},
		{"Talos cluster ID of 31 bytes", insertClusterID, []any{id.New(id.Cluster), "8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSA=="}, "23514"},
		{"Talos cluster ID unpadded", insertClusterID, []any{id.New(id.Cluster), "8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0"}, "23514"},
		{"Talos cluster ID in the standard alphabet", insertClusterID, []any{id.New(id.Cluster), "8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuyzCfGIRVSL0="}, "23514"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
				t.Fatalf("%v; want SQLSTATE %s", err, c.want)
			}
		})
	}
}

// Each key's unique index is what refuses its duplicate: without it the same insert commits.
func TestIdentityUniqueControls(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	mustExec(t, db, insertNodeMachine, id.New(id.Machine), a.cluster, nodeID)
	mustExec(t, db, insertClusterID, id.New(id.Cluster), clusterID)
	for _, c := range []struct {
		index, q string
		args     func() []any
	}{
		{"machine_talos_node_id", insertNodeMachine, func() []any { return []any{id.New(id.Machine), a.other, nodeID} }},
		{"cluster_talos_cluster_id", insertClusterID, func() []any { return []any{id.New(id.Cluster), clusterID} }},
	} {
		if _, err := db.Exec(c.q, c.args()...); sqlState(err) != "23505" {
			t.Fatalf("%s: duplicate with the index: %v; want 23505", c.index, err)
		}
		mustExec(t, db, "DROP INDEX "+c.index)
		if _, err := db.Exec(c.q, c.args()...); err != nil {
			t.Fatalf("%s: duplicate without the index: %v; want it to commit", c.index, err)
		}
	}
}
