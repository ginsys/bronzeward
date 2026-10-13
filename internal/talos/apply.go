package talos

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This file is the one place the package sends a mutating request: a no-reboot apply of one machine
// configuration to one node (execution-recovery.md §3.5). It is kept apart from Reader on purpose.
// guard_test.go holds this file, and no other, to its own allowlist (the one call, one request
// shape, two client members), and holds its callers to the dispatch sender in internal/api.

// Class is how a node answered one apply, in the words of execution-recovery.md §4.1: an
// acceptance, a gRPC code the node returned, or a transport outcome.
type Class string

const (
	// ClassAccepted is a response from the node accepting the configuration in the requested mode.
	ClassAccepted Class = "accepted"
	// ClassRejected is InvalidArgument of the validation-error class, the one class E4 showed
	// precedes any mutation (§5.1). Recognised positively; anything else is ClassInvalidArgument.
	ClassRejected Class = "rejected"
	// ClassInvalidArgument is any other InvalidArgument, such as the immediate-mode refusal. It
	// accounts for its attempt and leaves the outcome to a completion observation (§5.1).
	ClassInvalidArgument Class = "invalid-argument"
	// ClassCode is any other gRPC code the node returned.
	ClassCode Class = "grpc-code"
	// ClassTransport is a dial failure, a timeout, a reset connection, a cancellation, or a status
	// the client itself produces (a response over its limit, one it could not decode): the node may
	// or may not have received the request. It accounts for nothing (§5.1).
	ClassTransport Class = "transport"
	// ClassUnrecognised is an answer without an error that is not a recognisable acceptance: no
	// message, several, or another mode than the requested one. It accounts for nothing.
	ClassUnrecognised Class = "unrecognised"
)

// Response is what one apply learned. Text is one of this package's fixed notices, never a word the
// node said: a Talos response or error can embed the configuration or its diff (E3 §5.4), and no
// message template is positively recognised yet, so every node message is withheld (§3.5, and the
// compilation contract's §8.3 rule for output that embeds the configuration).
type Response struct {
	Class Class
	// Code is the gRPC code of the answer: OK for an acceptance and for an unrecognised answer
	// without an error, the node's code otherwise, and for a transport outcome the status code the
	// client saw (clientCodes).
	Code codes.Code
	Text string
}

// Accounts reports whether this response accounts for its attempt: only an acceptance or a code the
// node returned does (execution-recovery.md §5.1).
func (r Response) Accounts() bool {
	switch r.Class {
	case ClassAccepted, ClassRejected, ClassInvalidArgument, ClassCode:
		return true
	}
	return false
}

const (
	noticeAccepted        = "the node accepted the configuration in no-reboot mode"
	noticeRejected        = "the node refused the configuration as invalid; its message is withheld"
	noticeInvalidArgument = "the node answered InvalidArgument outside the validation class; its message is withheld"
	noticeCode            = "the node answered with an error code; its message is withheld"
	noticeTransport       = "no answer from the node; the request may or may not have been received"
	noticeUnrecognised    = "the node's answer is not a recognisable acceptance; its content is withheld"
)

// TransportFailure is the response of an attempt whose request could not be made: no client could
// be dialled, so nothing was sent. It is the transport outcome, which accounts for nothing
// (execution-recovery.md §5.1): §5.2 draws no conclusion from a dial failure.
func TransportFailure() Response {
	return Response{Class: ClassTransport, Code: codes.Unavailable, Text: noticeTransport}
}

// ErrNoDeadline refuses a send whose context has no deadline: the transport deadline is recorded
// with the attempt, and an unbounded send cannot be accounted (execution-recovery.md §3.3).
var ErrNoDeadline = errors.New("talos: a send needs a transport deadline")

// Applier sends one no-reboot apply. Its outcomes, including every transport one, are Responses;
// an error means nothing was sent.
type Applier interface {
	// ApplyNoReboot sends configuration to the node at the dialled endpoint, in no-reboot mode,
	// and returns how the node answered. ctx carries the transport deadline.
	ApplyNoReboot(ctx context.Context, configuration []byte) (Response, error)
	Close() error
}

// DialApplier makes an Applier for the node at endpoint, from a talosconfig's bytes, exactly as
// Dial makes a Reader: the dialled endpoint alone, no routing, nothing read from or written to disk,
// and no error that quotes the talosconfig.
func DialApplier(ctx context.Context, talosconfig []byte, endpoint string) (Applier, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	creds, err := currentCredentials(talosconfig)
	if err != nil {
		return nil, err
	}
	c, err := client.New(ctx, client.WithConfigContext(creds), client.WithEndpoints(ep))
	if err != nil {
		return nil, errors.New("talos: the talosconfig's certificate authority, certificate or key is unusable")
	}
	return &applier{api: c}, nil
}

type applier struct {
	api *client.Client
}

func (a *applier) ApplyNoReboot(ctx context.Context, configuration []byte) (Response, error) {
	if len(configuration) == 0 {
		return Response{}, errors.New("talos: no configuration to send")
	}
	if _, ok := ctx.Deadline(); !ok {
		return Response{}, ErrNoDeadline
	}
	// A zero retry buffer commits the attempt as soon as the request is sent, so the client cannot
	// replay it: grpc-go transparently re-sends a first attempt whose stream the node reset with
	// REFUSED_STREAM, which WithDisableRetry does not stop (grpc-go stream.go shouldRetry), and that
	// would put two mutating requests under one recorded attempt.
	resp, err := a.api.ApplyConfiguration(direct(ctx), &machine.ApplyConfigurationRequest{
		Data: configuration,
		Mode: machine.ApplyConfigurationRequest_NO_REBOOT,
	}, grpc.MaxRetryRPCBufferSize(0))
	return classifyApply(ctx, resp, err), nil
}

