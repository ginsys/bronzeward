package topf

import (
	"strings"
	"testing"

	"github.com/postfinance/topf/pkg/config"
)

func TestBronzewardRunningContractAndDesiredImage(t *testing.T) {
	n := &Node{
		t:    &topf{TopfConfig: &config.TopfConfig{TalosVersion: "v1.14.1"}},
		Node: &config.Node{TalosVersion: "v1.15.0"}, runningVersion: "v1.13.6",
	}
	if n.TalosVersion() != "1.13.6" || !strings.HasSuffix(n.InstallerImage(), ":v1.15.0") {
		t.Fatal("running contract and per-node desired image were not distinguished")
	}
	n.runningVersion = ""
	if n.TalosVersion() != "1.14.1" {
		t.Fatal("offline contract did not use the cluster setting")
	}
}
