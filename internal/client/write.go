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
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// Write is one logical write (client-contracts.md §8.4, decision T9 of #256): Path is under
// /api/v1 and Body the JSON object sent, nil for a DELETE. IfMatch is sent where the route takes
// one, and Draft names the draft whose kept ETag the answer replaces and, with no IfMatch, is sent
// as it (T11). Repeat holds an operator's T10 repeat flags.
// Read reads the resource after an unknown or set-aside outcome and says what it shows; Check is
// the command that shows it, after "bronzewardctl --context <the context>", which Send adds.
// Ingests marks a draft fragment write, whose in-progress 409 is waited for up to Wait, raised to
// the claim lease the server serves (§8.4 item 2).
// Entry marks the recovery entry, §8.4 item 3's one exception: it is sent without
// Bronzeward-Expected-Epoch, its repeats check the principal and not the epoch, and the epoch change
// its own commit causes never sets it aside.
type Write struct {
	Method, Path     string
	Body             []byte
	IfMatch, Draft   string
	HumanOnly, Entry bool
	Ingests          bool
	Wait             time.Duration
	Repeat           *Repeat
	// Reads is the epoch of the reads that built Body. A first send under another epoch is refused
	// (§8.5): the preflight reads of Send can show a newer one than those reads.
	Reads string
	Read  func(*Client) (string, error)
	Check string
	// Input is the input a repeat gives where the command resolved what the operator typed, so the
	// outcome-unknown block names it instead of "the same input".
	Input string
	// checked is the client whose CheckRepeat passed for the next Send of this write; that Send
	// consumes it.
	checked *Client
}

// CheckRepeat runs a T10 repeat's checks, the principal and the confirming GET /recovery, for the
// next Send of w on c: a command that reads before Send calls it first, so a refused repeat sends
// only item 3's reads (§8.4 item 4). For a first send it does nothing.
func (c *Client) CheckRepeat(w *Write) (err error) {
	rp := w.Repeat
	if rp == nil || w.checked == c {
		return nil
	}
	defer func() {
		if err != nil {
			// A refusal ends the command before any other read: the recovery banner its reads called
			// for, with the start mode last shown (§8.5).
			c.banner()
		}
	}()
	if _, err := c.principal(rp); err != nil {
		return err
	}
	// The recovery entry confirms no epoch: its own commit mints one (§8.4 item 3).
	if !w.Entry {
		if now, why := c.confirm(); now != rp.Epoch {
			return RefusedRepeat{c.Context.fail(fmt.Errorf("%s /api/v1%s not sent: %s, not the given --expect-epoch %s", w.Method, w.Path, why, printable(rp.Epoch)))}
		}
	}
	w.checked = c
	return nil
}

// Repeat is T10's repeat of a write whose outcome was unknown: its key, reference epoch and
// principal, as the outcome-unknown block printed them. A recovery entry's has no epoch.
type Repeat struct{ Key, Epoch, Principal string }

// RefusedRepeat is a T10 repeat refused before the write was sent; the CLI exits 2.
type RefusedRepeat struct{ error }

// Unknown is a write whose outcome is unknown, or which was set aside (§8.4 items 2-4): its Error
// is T10's block, which the CLI prints on standard error before it exits 3.
// Ambiguous says an attempt, this process's or the one a T10 repeat repeats, may have committed;
// a write set aside without one is not repeated (§8.4 item 3), though none of it committed.
// Entry says it is the recovery entry's, sent with no epoch, so its repeat takes none (item 4).
type Unknown struct {
	Why, Key, Route, Where, Principal, Epoch, IfMatch, Shows, Check, Input string
	SetAside, Ambiguous, Entry                                             bool
	// Cause is the refusal a repeat got, kept so a command can read what the server named in it.
	Cause error
}

// Unwrap gives the refusal the write was set aside after, if any.
func (u *Unknown) Unwrap() error { return u.Cause }

func (u *Unknown) Error() string { return strings.Join(u.Lines(), "\n") }

