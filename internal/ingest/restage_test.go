package ingest

import (
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"
)

// restagedStream is the full stream the machinery v1.14.5 generates for a control plane, parsed,
// with every leaf the machinery redacts turned into a !bwref node: what a second ingestion of an
// already extracted machine (a re-stage after a mark, a re-import) reads (ginsys/bronzeward#712).
func restagedStream(t *testing.T) (docs []*yaml.Node, leaves int) {
	t.Helper()
	in, err := generate.NewInput("bwsynth", "https://cp.example.test:6443", constants.DefaultKubernetesVersion, generate.WithInstallDisk("/dev/sda"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := in.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	docs, err = parseStream(b)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range docs {
		ps, err := schemaPointers(d, i, nil)
		if err != nil {
			t.Fatalf("doc[%d]: %v", i, err)
		}
		for _, sp := range ps {
			n, ok := resolve(docs, i, sp.pointer)
			if !ok {
				t.Fatalf("doc[%d] %v: no node", i, sp.pointer)
			}
			n.Tag, n.Value = refTag, "ref"
			leaves++
		}
	}
	return docs, leaves
}

// The etcd encryption secret is redacted by the machinery even when it loads as a null (a reference),
// so the stream above still names it. identify and knownSecrets both skip a node already tagged
// !bwref. The two call sites are one defect: the test runs both.
func TestReStagedStreamHoldsNoSecretToExtract(t *testing.T) {
	docs, leaves := restagedStream(t)
	if leaves < 8 {
		t.Fatalf("only %d leaves were made references; the full v1.14.5 stream holds more", leaves)
	}
	t.Run("identify", func(t *testing.T) {
		targets, err := identify(docs, nil)
		if err != nil {
			t.Fatalf("identify refused a stream whose secrets are all references: %v", err)
		}
		if len(targets) != 0 {
			t.Errorf("identify found %d targets in a stream of references", len(targets))
		}
	})
	t.Run("knownSecrets", func(t *testing.T) {
		if _, complete := knownSecrets(docs, nil); !complete {
			t.Error("knownSecrets reported the references as an incomplete reading")
		}
	})
}
