package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// sameCAKey is the signer's comparison, a variable so that a test can run its mutation control.
var sameCAKey = provider.CAKey.Holds

// The verdicts of compilation.md §2.3 step 3a.
const (
	verdictMatch      = "match"
	verdictMismatch   = "mismatch"
	verdictNoKey      = "no key"
	verdictUnreadable = "unreadable"
)

// verifyCAKey is step 3a for an import that set a CA key apart: the signer compares the offered key
// with the version the cluster's latest first-key act names, and the verdict is recorded as an event
// of the operation, committed before anything else of the run. Only a match returns neither a
// refusal nor an error. The key reaches the comparison and nothing else: no event, log line or
// refusal holds it or a digest of it.
func (a *API) verifyCAKey(ctx context.Context, j job, c *ingest.Candidate) (*refusal, error) {
	k, cause, err := a.actKey(ctx, j.claim.Cluster)
	var mismatch error
	var verdict string
	switch {
	case ctx.Err() != nil:
		return nil, errStop
	case err == nil:
		verdict = verdictMatch
		if mismatch = c.MatchCAKey(func(offered string) bool { return sameCAKey(k, offered) }); mismatch != nil {
			verdict = verdictMismatch
		}
	case cause == "": // a database failure: the run stops with it
		return nil, err
	case cause == "absent" || cause == "version-gone":
		verdict = verdictNoKey
	default:
		verdict = verdictUnreadable
	}
	entry := map[string]any{"type": "ca-key-match", "verdict": verdict}
	if err == nil {
		v := k.Version()
		entry["path"], entry["version"] = v.Path, v.Version
		entry["createdTime"] = v.CreatedTime.UTC().Format(time.RFC3339Nano)
	}
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := staging.Hold(ctx, tx, a.d.owner, j.claim); err != nil {
			return err
		}
		return a.event(ctx, tx, j, entry)
	}); err != nil {
		return nil, fmt.Errorf("recording the CA key verdict: %w", err)
	}
	switch {
	case cause != "":
		a.quote(j, cause, err)
		detail := "the cluster's stored CA key could not be read to verify the offered one"
		if cause == "absent" {
			detail = noFirstKeyDetail
		}
		r := refuse(http.StatusServiceUnavailable, "talos-access-unavailable", detail+"; the claim is abandoned").
			with("cluster", j.claim.Cluster)
		r.cause = cause
		return r, nil
	case mismatch != nil:
		return a.failure(j, "CA key match", mismatch), nil
	}
	return nil, nil
}
