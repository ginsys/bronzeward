package talos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The synthetic configuration and node text a response must never carry: every check below that a
// recorded response is free of them has a control that they were sent.
const (
	applyConfig = "version: v1alpha1\nmachine:\n  type: worker\n  marker: apply-secret-7Qx\n"
	nodeSecret  = "node-secret-4Zd"
)

// validationText is the shape E4 recorded for a validation refusal (row 022): the node's own
// multi-error, naming the document type, quoting the offending value.
const validationText = "1 error occurred:\n\t* v1alpha1.Config: 1 error occurred:\n\t* unknown machine type \"" + nodeSecret + "\"\n\n\n\n"

// applierAt dials an Applier at a fresh stand-in node and returns it with a ctx that has a
// deadline.
func applierAt(t *testing.T, timeout time.Duration) (Applier, *talostest.Node, context.Context) {
	t.Helper()
	p := talostest.NewPKI(t)
	n := talostest.Serve(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	a, err := DialApplier(ctx, p.Talosconfig("", "", ""), n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, n, ctx
}

func accepted(mode machine.ApplyConfigurationRequest_Mode) func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
	return func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
		return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{{
			Metadata: &common.Metadata{Hostname: "w"}, Mode: mode,
			Warnings: []string{"warning quoting " + nodeSecret}, ModeDetails: "details quoting " + nodeSecret,
		}}}, nil
	}
}

// One apply sends exactly the configuration, in no-reboot mode, as a real request (no dry run, no
// try-mode timeout) and without routing metadata, and an acceptance records the class and a fixed
// notice, nothing the node said (execution-recovery.md §3.5, §5.1).
func TestApplyNoReboot(t *testing.T) {
	a, n, ctx := applierAt(t, 10*time.Second)
	n.OnApply(accepted(machine.ApplyConfigurationRequest_NO_REBOOT))
	routed := metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2", "nodes", "192.0.2.3")
	r, err := a.ApplyNoReboot(routed, []byte(applyConfig))
	if err != nil {
		t.Fatal(err)
	}
	if r.Class != ClassAccepted || r.Code != codes.OK || !r.Accounts() {
		t.Fatalf("response %+v", r)
	}
	if strings.Contains(r.Text, nodeSecret) || strings.Contains(fmt.Sprintf("%+v %#v", r, r), nodeSecret) {
		t.Fatalf("the node's text is in the response: %+v", r)
	}
	got := n.AppliedRequests()
	if len(got) != 1 || string(got[0].Data) != applyConfig || got[0].Mode != "NO_REBOOT" || got[0].DryRun || got[0].TryModeTimeout {
		t.Fatalf("the node received %+v", got)
	}
	seen := n.Seen()
	if len(seen) != 1 || hasRouting(seen[0]) {
		t.Fatalf("routing metadata reached the node: %v", seen)
	}
}

// Classes: only a validation-class InvalidArgument is a rejection; every other code the node
// returned accounts for its attempt without deciding it; a transport outcome accounts for nothing.
// Each runs through the real client, in both shapes the node answers an error in: a gRPC status, and
// an OK response whose message carries the error and status (apid's, which the machinery turns into
// a node error). The shapes differ in provenance: a code the client produces itself (clientCodes)
// shows no answer as a status error, and is an answer as a message's.
func TestApplyClasses(t *testing.T) {
	immediate := "1 error occurred:\n\t* v1alpha1.Config: this config change can't be applied in immediate mode " + nodeSecret
	// asStatus is the class when the error is the call's status, asMessage when it is a message's.
	type outcome struct {
		class    Class
		accounts bool
	}
	answered := outcome{ClassCode, true}
	cases := []struct {
		name                string
		code                codes.Code
		msg                 string
		asStatus, asMessage outcome
	}{
		{"validation", codes.InvalidArgument, validationText, outcome{ClassRejected, true}, outcome{ClassRejected, true}},
		{"immediate mode refusal", codes.InvalidArgument, immediate, outcome{ClassInvalidArgument, true}, outcome{ClassInvalidArgument, true}},
		{"unrecognised invalid argument", codes.InvalidArgument, "something " + nodeSecret, outcome{ClassInvalidArgument, true}, outcome{ClassInvalidArgument, true}},
		{"validation prefix elsewhere in the text", codes.InvalidArgument, "x " + validationText, outcome{ClassInvalidArgument, true}, outcome{ClassInvalidArgument, true}},
		{"failed precondition", codes.FailedPrecondition, nodeSecret, answered, answered},
		{"permission denied", codes.PermissionDenied, nodeSecret, answered, answered},
		{"internal", codes.Internal, nodeSecret, outcome{ClassTransport, false}, answered},
		{"resource exhausted", codes.ResourceExhausted, nodeSecret, outcome{ClassTransport, false}, answered},
		{"unknown", codes.Unknown, nodeSecret, outcome{ClassTransport, false}, answered},
		{"unavailable", codes.Unavailable, nodeSecret, outcome{ClassTransport, false}, answered},
		{"deadline exceeded", codes.DeadlineExceeded, nodeSecret, outcome{ClassTransport, false}, answered},
		{"canceled", codes.Canceled, nodeSecret, outcome{ClassTransport, false}, answered},
	}
	for _, c := range cases {
		for _, shape := range []string{"status", "message"} {
			t.Run(c.name+"/"+shape, func(t *testing.T) {
				want := c.asStatus
				if shape == "message" {
					want = c.asMessage
				}
				a, n, ctx := applierAt(t, 10*time.Second)
				n.OnApply(func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
					if shape == "status" {
						return nil, status.Error(c.code, c.msg)
					}
					return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{{
						Metadata: &common.Metadata{Hostname: "w", Error: c.msg, Status: status.New(c.code, c.msg).Proto()},
					}}}, nil
				})
				r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
				if err != nil {
					t.Fatalf("a node outcome is a response, not an error: %v", err)
				}
				if r.Class != want.class || r.Code != c.code || r.Accounts() != want.accounts {
					t.Fatalf("%+v; want class %s code %s accounts %v", r, want.class, c.code, want.accounts)
				}
				if s := fmt.Sprintf("%+v %#v %s", r, r, r.Text); strings.Contains(s, nodeSecret) || strings.Contains(s, "unknown machine type") {
					t.Fatalf("the node's text is in the response: %s", s)
				}
			})
		}
	}
}

