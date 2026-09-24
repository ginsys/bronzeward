package main

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestNormalizeReadBack(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a: 1\n\n", "a: 1\n"},
		{"a: 1\n", "a: 1\n"},
		{"a: 1", "a: 1\n"},
	} {
		if got := string(normalizeReadBack([]byte(c.in))); got != c.want {
			t.Errorf("normalizeReadBack(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyApply(t *testing.T) {
	exitErr := &exec.ExitError{}
	for _, c := range []struct {
		name   string
		err    error
		ctxErr error
		out    string
		want   string
	}{
		{"exit 0", nil, nil, "Applied configuration without a reboot\n", respAccepted},
		{"transport deadline", exitErr, context.DeadlineExceeded, "", respUnknown},
		{"validation", exitErr, nil, "error applying new configuration: rpc error: code = InvalidArgument desc = bad", respRejected},
		// The control plane's proxy answering for an unreachable worker is not the worker's word.
		{"proxy unavailable", exitErr, nil, "rpc error: code = Unavailable desc = connection error", respUnknown},
		{"other exit", exitErr, nil, "anything else", respUnknown},
		{"not started", errors.New("exec: not found"), nil, "", respUnknown},
	} {
		if got, _ := classifyApply(c.err, c.ctxErr, c.out); got != c.want {
			t.Errorf("%s: classifyApply = %q, want %q", c.name, got, c.want)
		}
	}
}
