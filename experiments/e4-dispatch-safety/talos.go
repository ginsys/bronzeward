package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Talos runs the pinned talosctl against one node. Requests go through Endpoint (the fixture's
// control plane, which proxies them to the worker) unless Endpoint is the node itself.
type Talos struct {
	Bin      string
	Endpoint string
	Node     string
}

// readLimit bounds a read-back, as the fixture's own worker reads are bounded.
const readLimit = 10 * time.Second

func (t Talos) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, t.Bin, append([]string{"-e", t.Endpoint, "-n", t.Node}, args...)...)
	// CommandContext kills with SIGKILL when the context ends; WaitDelay stops a pipe held open by
	// anything the process left behind from hanging the wait.
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// normalizeReadBack is the fixture's normalization (fixtures/bin/selftest, worker_digest): the
// shell's command substitution strips every trailing newline and printf adds one back. The
// `-o jsonpath` output carries one newline more than the configuration that was sent.
func normalizeReadBack(b []byte) []byte {
	return append(bytes.TrimRight(b, "\n"), '\n')
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ReadDigest is the SHA-256 of the node's current machine configuration, normalized as above.
func (t Talos) ReadDigest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readLimit)
	defer cancel()
	var stderr bytes.Buffer
	cmd := t.command(ctx, "get", "machineconfig", "v1alpha1", "-o", "jsonpath={.spec}")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read-back: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", errors.New("read-back: empty configuration")
	}
	return digestOf(normalizeReadBack(out)), nil
}

// Response is what the executor learned from one send.
type Response struct {
	Class   string // respAccepted | respRejected | respUnknown
	Detail  string
	Elapsed time.Duration
}

const (
	respAccepted = "accepted"
	respRejected = "rejected"
	respUnknown  = "unknown"
)

// Apply sends the file with `apply-config --mode=no-reboot`, killing talosctl at the deadline.
func (t Talos) Apply(ctx context.Context, file string, deadline time.Time) Response {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	began := time.Now()
	out, err := t.command(ctx, "apply-config", "--mode=no-reboot", "--file", file).CombinedOutput()
	class, detail := classifyApply(err, ctx.Err(), string(out))
	return Response{Class: class, Detail: detail, Elapsed: time.Since(began)}
}

// classifyApply turns one send into a response class. Only two outcomes are definitive:
//   - accepted: talosctl exited 0 and printed its acceptance line;
//   - rejected: the node answered InvalidArgument, the configuration validation failure that the
//     worker logs before persisting anything (the rejection rows check that nothing changed).
//
// Everything else is unknown, including the control plane's own error for an unreachable worker:
// that is the proxy's word, not the worker's, and says nothing about whether the request arrives.
func classifyApply(err, ctxErr error, out string) (string, string) {
	oneLine := strings.Join(strings.Fields(out), " ")
	switch {
	case ctxErr != nil:
		return respUnknown, "transport deadline reached, talosctl killed: " + oneLine
	case err == nil && strings.Contains(out, "Applied configuration"):
		return respAccepted, oneLine
	case err == nil:
		return respUnknown, "exit 0 without the acceptance line: " + oneLine
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && strings.Contains(out, "code = InvalidArgument") {
		return respRejected, oneLine
	}
	return respUnknown, fmt.Sprintf("%v: %s", err, oneLine)
}
