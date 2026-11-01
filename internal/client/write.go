package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// Write is one logical write (client-contracts.md §8.4, decision T9 of #256): Path is under
// /api/v1 and Body the JSON object sent. IfMatch is sent where the route takes one, and Draft names
// the draft whose kept ETag the answer replaces (T11). Repeat holds an operator's T10 repeat flags.
// Read reads the resource after an unknown or set-aside outcome and says what it shows; Check is
// the command that shows it, after "bronzewardctl --context <the context>", which Send adds.
type Write struct {
	Method, Path   string
	Body           []byte
	IfMatch, Draft string
	HumanOnly      bool
	Repeat         *Repeat
	Read           func(*Client) (string, error)
	Check          string
}

// Repeat is T10's repeat of a write whose outcome was unknown: its key, reference epoch and
// principal, as the outcome-unknown block printed them.
type Repeat struct{ Key, Epoch, Principal string }

// RefusedRepeat is a T10 repeat refused before the write was sent; the CLI exits 2.
type RefusedRepeat struct{ error }

// Unknown is a write whose outcome is unknown, or which was set aside (§8.4 items 2-4): its Error
// is T10's block, which the CLI prints on standard error before it exits 3.
// Ambiguous says an attempt, this process's or the one a T10 repeat repeats, may have committed;
// a write set aside without one is not repeated (§8.4 item 3), though none of it committed.
type Unknown struct {
	Why, Key, Route, Where, Principal, Epoch, IfMatch, Shows, Check string
	SetAside, Ambiguous                                             bool
}

func (u *Unknown) Error() string {
	head := "outcome unknown: " + u.Why
	if u.SetAside && u.Ambiguous {
		head = "set aside, not repeated: " + u.Why + ". Its outcome is unknown: it may already have been committed"
	} else if u.SetAside {
		head = "set aside, not repeated: " + u.Why + ". No attempt of it can have committed anything"
	}
	s := fmt.Sprintf("%s: %s: %s.\n  key:       %s\n  request:   %s\n  context:   %s\n  principal: %s\n  epoch:     %s\n",
		u.Where, u.Route, head, u.Key, u.Route, u.Where, printable(u.Principal), printable(u.Epoch))
	repeat := fmt.Sprintf("--idempotency-key %s --expect-epoch %s --expect-principal %s", u.Key, ShellWord(u.Epoch), ShellWord(u.Principal))
	if u.IfMatch != "" {
		s += "  if-match:  " + printable(u.IfMatch) + "\n"
		repeat += " --if-match " + ShellWord(u.IfMatch)
	}
	s += "  read:      " + u.Shows + "; read it again with: " + printable(u.Check) + "\n"
	return s + "To repeat exactly this request, give the same input and: " + repeat
}

// ShellWord is s as one shell word, its unprintable characters escaped.
func ShellWord(s string) string { return "'" + strings.ReplaceAll(printable(s), "'", `'\''`) + "'" }

// KeyShape is the server's (internal/api/api.go:394); a key this client makes is 22 characters.
var KeyShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// newKey is one write's key: 128 bits from the operating system's random source, base64url
// without padding (§8.4 item 1), never derived from the content.
func newKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// backoff is the wait before each of a write's at most three repeats (T9); sleep waits it.
var (
	backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	sleep   = time.Sleep
)