// An answer that arrived is not taken back by a context that ended afterwards, and a context that
// ended before any answer is a transport outcome whatever status the client then reported. The
// classification is called directly: the race itself is not reproducible through a live call.
func TestApplyClassifiesTheAnswerBeforeTheContext(t *testing.T) {
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	validation := &client.NodeError{Node: "w", Err: status.Error(codes.InvalidArgument, validationText)}
	if r := classifyApply(ended, nil, validation); r.Class != ClassRejected || !r.Accounts() {
		t.Errorf("a validation answer, then cancellation: %+v", r)
	}
	if r := classifyApply(ended, nil, status.Error(codes.Canceled, "context canceled")); r.Class != ClassTransport || r.Accounts() || r.Code != codes.Canceled {
		t.Errorf("cancellation with no answer: %+v", r)
	}
	// The same answer as the call's own status, plain and wrapped, with the context ended.
	plainValidation := status.Error(codes.InvalidArgument, validationText)
	for name, err := range map[string]error{"plain": plainValidation, "wrapped": fmt.Errorf("apply: %w", plainValidation)} {
		if r := classifyApply(ended, nil, err); r.Class != ClassRejected || !r.Accounts() {
			t.Errorf("a %s validation status, then cancellation: %+v", name, r)
		}
	}
	// Control: the same validation answer, the context live.
	if r := classifyApply(t.Context(), nil, validation); r.Class != ClassRejected {
		t.Errorf("control: %+v", r)
	}
}

// More than one answer is not one node's answer, in any order or mix: the machinery folds several
// error messages into one multierror, and a first validation refusal must not stand for them all.
func TestApplyOneAnswerOnly(t *testing.T) {
	fail := func(code codes.Code, msg string) *machine.ApplyConfiguration {
		return &machine.ApplyConfiguration{Metadata: &common.Metadata{Hostname: "w", Error: msg, Status: status.New(code, msg).Proto()}}
	}
	ok := &machine.ApplyConfiguration{Metadata: &common.Metadata{Hostname: "w"}, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}
	cases := map[string][]*machine.ApplyConfiguration{
		"validation then unavailable": {fail(codes.InvalidArgument, validationText), fail(codes.Unavailable, nodeSecret)},
		"unavailable then validation": {fail(codes.Unavailable, nodeSecret), fail(codes.InvalidArgument, validationText)},
		"two validations":             {fail(codes.InvalidArgument, validationText), fail(codes.InvalidArgument, validationText)},
		"validation beside an accept": {fail(codes.InvalidArgument, validationText), ok},
		"an accept beside a failure":  {ok, fail(codes.Internal, nodeSecret)},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			a, n, ctx := applierAt(t, 10*time.Second)
			n.OnApply(func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
				return &machine.ApplyConfigurationResponse{Messages: msgs}, nil
			})
			r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
			if err != nil {
				t.Fatal(err)
			}
			if r.Class != ClassUnrecognised || r.Accounts() {
				t.Fatalf("%+v; want %s, accounting for nothing", r, ClassUnrecognised)
			}
		})
	}
}

