package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failSaveAt makes keeping what the answers taught the context fail after the write to path is answered:
// the answer shows an epoch the context does not hold, and the contexts file cannot be read back.
func failSaveAt(t *testing.T, path string) func(string, http.ResponseWriter) {
	t.Helper()
	return func(p string, w http.ResponseWriter) {
		if p != path {
			return
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward", "config.yaml"), []byte("{"), 0o600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Bronzeward-Epoch", "ep_OB-2")
	}
}

// TestWriteSaveFailsAfterAck: a write the server acknowledged stays a write that was done when keeping
// the context fails after it. The result is printed, the failure is reported and the exit is 1, and the
// report says the write is stored and advises no repeat of it.
func TestWriteSaveFailsAfterAck(t *testing.T) {
	add := []string{"cluster", "add", "--name", "lab", "--endpoint", "https://e", "--contract", "v1.14", "--talos-cluster-id", "talos-cluster-1"}
	repeat := append(append([]string{}, add...), "--idempotency-key=BWSYNTH-aaaaaaaaaaaaaaa", "--expect-epoch=ep_OB-1", "--expect-principal=h_OB")
	for _, c := range []struct {
		name string
		args []string
		out  string
	}{
		{"a one-shot write", add, "recorded"},
		{"a write under -o json", append([]string{"-o", "json"}, add...), `"talosClusterId"`},
		{"a write with a repeat key", repeat, "recorded"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newOnboard(t)
			e.api.onPost = failSaveAt(t, "/clusters")
			code, out, errOut := e.runArgs(c.args...)
			if code != 1 || len(e.api.clusters) != 1 || !strings.Contains(out, c.out) || !strings.Contains(errOut, "keeping the epoch") || !strings.Contains(errOut, "the write is stored") {
				t.Errorf("exit %d, %d clusters, stdout %q, stderr %q; want 1, 1, %q shown, the save failure and \"the write is stored\"", code, len(e.api.clusters), out, errOut, c.out)
			}
			if strings.Contains(out+errOut, "--idempotency-key ") {
				t.Errorf("the report advises a repeat of the write:\n%s%s", out, errOut)
			}
		})
	}
}

// TestWriteSaveFailsWithFailedWrite: a write the server refused keeps its own error and exit when saving
// fails as well, and never says it is stored.
func TestWriteSaveFailsWithFailedWrite(t *testing.T) {
	e := newOnboard(t)
	e.api.failPost = map[string]int{"/clusters": 1}
	e.api.onPost = failSaveAt(t, "/clusters")
	code, out, errOut := e.runArgs("cluster", "add", "--name", "lab", "--endpoint", "https://e", "--contract", "v1.14", "--talos-cluster-id", "talos-cluster-1")
	if code != 1 || out != "" || len(e.api.clusters) != 0 || !strings.Contains(errOut, "validation-failed") || !strings.Contains(errOut, "keeping the epoch") || strings.Contains(errOut, "the write is stored") {
		t.Errorf("exit %d, %d clusters, stdout %q, stderr %q; want 1, none, the refusal and the save failure, and no claim that the write is stored", code, len(e.api.clusters), out, errOut)
	}
}

// TestOnboardClusterSaveFails: a cluster the server recorded stays created in the report when keeping the
// context fails afterwards; the run ends with the error and its rerun finds the record again.
func TestOnboardClusterSaveFails(t *testing.T) {
	e := newOnboard(t)
	e.api.onPost = failSaveAt(t, "/clusters")
	code, out, errOut := e.run("-o", "json")
	var obj onboarding
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("stdout is not the object: %v\n%s\nstderr %q", err, out, errOut)
	}
	if code != 1 || len(e.api.clusters) != 1 || obj.Cluster.State != "created" || obj.Cluster.ID == "" || obj.Cluster.Command == "" || !strings.Contains(errOut, "keeping the epoch") {
		t.Errorf("exit %d, %d clusters, cluster %+v, stderr %q; want 1, 1, created with its ID and a rerun, the save failure reported", code, len(e.api.clusters), obj.Cluster, errOut)
	}
}

// TestImportStartSaveFailsStillFollows: an import the server accepted is followed to its end when keeping
// the context failed after the start, and the save failure is reported once: the follow keeps the context
// again, which is the retry, and says if that fails too.
func TestImportStartSaveFailsStillFollows(t *testing.T) {
	e := newOnboard(t)
	e.api.machines = []obMachine{{id: "mch_OB", cluster: "cl_OB", key: "node-1", platform: "metal", endpoint: "10.0.0.1:50000"}}
	e.api.draft.id, e.api.draft.cluster, e.api.draft.title, e.api.draft.state, e.api.draft.rev = "drf_OB", "cl_OB", "onboard lab", "open", 1
	e.api.onPost = failSaveAt(t, "/ingestions")
	code, out, errOut := e.runArgs("import", "start", "--machine", "mch_OB", "--draft", "drf_OB", "--if-match", `"1-OB"`)
	if code != 1 || len(e.api.ings) != 1 || !strings.Contains(out, "started") || !strings.Contains(out, "released into draft") || !strings.Contains(errOut, "  succeeded") || strings.Count(errOut, "keeping the epoch") != 1 {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 1, 1, the import started and followed to its release, and the save failure once", code, len(e.api.ings), out, errOut)
	}
}