// Send sends w (T9). Before the first send it reads the principal it compares before every repeat
// and takes the reference epoch every send carries. It repeats the identical request with the
// same key, at most three times, after an ambiguous outcome, a 503 a write repeats, and a 401 it
// renewed after; before each repeat it checks the principal and confirms the epoch with one
// GET /recovery. It returns the 2xx answer, a refusal, an *Unknown or a *RefusedRepeat.
func (c *Client) Send(w *Write) (r *Response, err error) {
	route := w.Method + " /api/v1" + w.Path
	defer c.banner()
	if w.HumanOnly && c.signIn == nil {
		fmt.Fprintf(c.Notices, "%s: %s is for a person signed in, not an automation token: the server refuses it.\n", c.Context.Where(), route)
	}
	who, err := c.principal(w.Repeat)
	if err != nil {
		return nil, err
	}
	key, ref := newKey(), ""
	if rp := w.Repeat; rp != nil {
		// Only item 3's reads come before the refusal, so the context-use read follows them.
		key, ref = rp.Key, rp.Epoch
		if now, why := c.confirm(); now != ref {
			return nil, RefusedRepeat{c.Context.fail(fmt.Errorf("%s not sent: %s, not the given --expect-epoch %s", route, why, printable(ref)))}
		}
	}
	if _, _, err := c.Server(); err != nil {
		return nil, err
	}
	if w.Repeat == nil {
		// The last epoch seen before the first send, the context-use read's included (§8.4 item 3).
		ref = c.Context.LastEpoch
	}
	if ref == "" {
		// A context that has seen no epoch takes it from GET /recovery (§8.4 item 3).
		if ref, _ = c.confirm(); ref == "" {
			return nil, c.Context.fail(fmt.Errorf("%s not sent: GET /api/v1/recovery showed no epoch to send it under", route))
		}
	}
	u := &Unknown{Key: key, Route: route, Where: c.Context.Where(), Principal: who, Epoch: ref, IfMatch: w.IfMatch,
		Check: "bronzewardctl --context " + ShellWord(c.Context.Name) + " " + w.Check}
	// A T10 repeat exists because an earlier process's attempt was ambiguous (§8.4 item 4).
	ambiguous, renewed := w.Repeat != nil, false
	var last error
	for try := 0; ; try++ {
		if try > 0 {
			if try > len(backoff) {
				break
			}
			fmt.Fprintf(c.Notices, "%s: %v; repeat %d of %d with the same key in %s\n", c.Context.Where(), last, try, len(backoff), backoff[try-1])
			sleep(backoff[try-1])
			if now, err := c.principal(nil); err != nil {
				u.Why = "the credential's principal cannot be read (" + printable(err.Error()) + "), so it cannot be compared with " + printable(who)
				return nil, c.setAside(w, u, ambiguous)
			} else if now != who {
				u.Why = "the credential now names " + printable(now) + ", not the principal " + printable(who) + " the write was sent as"
				return nil, c.setAside(w, u, ambiguous)
			}
			if now, why := c.confirm(); now != ref {
				u.Why = why + ", not the reference epoch"
				return nil, c.setAside(w, u, ambiguous)
			}
		}
		a := c.post(w, key, ref)
		p, code := a.p, ""
		if p != nil {
			code = p.Code()
		}
		last = a.err
		switch {
		case a.r != nil:
			if a.r.Header.Get("Idempotent-Replayed") == "true" {
				fmt.Fprintf(c.Notices, "%s: %s: already done earlier; showing that result.\n", c.Context.Where(), route)
			}
			c.keepETag(w, a.r)
			return a.r, nil
		case !a.sent:
			// Nothing reached the server: repeated, never ambiguous (AMB-01).
			if errors.As(a.err, new(*trust)) {
				return nil, c.after(ambiguous, w, u, a.err)
			}
		case a.status >= 300 && a.status < 400:
			u.Why = printable(a.err.Error())
			return nil, c.unknown(w, u)
		case p == nil && (a.status == 0 || a.status >= 500), p != nil && (code == "internal-error" || a.status >= 500 && !p.unavailable() && code != "epoch-superseded"):
			ambiguous = true
		case p != nil && p.unavailable():
		case p != nil && p.Status == http.StatusUnauthorized && c.signIn != nil && !renewed && !c.signIn.failed && c.signIn.tokens.RefreshToken != "":
			renewed = true
			if rerr := c.renew(); rerr != nil {
				fmt.Fprintf(c.Notices, "%s: %v.\n", c.Context.Where(), rerr)
				return nil, c.after(ambiguous, w, u, a.err)
			}
		case code == "conflict" && p.Doc["request"] != nil && p.Doc["epoch"] != nil:
			u.Why = "the key's record is from an earlier epoch (" + printable(fmt.Sprint(p.Doc["epoch"])) + ")"
			return nil, c.setAside(w, u, ambiguous)
		case code == "epoch-mismatch":
			u.Why = "the epoch is now " + printable(fmt.Sprint(p.Doc["epoch"])) + ", and this send committed nothing"
			return nil, c.setAside(w, u, ambiguous)
		case code == "idempotency-key-reused" && (try > 0 || w.Repeat != nil):
			u.Why = "the key is recorded for another request; this send did nothing, and the original outcome is still unknown"
			return nil, c.unknown(w, u)
		case p == nil, p.Status == http.StatusUnauthorized || code == "forbidden" || code == "identity-revoked" || code == "epoch-superseded" ||
			code == "recovery-mode-active" && p.Doc["scope"] == nil:
			// Decided before the idempotency lookup, or not by Bronzeward: nothing said of an earlier
			// attempt (§8.2).
			return nil, c.after(ambiguous, w, u, a.err)
		case w.Repeat != nil:
			// An operator's T10 repeat, refused: the server checks the input before its lookup, and
			// a lookup that found the key's record would have replayed it, so this send did nothing
			// and says nothing of the attempt it repeats (§8.2; AMB-04, AMB-13).
			u.Why = "this send was refused (" + printable(a.err.Error()) + ") and did nothing; the original outcome is still unknown"
			return nil, c.unknown(w, u)
		default:
			// This send's own answer: an identical earlier attempt in this process met the same input
			// checks, and a repeat that found the key's record would have replayed it.
			return nil, a.err
		}
	}
	if !ambiguous {
		return nil, fmt.Errorf("%w\nNot done: none of the %d attempts committed anything of its own.", last, len(backoff)+1)
	}
	return nil, c.after(ambiguous, w, u, last)
}