func (a *applier) Close() error { return a.api.Close() }

// validationShape is the node's validation refusal as E4 recorded it (row 022), whitespace
// collapsed: the multi-error naming the document type. Anything that does not begin so is not
// recognised as validation, so it cannot make an operation `rejected`.
var validationShape = regexp.MustCompile(`^\d+ errors? occurred: \* v1alpha1\.Config: `)

// clientCodes are the codes the gRPC client itself produces, from something other than a status the
// node sent: a connection or transport failure, a deadline, a cancellation, a response over its
// receive limit (ResourceExhausted), one it could not decode (Internal), a malformed status from the
// other end (Unknown), and the codes it derives from a stream reset or an HTTP status when no gRPC
// status arrived: INADEQUATE_SECURITY and HTTP 403 become PermissionDenied, HTTP 401 Unauthenticated,
// HTTP 404 Unimplemented (grpc-go internal/transport/http_util.go, http2ErrConvTab and
// HTTPStatusConvTab). A plain status error of one of them cannot be told from the node's own, so it
// does not show the node answered and accounts for nothing; the same code in a response message's
// error is the node's (a NodeError). A node's genuine refusal with one of these codes is therefore
// left to a completion observation, which is the safe side of the two.
var clientCodes = []codes.Code{
	codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.ResourceExhausted, codes.Internal, codes.Unknown,
	codes.PermissionDenied, codes.Unauthenticated, codes.Unimplemented,
}

// classifyApply reads one send's outcome. It reads the node's message only to classify it and never
// returns it. An answer is what the node put in a response message: the machinery turns such a
// message's error into a client.NodeError, so a NodeError is the one provenance that shows the node
// answered, whatever its code, and a plain status error shows it only for a code the client does not
// produce itself (clientCodes). Exactly one answer is recognised; a context ending after one arrived
// does not take it back.
func classifyApply(ctx context.Context, resp *machine.ApplyConfigurationResponse, err error) Response {
	unrecognised := Response{Class: ClassUnrecognised, Code: codes.Unknown, Text: noticeUnrecognised}
	if err == nil {
		msgs := resp.GetMessages()
		if len(msgs) != 1 || msgs[0].GetMetadata().GetError() != "" || msgs[0].GetMode() != machine.ApplyConfigurationRequest_NO_REBOOT {
			return Response{Class: ClassUnrecognised, Code: codes.OK, Text: noticeUnrecognised}
		}
		return Response{Class: ClassAccepted, Code: codes.OK, Text: noticeAccepted}
	}
	var nodes []*client.NodeError
	plain := 0
	for _, leaf := range errorLeaves(err) {
		if ne, ok := errors.AsType[*client.NodeError](leaf); ok {
			nodes = append(nodes, ne)
		} else {
			plain++
		}
	}
	if len(nodes) > 0 {
		if len(nodes) != 1 || plain != 0 || len(resp.GetMessages()) != 0 {
			// Several answers, or an error beside others: not a single node's answer.
			return unrecognised
		}
		return statusResponse(errors.Unwrap(nodes[0]), true)
	}
	if len(resp.GetMessages()) > 0 {
		// An error beside a message that was not an error: not a single node's answer.
		return unrecognised
	}
	// The call's own status is read before the context: a status the node returned stands even if
	// the context ended after it; only a transport outcome takes the context's code.
	r := statusResponse(err, false)
	if r.Class == ClassTransport && ctx.Err() != nil {
		r.Code = codes.DeadlineExceeded
		if errors.Is(ctx.Err(), context.Canceled) {
			r.Code = codes.Canceled
		}
	}
	return r
}

// errorLeaves flattens the aggregates the machinery builds (a multierror, or any error that wraps
// several) into the errors they hold.
func errorLeaves(err error) []error {
	switch e := err.(type) {
	case interface{ WrappedErrors() []error }:
		var out []error
		for _, w := range e.WrappedErrors() {
			out = append(out, errorLeaves(w)...)
		}
		return out
	case interface{ Unwrap() []error }:
		var out []error
		for _, w := range e.Unwrap() {
			out = append(out, errorLeaves(w)...)
		}
		return out
	}
	return []error{err}
}

// statusResponse classifies one error by its gRPC status. fromNode is that the error came in a
// response message, so the node answered whatever its code.
func statusResponse(err error, fromNode bool) Response {
	var withStatus interface{ GRPCStatus() *status.Status }
	if err == nil || !errors.As(err, &withStatus) {
		if fromNode {
			return Response{Class: ClassCode, Code: codes.Unknown, Text: noticeCode}
		}
		// Not an answer: a client or connection failure without a gRPC status.
		return Response{Class: ClassTransport, Code: codes.Unavailable, Text: noticeTransport}
	}
	st := withStatus.GRPCStatus()
	code := st.Code()
	switch {
	case !fromNode && slices.Contains(clientCodes, code):
		return Response{Class: ClassTransport, Code: code, Text: noticeTransport}
	case code == codes.InvalidArgument:
		msg := strings.Join(strings.Fields(st.Message()), " ")
		if validationShape.MatchString(msg) && !strings.Contains(msg, "immediate mode") {
			return Response{Class: ClassRejected, Code: code, Text: noticeRejected}
		}
		return Response{Class: ClassInvalidArgument, Code: code, Text: noticeInvalidArgument}
	default:
		return Response{Class: ClassCode, Code: code, Text: noticeCode}
	}
}