// A response over the client's receive limit is a status the client produces itself
// (ResourceExhausted): the node may have applied the configuration, so it accounts for nothing and is
// not a node's error answer.
func TestApplyOversizedResponse(t *testing.T) {
	a, n, ctx := applierAt(t, 30*time.Second)
	n.OnApply(func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
		return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{{
			Metadata: &common.Metadata{Hostname: "w"}, Mode: machine.ApplyConfigurationRequest_NO_REBOOT,
			ModeDetails: strings.Repeat("x", 33<<20),
		}}}, nil
	})
	r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
	if err != nil {
		t.Fatal(err)
	}
	if r.Class != ClassTransport || r.Code != codes.ResourceExhausted || r.Accounts() {
		t.Fatalf("%+v; want a transport outcome with ResourceExhausted", r)
	}
}

// An answer that is not recognisable as an acceptance is not one: no message, more than one, the
// message carrying an error without a status, or a mode other than the requested one. It accounts
// for nothing, so the attempt waits for an observation or a decision (§5.1).
func TestApplyUnrecognisedAnswers(t *testing.T) {
	msg := func(m *machine.ApplyConfiguration) *machine.ApplyConfigurationResponse {
		return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{m}}
	}
	cases := map[string]*machine.ApplyConfigurationResponse{
		"no message":        {},
		"two messages":      {Messages: []*machine.ApplyConfiguration{{Mode: machine.ApplyConfigurationRequest_NO_REBOOT}, {Mode: machine.ApplyConfigurationRequest_NO_REBOOT}}},
		"another mode":      msg(&machine.ApplyConfiguration{Mode: machine.ApplyConfigurationRequest_REBOOT}),
		"staged":            msg(&machine.ApplyConfiguration{Mode: machine.ApplyConfigurationRequest_STAGED}),
		"auto":              msg(&machine.ApplyConfiguration{Mode: machine.ApplyConfigurationRequest_AUTO}),
		"mode left default": msg(&machine.ApplyConfiguration{}),
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			a, n, ctx := applierAt(t, 10*time.Second)
			n.OnApply(func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
				return resp, nil
			})
			r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
			if err != nil {
				t.Fatal(err)
			}
			if r.Class != ClassUnrecognised || r.Accounts() {
				t.Fatalf("%+v; want %s, accounting for nothing", r, ClassUnrecognised)
			}
		})
	}
}

// A request that does not complete by the transport deadline is a transport outcome: the node may or
// may not have received it, so the response accounts for nothing (§5.1).
func TestApplyTransportDeadline(t *testing.T) {
	a, n, ctx := applierAt(t, 300*time.Millisecond)
	n.OnApply(func(ctx context.Context, _ *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
	if err != nil {
		t.Fatal(err)
	}
	if r.Class != ClassTransport || r.Accounts() {
		t.Fatalf("%+v; want a transport outcome", r)
	}
	if got := n.AppliedRequests(); len(got) != 1 {
		t.Fatalf("the request reached the node %d times; want once", len(got))
	}
}

// A node that is not there is a transport outcome, not an error.
func TestApplyUnreachable(t *testing.T) {
	p := talostest.NewPKI(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, err := DialApplier(ctx, p.Talosconfig("", "", ""), "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
	if err != nil {
		t.Fatal(err)
	}
	if r.Class != ClassTransport || r.Accounts() {
		t.Fatalf("%+v; want a transport outcome", r)
	}
}

// A send with nothing to send, or with no transport deadline, is refused before any request: an
// unbounded send cannot be accounted (§3.3) and an empty one is not a dispatch.
func TestApplyRefusesBeforeSending(t *testing.T) {
	a, n, ctx := applierAt(t, 10*time.Second)
	n.OnApply(accepted(machine.ApplyConfigurationRequest_NO_REBOOT))
	if _, err := a.ApplyNoReboot(ctx, nil); err == nil {
		t.Error("an empty configuration was sent")
	}
	if _, err := a.ApplyNoReboot(context.WithoutCancel(ctx), []byte(applyConfig)); !errors.Is(err, ErrNoDeadline) {
		t.Errorf("a send without a deadline: %v, want %v", err, ErrNoDeadline)
	}
	if got := n.AppliedRequests(); len(got) != 0 {
		t.Fatalf("%d requests reached the node", len(got))
	}
	// Control: the same applier sends once a deadline and a configuration are there.
	if _, err := a.ApplyNoReboot(ctx, []byte(applyConfig)); err != nil || len(n.AppliedRequests()) != 1 {
		t.Fatalf("control: %v after %d requests", err, len(n.AppliedRequests()))
	}
}

// DialApplier refuses what Dial refuses, without quoting the talosconfig.
func TestDialApplierRefuses(t *testing.T) {
	ctx := t.Context()
	if _, err := DialApplier(ctx, []byte("not a talosconfig: ["+applyConfig), "127.0.0.1:50000"); err == nil || strings.Contains(err.Error(), "apply-secret") {
		t.Errorf("a bad talosconfig: %v", err)
	}
	if _, err := DialApplier(ctx, talostest.NewPKI(t).Talosconfig("", "", ""), "not an endpoint"); err == nil {
		t.Error("a bad endpoint was accepted")
	}
}
