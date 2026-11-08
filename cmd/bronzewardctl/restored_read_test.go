package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestRestoredRead is the check of publish and the import watch on the read the watch returns
// (ginsys/bronzeward#728). The watch repairs the headerless case itself, so this case holds the
// command's own guard: a terminal read that the client's count of epoch moves has passed gives the
// unknown-state error and no state to print. A client that moved none holds a read taken at count 0.
func TestRestoredRead(t *testing.T) {
	c := new(client.Client)
	done := &apitypes.OperationBody{State: "succeeded"}
	running := &apitypes.OperationBody{State: "running"}
	holds := client.OperationRead{Moves: 0}
	passed := client.OperationRead{Moves: -1} // not the client's count: a move came after this read
	for _, tc := range []struct {
		name string
		o    *apitypes.OperationBody
		read client.OperationRead
		fail bool
	}{
		{"terminal read that stands", done, holds, false},
		{"terminal read overtaken by a move", done, passed, true},
		{"operation still running", running, passed, false},
		{"no operation", nil, passed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := restoredRead(c, "op_BWSYNTH", tc.o, tc.read)
			switch {
			case !tc.fail && err != nil:
				t.Fatalf("restoredRead = %v; want nil", err)
			case tc.fail && err == nil:
				t.Fatal("restoredRead = nil; want the unknown-state error")
			case tc.fail:
				var unknown outcomeUnknown
				if !errors.As(err, &unknown) || !strings.Contains(err.Error(), "restored") || !strings.Contains(err.Error(), "op_BWSYNTH") {
					t.Fatalf("restoredRead = %v; want an outcomeUnknown that says the installation was restored and names the operation", err)
				}
			}
		})
	}
}