// Lines are the lines of the block; a line feed inside one is text.
func (u *Unknown) Lines() []string {
	head := "outcome unknown: " + u.Why
	if u.SetAside && u.Ambiguous {
		head = "set aside, not repeated: " + u.Why + ". Its outcome is unknown: it may already have been committed"
	} else if u.SetAside {
		head = "set aside, not repeated: " + u.Why + ". No attempt of it can have committed anything"
	}
	epoch, repeat := printable(u.Epoch), fmt.Sprintf("--idempotency-key %s --expect-epoch %s --expect-principal %s", u.Key, ShellWord(u.Epoch), ShellWord(u.Principal))
	if u.Entry {
		epoch, repeat = "none: the recovery entry is sent without one", fmt.Sprintf("--idempotency-key %s --expect-principal %s", u.Key, ShellWord(u.Principal))
	}
	ls := []string{fmt.Sprintf("%s: %s: %s.", u.Where, u.Route, head), "  key:       " + u.Key, "  request:   " + u.Route, "  context:   " + u.Where,
		"  principal: " + printable(u.Principal), "  epoch:     " + epoch}
	if u.IfMatch != "" {
		ls = append(ls, "  if-match:  "+printable(u.IfMatch))
		repeat += " --if-match " + ShellWord(u.IfMatch)
	}
	ls = append(ls, "  read:      "+u.Shows+"; read it again with: "+printable(u.Check))
	input := "the same input"
	if u.Input != "" {
		input = "the same input, with " + u.Input + ","
	}
	return append(ls, "To repeat exactly this request, give "+input+" and: "+repeat)
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

// backoff is the wait before each of a write's at most three repeats (T9); sleep waits it. clock
// measures the wait for an ingesting first request, its repeats' own time included.
var (
	backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	sleep   = time.Sleep
	clock   = time.Now
)

// Send sends w (T9). Before the first send it reads the principal it compares before every repeat
// and takes the reference epoch every send carries. It repeats the identical request with the
// same key, at most three times, after an ambiguous outcome, a 503 a write repeats, and a 401 it
// renewed after; before each repeat it checks the principal and confirms the epoch with one
// GET /recovery. It returns the 2xx answer, a refusal, an *Unknown or a *RefusedRepeat.
// The recovery entry (w.Entry) carries no epoch and confirms none; its answers show the start mode
// (§8.5 item 1, 2).
func (c *Client) Send(w *Write) (r *Response, err error) {
	route := w.Method + " /api/v1" + w.Path
	if c.Cut != nil { // a Ctrl-C the user already gave starts no new write; one in flight keeps its outcome
		if e := c.Cut(nil); e != nil {
			return nil, e
		}
	}
	defer c.banner()
	c.sending = true // no read inside a write takes the Ctrl-C (interrupt.go)
	defer func() { c.sending = false }()
	if w.HumanOnly && c.signIn == nil {
		fmt.Fprintf(c.Notices, "%s: %s is for a person signed in, not an automation token: the server refuses it.\n", c.Context.Where(), route)
	}
	// Only item 3's reads come before a repeat's refusal, so the context-use read follows them.
	err = c.CheckRepeat(w)
	w.checked = nil // a passed check serves this send only
	if err != nil {
		return nil, err
	}
	who, err := c.principal(w.Repeat)
	if err != nil {
		return nil, err
	}
	key, ref := newKey(), ""
	if rp := w.Repeat; rp != nil {
		key, ref = rp.Key, rp.Epoch
	}
	// A recovery start refuses GET /server until its entry commits: for the entry that refusal shows
	// the start mode it needs (§8.5 item 1), not a failure.
	_, srv, err := c.Server()
	if err != nil && !(w.Entry && processRefusal(err)) {
		return nil, err
	}
	bound := w.Wait
	if srv == nil {
		// The entry's tolerated refusal served no settings, and the entry waits for no ingestion.
	} else if l := srv.IngestionClaim; l != nil && time.Duration(float64(l.LeaseSeconds)*float64(time.Second)) > bound {
		bound = time.Duration(float64(l.LeaseSeconds) * float64(time.Second))
	}
	// GET /server can renew the sign-in and adopt one another process stored for another subject
	// (§9.5 item 2), so the principal is read again before the first send (§8.4 item 3, T10).
	if who, err = c.principal(w.Repeat); err != nil {
		return nil, err
	}
	if rp := w.Repeat; rp != nil && !w.Entry && c.Context.LastEpoch != rp.Epoch {
		// A read after the confirming one, the context-use read's included, saw another epoch: the
		// change is known, so the repeat is refused, not sent under the old epoch (§8.4 item 3, §8.5).
		return nil, RefusedRepeat{c.Context.fail(fmt.Errorf("%s not sent: the epoch is now %s, not the given --expect-epoch %s", route, printable(c.Context.LastEpoch), printable(rp.Epoch)))}
	}
	if w.Draft != "" && w.IfMatch == "" {
		// T11: the ETag kept from the last read or write of the draft, read after the context-use read,
		// so an epoch change it saw leaves none (§8.3). With none, the write waits for a read.
		if w.IfMatch, err = ETag(c.Context, w.Draft); err != nil {
			return nil, c.Context.fail(fmt.Errorf("reading the kept ETag of %s: %w", printable(w.Draft), err))
		} else if w.IfMatch == "" {
			return nil, c.Context.fail(fmt.Errorf("%s not sent: no ETag of draft %s is kept for this context at its current epoch. Read the draft first: bronzewardctl --context %s draft show %s",
				route, printable(w.Draft), ShellWord(c.Context.Name), ShellWord(w.Draft)))
		}
	}
	if w.Repeat == nil && !w.Entry {
		// The last epoch seen before the first send, the context-use read's included (§8.4 item 3).
		ref = c.Context.LastEpoch
		if w.Reads != "" && ref != "" && ref != w.Reads {
			return nil, c.Context.fail(fmt.Errorf("%s not sent: the epoch changed from %s to %s after the request was built from reads of the earlier one. Nothing was sent. Read again with: bronzewardctl --context %s %s, then run this command again",
				route, ShellWord(w.Reads), ShellWord(ref), ShellWord(c.Context.Name), w.Check))
		}
	}
	if ref == "" && !w.Entry {
		// A context that has seen no epoch takes it from GET /recovery (§8.4 item 3).
		if ref, _ = c.confirm(); ref == "" {
			return nil, c.Context.fail(fmt.Errorf("%s not sent: GET /api/v1/recovery showed no epoch to send it under", route))
		}
	}
	u := &Unknown{Key: key, Route: route, Where: c.Context.Where(), Principal: who, Epoch: ref, IfMatch: w.IfMatch, Input: w.Input, Entry: w.Entry,
		Check: "bronzewardctl --context " + ShellWord(c.Context.Name) + " " + w.Check}
	// A T10 repeat exists because an earlier process's attempt was ambiguous (§8.4 item 4).
	ambiguous, renewed := w.Repeat != nil, false
	var last error
	// ingesting names the first request a fragment write's in-progress 409 says still runs; waited
	// is the time since the send that got the first such 409, the repeats' own time included, so
	// the bound is a fixed maximum (§8.4 item 2). No repeat starts after it, whatever the last
	// answer was; one already sent can end past it. ingestion keeps the name once an answer since
	// has cleared ingesting.
	var ingesting, ingestion string
	var since time.Time
	var waited time.Duration
	stillIngesting := func() error {
		u.Why = fmt.Sprintf("the first request with this key was still ingesting (ingestion %s) after %s", ingestion, waited)
		return c.unknown(w, u)
	}
	for try := 0; ; try++ {
		if ingesting != "" || try > 0 {
			if ingesting != "" {
				step := min(time.Second<<min(waited/time.Second, 2), bound-waited)
				fmt.Fprintf(c.Notices, "%s: the first request with this key is still ingesting (ingestion %s); repeat with the same key in %s\n",
					c.Context.Where(), ingesting, step)
				sleep(step)
			} else if try > len(backoff) {
				break
			} else {
				fmt.Fprintf(c.Notices, "%s: %s; repeat %d of %d with the same key in %s\n", c.Context.Where(), Shown(last), try, len(backoff), backoff[try-1])
				sleep(backoff[try-1])
			}
			if now, err := c.principal(nil); err != nil {
				u.Why = "the credential's principal cannot be read (" + printable(err.Error()) + "), so it cannot be compared with " + printable(who)
				return nil, c.setAside(w, u, ambiguous)
			} else if now != who {
				u.Why = "the credential now names " + printable(now) + ", not the principal " + printable(who) + " the write was sent as"
				return nil, c.setAside(w, u, ambiguous)
			}
			if now, why := c.confirm(); !w.Entry && now != ref {
				u.Why = why + ", not the reference epoch"
				return nil, c.setAside(w, u, ambiguous)
			}
			// The reads before a waited repeat take time too.
			if !since.IsZero() {
				if waited = clock().Sub(since); waited > bound {
					return nil, stillIngesting()
				}
			}
		}
		sent := clock()
		a := c.post(w, key, ref)
		p, code := a.p, ""
		if p != nil {
			code = p.Code()
		}
		last, ingesting = a.err, ""
		// The entry's not-a-recovery-start refusal: a 409 conflict with neither member (§8.5 item 2).
		normal := w.Entry && code == "conflict" && p.Doc["epoch"] == nil && p.Doc["request"] == nil
		if w.Entry && a.epoch && (a.p != nil || a.r != nil) && a.status != http.StatusUnauthorized && a.status != http.StatusForbidden && !normal {
			// Any other entry answer after the role check, which carries the epoch, was given by a
			// recovery start; a 401 or 403 shows nothing (§8.5 item 1). An answer cut off before its
			// body is read shows neither a problem nor a result: the mode known so far stays.
			c.setStartMode("recovery")
		}
		switch {
		case normal:
			if a.epoch { // an entry answer without Bronzeward-Epoch shows no start mode (§8.5)
				c.setStartMode("normal")
			}
			last = fmt.Errorf("%w\nThis server was started normally; only a server started for recovery serves the entry.", a.err)
			return nil, c.after(ambiguous, w, u, last)
		case w.Entry && code == "conflict":
			// Another entry committed, under another key or in an earlier process: refused here, not set
			// aside, since only an earlier attempt of this one can make it ambiguous (§8.4 item 3; REC-02).
			// A refusal shows the epoch; an unknown-outcome block prints none for the entry (REC-04 (a)).
			if ambiguous {
				last = fmt.Errorf("%w\nAnother recovery entry committed first; this one recorded nothing. Read it with: recovery status.", a.err)
			} else if ep, _ := p.Doc["epoch"].(string); ep != "" {
				last = fmt.Errorf("%w\nAnother recovery entry committed first (epoch %s); this one recorded nothing. Read it with: recovery status.",
					a.err, printable(ep))
			} else {
				last = fmt.Errorf("%w\nAnother recovery entry committed first; this one recorded nothing. Read it with: recovery status.", a.err)
			}
			return nil, c.after(ambiguous, w, u, last)
		case a.r != nil:
			if a.r.Header.Get("Idempotent-Replayed") == "true" {
				fmt.Fprintf(c.Notices, "%s: %s: already done earlier; showing that result.\n", c.Context.Where(), route)
			}
			c.keepETag(w, a.r)
			if w.Entry {
				// The entry committed: the minimum revision is read again, and the start mode with it
				// (§7 item 3). A failed read changes nothing about the known success.
				if _, _, serr := c.Server(); serr != nil {
					fmt.Fprintf(c.Notices, "%s: the entry committed, but GET /server could not be read again: %s.\n", c.Context.Where(), Shown(serr))
				}
			}
			return a.r, nil
		case !a.sent:
			// Nothing reached the server: repeated, never ambiguous (AMB-01).
			if errors.As(a.err, new(*trust)) {
				return nil, c.after(ambiguous, w, u, a.err)
			}
		case a.status >= 300 && a.status < 400:
			u.Why = printable(a.err.Error())
			return nil, c.unknown(w, u)
		case p != nil && a.replayed:
			// The key's stored refusal, replayed: the earlier attempt's known outcome (§8.4 item 2).
			return nil, &Blocked{a.err, []string{"This is the stored answer to an earlier attempt with this key: that attempt was refused."}}
		case p == nil && (a.status == 0 || a.status >= 500), p != nil && (code == "internal-error" || a.status >= 500 && !p.unavailable() && code != "epoch-superseded"):
			ambiguous = true
		case p != nil && p.unavailable():
		case p != nil && p.Status == http.StatusUnauthorized && c.signIn != nil && !renewed && !c.signIn.failed && c.signIn.tokens.RefreshToken != "":
			renewed = true
			if rerr := c.renew(); rerr != nil {
				fmt.Fprintf(c.Notices, "%s: %s.\n", c.Context.Where(), Shown(rerr))
				return nil, c.after(ambiguous, w, u, a.err)
			}
		case w.Ingests && code == "conflict" && p.Doc["ingestion"] != nil && p.Doc["draft"] == nil && (try > 0 || w.Repeat != nil):
			// A repeat's in-progress 409: the first request still runs, or ended without committing
			// while its claim's lease runs (§8.4 item 2, AMB-02). It is repeated with the same key
			// until it answers otherwise, within the wait bound, and counts as no repeat.
			if since.IsZero() {
				since = sent
			}
			ingestion = printable(fmt.Sprint(p.Doc["ingestion"]))
			if waited = clock().Sub(since); waited >= bound {
				return nil, stillIngesting()
			}
			ingesting = ingestion
			try--
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
			// An operator's T10 repeat, refused and not replayed: decided by the input checks before
			// the lookup, or after a lookup that found no record, so this send did nothing and says
			// nothing of the attempt it repeats (§8.2; AMB-13).
			u.Why = "this send was refused (" + printable(a.err.Error()) + ") and did nothing; the original outcome is still unknown"
			u.Cause = a.err
			return nil, c.unknown(w, u)
		case code == "precondition-failed" && w.Draft != "":
			// Another write moved the draft first (ETAG-01): stop and show the newer revision, never
			// read the new ETag and resend (§8.3, T11). An attempt of this process that committed
			// would have been replayed, not refused.
			return nil, c.moved(w, a.err)
		default:
			// This send's own answer: an identical earlier attempt in this process met the same input
			// checks, and a repeat that found the key's record would have replayed it.
			return nil, a.err
		}
	}
	if !ambiguous {
		return nil, &Blocked{last, []string{fmt.Sprintf("Not done: none of the %d attempts committed anything of its own.", len(backoff)+1)}}
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

// moved is ETAG-01's refusal (journeys §5.4): the revision the sent ETag names and the one a read
// shows now. The read's ETag is not kept, so the next write still needs a read of the change.
func (c *Client) moved(w *Write, err error) error {
	now := "unknown (reading the draft failed)"
	if r, rerr := c.Get("/drafts/" + url.PathEscape(w.Draft)); rerr == nil {
		var d apitypes.DraftBody
		if json.Unmarshal(r.Body, &d) == nil {
			now = fmt.Sprint(d.Revision)
		}
	}
	return &Blocked{err, []string{
		fmt.Sprintf("The draft changed since you last read it (you saw revision %s, it is now revision %s). Nothing was saved. Review the change before writing again:", DraftRevision(w.IfMatch), now),
		fmt.Sprintf("  bronzewardctl --context %s draft show %s", ShellWord(c.Context.Name), ShellWord(w.Draft))}}
}

// DraftRevision is the draft revision a draft's ETag names: the server writes "<revision>-<token>"
// (internal/api/inventory.go). It is "unknown" for any other ETag.
func DraftRevision(etag string) string {
	n, _, ok := strings.Cut(strings.Trim(strings.TrimPrefix(etag, "W/"), `"`), "-")
	if !ok || n == "" || strings.Trim(n, "0123456789") != "" {
		return "unknown"
	}
	return n
}

// keepETag replaces the draft's kept ETag with the answer's, even when the command fails later
// (T11). A failure to keep it is shown: the next write to the draft then needs a read first.
func (c *Client) keepETag(w *Write, r *Response) {
	if et := r.Header.Get("ETag"); w.Draft != "" && et != "" {
		if err := KeepETag(c.Context, w.Draft, et); err != nil {
			fmt.Fprintf(c.Notices, "%s: keeping the ETag of %s: %s\n", c.Context.Where(), printable(w.Draft), Shown(err))
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
		// A repeat's check sends only the identity and confirming reads (§8.4 item 4), so a new epoch
		// this read shows is followed by GET /server later, at Send's context-use read (§8.5).
		read := c.Get
		if rp != nil {
			read = c.retry
		}
		r, err := read("/identity")
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
	r, _, err := c.once(context.Background(), "/recovery") // inside a write: never cancelled
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
	r        *Response
	p        *Problem
	status   int
	sent     bool
	replayed bool // the answer is a stored one, replayed for this key (§8.4 item 2)
	epoch    bool // the answer carried Bronzeward-Epoch
	err      error
}

// processRefusal says err is a 409 recovery-mode-active without a scope member: the restriction of a
// process started for recovery, which before its entry refuses every request but the entry.
func processRefusal(err error) bool {
	var p *Problem
	return errors.As(err, &p) && p.Code() == "recovery-mode-active" && p.Doc["scope"] == nil
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
	// closed, after bytes may have gone out, when the request has no body, http.NoBody or a
	// GetBody (Request.isReplayable). Without GetBody and with a body that is never NoBody, it
	// resends nothing, so every repeat that could reach the server is this layer's, after its
	// checks; a failure once a connection is taken counts as ambiguous.
	q.GetBody = nil
	if w.Body == nil {
		// A DELETE sends no body and no Content-Type (§8.1). NewRequest made an empty body NoBody;
		// the transport reads this empty reader instead, finds it empty and sends no body.
		q.Body = io.NopCloser(bytes.NewReader(nil))
	}
	q.Header.Set("Authorization", "Bearer "+c.token)
	q.Header.Set("Accept", "application/json")
	if w.Body != nil {
		q.Header.Set("Content-Type", "application/json")
	}
	q.Header.Set("Idempotency-Key", key)
	if !w.Entry {
		q.Header.Set("Bronzeward-Expected-Epoch", epoch)
	}
	if w.IfMatch != "" {
		q.Header.Set("If-Match", w.IfMatch)
	}
	r, err := c.hc.Do(q)
	if err != nil {
		a.err = c.Context.fail(fmt.Errorf("%s: %w", route, transportError(c.Context, c.Context.Host(), err)))
		return a
	}
	defer r.Body.Close()
	a.sent, a.status, a.replayed = true, r.StatusCode, r.Header.Get("Idempotent-Replayed") == "true"
	a.epoch = r.Header.Get("Bronzeward-Epoch") != ""
	c.observe(route, r)
	if r.StatusCode >= 300 && r.StatusCode < 400 {
		a.err = fmt.Errorf("%s answered %s redirecting to \"%s\", which bronzewardctl does not follow; it is not repeated",
			route, printable(r.Status), quoted(r.Header.Get("Location")))
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
		a.r = &Response{Header: r.Header, Body: body, Moves: c.moves}
		return a
	}
	if a.p = c.problem(route, r, body); a.p != nil {
		a.err = a.p
	} else {
		a.err = c.Context.fail(fmt.Errorf("%s answered %s, not a Bronzeward problem", route, printable(r.Status)))
	}
	return a
}
