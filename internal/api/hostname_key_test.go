package api

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// The legacy machine.network.hostname key at import and at drift adoption (compilation.md §2.3 step 2a,
// persistence-api §4.2). The names are synthetic.

const hostnameKeyDoc = "machine:\n  token: " + runToken + "\n  network:\n    hostname: node-a\n"

// Step 2a releases the key as it releases a document: the stored base holds no hostname, the parent mapping
// stays, and the record names the machine and says the removed thing was not the fill's own.
func TestHostnameKeyImportRemovesIt(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-a")
	op, j := ie.startJob(t, map[string]any{"document": hostnameKeyDoc})
	ie.runWith(t, options{}, j)
	if r := readOp(t, ie.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	doc, name, equal := ie.filledRecord(t)
	if strings.Contains(doc, "hostname") || !strings.Contains(doc, "network:") {
		t.Errorf("the stored base: %q", doc)
	}
	if name.String != "node-a" || !equal.Valid || equal.Bool {
		t.Errorf("record %+v, %+v: want node-a, not the fill's own", name, equal)
	}
}

// The previous-name case: a key that differs from the machine's name now is an explicit hostname, kept, with
// nothing recorded, so the release says `configuration` and the rename does not reach the node.
func TestHostnameKeyImportKeepsAPreviousName(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-b")
	_, j := ie.startJob(t, map[string]any{"document": hostnameKeyDoc})
	ie.runWith(t, options{}, j)
	doc, name, equal := ie.filledRecord(t)
	if !strings.Contains(doc, "hostname: node-a") || name.Valid || equal.Valid {
		t.Errorf("document %q, record %+v, %+v", doc, name, equal)
	}
}

// The baseline stays the exact input: the configuration digest the adoption comparison reads is the same
// whether or not the machine has a name that removes the key.
func TestHostnameKeyBaselineStaysTheExactInput(t *testing.T) {
	digest := func(name string) (string, []byte) {
		ie := newIngestEnv(t, options{})
		if name != "" {
			ie.nameMachine(t, name)
		}
		_, j := ie.startJob(t, map[string]any{"document": hostnameKeyDoc})
		ie.runWith(t, options{}, j)
		var doc string
		var conf []byte
		if err := ie.db.QueryRow(`SELECT document, configuration_digest FROM import_base_revision`).Scan(&doc, &conf); err != nil {
			t.Fatal(err)
		}
		return doc, conf
	}
	named, confNamed := digest("node-a")
	unnamed, confUnnamed := digest("")
	if strings.Contains(named, "node-a") || !strings.Contains(unnamed, "node-a") {
		t.Fatalf("control: named %q, unnamed %q", named, unnamed)
	}
	if string(confNamed) != string(confUnnamed) {
		t.Error("the configuration digest follows the removal")
	}
}

// A drift adoption runs step 2a on the node's own configuration, under the same handler as an import.
func TestHostnameKeyDriftAdoptionRemovesIt(t *testing.T) {
	me := newMachineEnv(t)
	me.nameMachine(t, "node-a")
	me.node.SetMachineConfig(t, []byte("version: v1alpha1\n"+hostnameKeyDoc+"cluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443\n"))
	openDriftOn(t, me.db, me.machine)
	op, j := me.startJob(t, driftAdoption)
	me.runWith(t, options{}, j)
	if r := readOp(t, me.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	doc, name, equal := me.filledRecord(t)
	if strings.Contains(doc, "hostname") || name.String != "node-a" || !equal.Valid || equal.Bool {
		t.Errorf("document %q, record %+v, %+v", doc, name, equal)
	}
}

// The consequence: a base stored without its key, recorded as not the fill's own, compiles a release whose
// hostname is the machine's name, and a rename reaches the next release (compilation.md §6.1 item 3).
func TestHostnameKeyRenameReachesTheRelease(t *testing.T) {
	for _, name := range []string{"web-1", "web-2"} {
		t.Run(name, func(t *testing.T) {
			b := newBuildEnv(t)
			b.emptyAssignment()
			mustExec(t, b.db, `UPDATE machine SET name = $1 WHERE id = $2`, name, b.machine)
			b.ibr = b.importBaseFilled(b.machine, withoutHostnameConfig(t, generatedConfig(t, constants.DefaultKubernetesVersion)), "web-1", false)
			rel, _ := b.published()
			var source, got string
			if err := b.db.QueryRow(`SELECT hostname_source, hostname_name FROM release_machine WHERE release = $1 AND machine = $2`,
				rel, b.machine).Scan(&source, &got); err != nil {
				t.Fatal(err)
			}
			if source != "machine-name" || got != name {
				t.Errorf("stored %q %q, want machine-name %q", source, got, name)
			}
			if b.releaseFormOnly() {
				t.Error("a released key reads form-only at the first apply")
			}
		})
	}
}
