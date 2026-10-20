package nodepool

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/postfinance/topf/internal/topf"
)

func TestBronzewardSerialFailureStopsAdmission(t *testing.T) {
	want := errors.New("synthetic refusal")
	calls := 0
	err := RunConcurrent(t.Context(), nodes(3), 1, func(context.Context, *topf.Node, *slog.Logger) error {
		calls++
		return want
	}, slog.New(slog.DiscardHandler))
	if calls != 1 || !errors.Is(err, want) {
		t.Fatalf("serial admission after failure: calls=%d error=%v", calls, err)
	}
	// No remote request, concurrent cancellation or restart recovery is tested.
}
