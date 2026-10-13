package api

import (
	"context"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// This file is the dispatch sender (execution-recovery.md §3.5): the one file of this package that
// dials a client able to send a mutating request, takes a decrypted artifact's bytes or makes the
// request. internal/talos's guard holds every other file to that.

// applyDialer dials a client able to send a mutating request to the node at endpoint.
type applyDialer func(ctx context.Context, talosconfig []byte, endpoint string) (talos.Applier, error)

// send makes the attempt's one request: it dials the node at endpoint from talosconfig and sends
// art to it in no-reboot mode under ctx's deadline, the attempt's transport deadline. Every outcome
// is the Response the attempt records; one that could not be dialled or started is a transport
// failure, which accounts for nothing (§5.1). The configuration and the bytes live only in this
// call.
func (a *API) send(ctx context.Context, talosconfig []byte, endpoint string, art provider.Artifact) talos.Response {
	dial := a.o.dialApply
	if dial == nil {
		dial = talos.DialApplier
	}
	ap, err := dial(ctx, talosconfig, endpoint)
	if err != nil {
		return talos.TransportFailure()
	}
	defer func() { _ = ap.Close() }()
	b := provider.ArtifactBytes(art)
	defer clear(b)
	resp, err := ap.ApplyNoReboot(ctx, b)
	if err != nil { // no deadline or nothing to send: the request was not made
		return talos.TransportFailure()
	}
	return resp
}