// after reports a write that ends without a 2xx: not done, unless an earlier attempt was
// ambiguous, which leaves the outcome unknown.
func (c *Client) after(ambiguous bool, w *Write, u *Unknown, err error) error {
	if !ambiguous {
		return err
	}
	u.Why = "an attempt may have committed before the connection or the server failed, and the last answer was: " + printable(err.Error())
	if w.Repeat != nil {
		u.Why = "an attempt may have committed: the one this command repeats, or one it sent; the last answer was: " + printable(err.Error())
	}
	return c.unknown(w, u)
}

func (c *Client) setAside(w *Write, u *Unknown, ambiguous bool) error {
	u.SetAside, u.Ambiguous = true, ambiguous
	return c.unknown(w, u)
}

// unknown reads the resource for the block (T9) and returns it.
func (c *Client) unknown(w *Write, u *Unknown) error {
	if w.Read == nil {
		u.Shows = "nothing was read"
	} else if s, err := w.Read(c); err != nil {
		u.Shows = "the read failed: " + printable(err.Error())
	} else {
		u.Shows = printable(s)
	}
	return u
}

// keepETag replaces the draft's kept ETag with the answer's, even when the command fails later
// (T11). A failure to keep it is shown: the next write to the draft then needs a read first.
func (c *Client) keepETag(w *Write, r *Response) {
	if et := r.Header.Get("ETag"); w.Draft != "" && et != "" {
		if err := KeepETag(c.Context, w.Draft, et); err != nil {
			fmt.Fprintf(c.Notices, "%s: keeping the ETag of %s: %v\n", c.Context.Where(), w.Draft, err)
		}
	}
}

