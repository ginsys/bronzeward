package api

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ginsys/bronzeward/internal/talos"
)

// The cause of a failed machine configuration read: a node that stopped answering gives the
// identity read's cause, and a refusal still gives machine-config. No database is needed.
func TestConfigReadCause(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"the connection was not accepted":    {status.Error(codes.Unavailable, "x"), "node-unreachable"},
		"the node did not answer in time":    {status.Error(codes.DeadlineExceeded, "x"), "node-unreachable"},
		"a deadline in a wrapped error":      {fmt.Errorf("talos: %w", context.DeadlineExceeded), "node-unreachable"},
		"a credential the node refuses":      {status.Error(codes.PermissionDenied, "x"), "machine-config"},
		"a resource the node lacks":          {status.Error(codes.NotFound, "x"), "machine-config"},
		"an internal error":                  {status.Error(codes.Internal, "x"), "machine-config"},
		"an answer the client cannot decode": {fmt.Errorf("read: %w", talos.ErrUndecodable), "machine-config-undecodable"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := configReadCause(c.err); got != c.want {
				t.Fatalf("cause %q, want %q", got, c.want)
			}
		})
	}
}