// principal is who a write is sent as (§8.4 item 3): a human's issuer and subject by the client's
// reading of the token, automation's the principal GET /identity serves, read once per process
// since its token does not change within one. With rp, a different one refuses the repeat.
func (c *Client) principal(rp *Repeat) (string, error) {
	var who string
	if t := c.TokenClaims(); t != nil {
		who = t.Issuer + "|" + t.Subject
	} else if c.signIn != nil {
		return "", c.Context.fail(errors.New("the access token cannot be read, so its principal cannot be checked; nothing was sent"))
	} else if c.service != "" {
		who = c.service
	} else {
		r, err := c.Get("/identity")
		if err != nil {
			return "", err
		}
		var id apitypes.IdentityBody
		if err := json.Unmarshal(r.Body, &id); err != nil || id.Principal == nil {
			return "", c.Context.fail(errors.New("GET /api/v1/identity served no principal, so a repeat could not be checked; nothing was sent"))
		}
		who, c.service = *id.Principal, *id.Principal
	}
	if rp != nil && rp.Principal != who {
		return "", RefusedRepeat{c.Context.fail(fmt.Errorf("the write is not sent: the credential names %s, not the given --expect-principal %s", printable(who), printable(rp.Principal)))}
	}
	return who, nil
}

// confirm is the epoch one GET /recovery shows, with what it showed when it shows none.
func (c *Client) confirm() (string, string) {
	r, _, err := c.once("/recovery")
	if err != nil {
		return "", "the confirming read GET /api/v1/recovery failed: " + printable(err.Error())
	}
	if e := r.Header.Get("Bronzeward-Epoch"); e != "" {
		return e, "the epoch is now " + printable(e)
	}
	return "", "the confirming read GET /api/v1/recovery showed no epoch"
}

// attempt is one send's answer: the 2xx, or the problem and the error to show; status is 0 with
// no complete answer, and sent says a connection was taken, so some byte may have gone out.
type attempt struct {
	r      *Response
	p      *Problem
	status int
	sent   bool
	err    error
}

func (c *Client) post(w *Write, key, epoch string) attempt {
	route := w.Method + " /api/v1" + w.Path
	var a attempt
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { a.sent = true }}
	q, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), w.Method,
		strings.TrimSuffix(c.Context.Server, "/")+"/api/v1"+w.Path, bytes.NewReader(w.Body))
	if err != nil {
		a.err = c.Context.fail(err)
		return a
	}
	// Go's transport resends a request carrying Idempotency-Key on a reused connection the server
	// closed, after bytes may have gone out. Without GetBody it resends only a request none of whose
	// bytes were written, so every repeat that could reach the server is this layer's, after its
	// checks; a failure once a connection is taken counts as ambiguous.
	q.GetBody = nil
	q.Header.Set("Authorization", "Bearer "+c.token)
	q.Header.Set("Accept", "application/json")
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", key)
	q.Header.Set("Bronzeward-Expected-Epoch", epoch)
	if w.IfMatch != "" {
		q.Header.Set("If-Match", w.IfMatch)
	}
	r, err := c.hc.Do(q)
	if err != nil {
		a.err = c.Context.fail(fmt.Errorf("%s: %w", route, transportError(c.Context, c.Context.Host(), err)))
		return a
	}
	defer r.Body.Close()
	a.sent, a.status = true, r.StatusCode
	c.observe(route, r)
	if r.StatusCode >= 300 && r.StatusCode < 400 {
		a.err = fmt.Errorf("%s answered %s redirecting to %q, which bronzewardctl does not follow; it is not repeated",
			route, printable(r.Status), printable(r.Header.Get("Location")))
		return a
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.err = c.Context.fail(fmt.Errorf("%s answered %s, and reading the answer failed: %w", route, printable(r.Status), printableError(err)))
		if r.StatusCode < 400 || r.StatusCode >= 500 {
			a.status = 0 // no complete answer: ambiguous
		}
		return a
	}
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		if r.Header.Get("Bronzeward-Epoch") == "" {
			fmt.Fprintf(c.Notices, "%s: %s answered without Bronzeward-Epoch, a server defect\n", c.Context.Where(), route)
		}
		a.r = &Response{Header: r.Header, Body: body}
		return a
	}
	if a.p = c.problem(route, r, body); a.p != nil {
		a.err = a.p
	} else {
		a.err = c.Context.fail(fmt.Errorf("%s answered %s, not a Bronzeward problem", route, printable(r.Status)))
	}
	return a
}
