package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// MinRevision is the lowest API revision this client works with (decision T7 of #254).
const MinRevision = 2

const problemPrefix = "urn:bronzeward:problem:"

// Client sends one command's requests to one context's server. Notices (epoch changes, the
// recovery banner, deprecations, retries) go to Notices; nothing it writes holds a token.
type Client struct {
	Context *Context
	Notices io.Writer
	hc      *http.Client
	token   string
	signIn  *signIn // an OIDC context's; nil for automation
	service string  // the automation principal GET /identity served, once read
	warned  map[string]bool

	// Arm, when set, takes the Ctrl-C for each read (interrupt.go). Cut records the Ctrl-C a read took
	// and answers the first one the run took (nil asks without recording). sending: a write is in flight.
	Arm     func() (context.Context, func() bool)
	Cut     func(*Interrupted) *Interrupted
	sending bool

	// Revision is the API revision the latest GET /server showed: a read can refresh it after a new
	// epoch, so a caller that gates on it reads it where it needs it (§7 item 1).
	Revision int

	// crossed: an answer showed a new epoch, so the context's kept ETags are dropped (§8.5 item 1).
	changed, newEpoch, recovery, crossed bool

	// moves counts the answers that showed another epoch than the one before, a failed answer
	// included, so a change that a later answer set back is still seen (§8.5).
	moves int
}

// EpochMoves is the number of times an answer showed another epoch than the one before. A command
// that builds one output from several reads compares it before and after them.
func (c *Client) EpochMoves() int { return c.moves }

// Response is a successful answer. Moves is EpochMoves as this answer left it: a later read, the
// GET /server that a new epoch calls for included, can move the epoch before the caller sees the
// answer, and a command that compares the count from this answer on sees that move too.
type Response struct {
	Header http.Header
	Body   []byte
	Moves  int
}

// Open prepares the client for x, reading its automation token or its stored sign-in, which it
// renews first when it is about to expire (§9.5 item 1). A context edited by hand is checked as
// context add checks it, so no token is sent over plain http to a remote host.
func Open(x *Context, in TokenInput, notices, verbose io.Writer) (*Client, error) {
	if err := x.Check(); err != nil {
		return nil, x.fail(err)
	}
	hc, err := httpClient(x, verbose)
	if err != nil {
		return nil, x.fail(err)
	}
	c := &Client{Context: x, Notices: notices, hc: hc, warned: map[string]bool{}}
	if x.Credential == "oidc" {
		if in.File != "" || in.Stdin {
			return nil, x.fail(errors.New("--token-file and --token-stdin take an automation token; this context signs in"))
		}
		if c.signIn, err = openSignIn(x, in.NoInput, notices); err != nil {
			return nil, x.fail(err)
		}
		c.token = c.signIn.tokens.AccessToken
		if c.signIn.expiring() && c.signIn.tokens.RefreshToken != "" {
			if err := c.renew(); err != nil {
				fmt.Fprintf(notices, "%s: %s; the current token stays in use until the server refuses it.\n", x.Where(), Shown(err))
			}
		}
		return c, nil
	}
	if c.token, err = automationToken(x, in); err != nil {
		return nil, x.fail(err)
	}
	return c, nil
}

// Where names the context and its server for every error and wait (client-contracts.md §5 item 2),
// printable: a command that never checks the context still shows them (§6 item 4).
func (x *Context) Where() string { return printable(x.Name + " (" + x.Server + ")") }

func (x *Context) fail(err error) error { return fmt.Errorf("%s: %w", x.Where(), err) }

// Live checks a new context's server: GET /livez must answer 204 (§5 item 5). It sends no
// credential and shows only that something answers.
func Live(x *Context, verbose io.Writer) error { return LiveContext(context.Background(), x, verbose) }

// LiveContext is Live ended by ctx: a cancelled ctx returns *Interrupted.
func LiveContext(ctx context.Context, x *Context, verbose io.Writer) error {
	hc, err := httpClient(x, verbose)
	if err != nil {
		return x.fail(err)
	}
	q, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(x.Server, "/")+"/livez", nil)
	if err != nil {
		return x.fail(err)
	}
	r, err := hc.Do(q)
	if err != nil {
		if ctx.Err() != nil {
			return x.fail(&Interrupted{Route: "GET /livez"})
		}
		return x.fail(transportError(x, x.Host(), err))
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		if l := r.Header.Get("Location"); l != "" {
			return x.fail(fmt.Errorf("GET /livez answered %s redirecting to \"%s\", which bronzewardctl does not follow", printable(r.Status), quoted(l)))
		}
		return x.fail(fmt.Errorf("GET /livez answered %s, not 204: this is not a Bronzeward server, or not its base URL", printable(r.Status)))
	}
	return nil
}

// Server reads GET /server on context use and after a new epoch (§7 item 3, §8.5), keeps the
// start mode it shows and then shows the recovery banner. It refuses a server below MinRevision,
// returning what it served with the refusal.
func (c *Client) Server() (*Response, *apitypes.ServerBody, error) {
	defer c.banner()
	r, err := c.retry("/server")
	c.newEpoch = false
	if err != nil {
		return nil, nil, err
	}
	var s apitypes.ServerBody
	if err := json.Unmarshal(r.Body, &s); err != nil {
		return nil, nil, c.Context.fail(fmt.Errorf("GET /server: %w", err))
	}
	c.Revision = s.ApiRevision
	c.setStartMode(string(s.StartMode))
	if m := int(s.MaxTokenLifetimeSeconds); m != c.Context.MaxTokenLifetimeSeconds {
		c.Context.MaxTokenLifetimeSeconds, c.changed = m, true
	}
	if s.ApiRevision < MinRevision {
		return r, &s, c.Context.fail(fmt.Errorf("the server serves API revision %d; this bronzewardctl needs revision %d or later", s.ApiRevision, MinRevision))
	}
	return r, &s, nil
}

func (c *Client) setStartMode(m string) {
	if m != c.Context.StartMode {
		c.Context.StartMode, c.changed = m, true
	}
}

// Get reads path under /api/v1. After an answer showing a new epoch it reads GET /server again
// for the start mode (§8.5) before the recovery banner is shown; a failed re-read fails the read.
func (c *Client) Get(path string) (*Response, error) { return c.get(path, nil) }

// get is Get with the retry policy of retryIn: with a stop, the read is repeated until stop answers true.
func (c *Client) get(path string, stop func() bool) (*Response, error) {
	r, err := c.retryUntil(path, stop)
	if errors.As(err, new(*Interrupted)) { // a Ctrl-C ends the read: no refresh arms another request
		return nil, err
	}
	// The end of a watch also ends the read with no refresh, which no stop can cut, but the banner is shown.
	if errors.Is(err, ErrWatchEnded) {
		c.banner()
		return nil, err
	}
	if c.newEpoch {
		if _, _, serr := c.Server(); serr != nil {
			r, err = nil, errors.Join(err, serr)
		}
	}
	c.banner()
	return r, err
}

// retry sends a GET, retrying a retryable 503, a 5xx that is not a Bronzeward problem and a
// transport failure after 1 s, doubling to at most 30 s, at most 5 tries (§8.2). A sign-in the
// server answers 401 to is renewed once and the request sent once more (§9.5 item 1).
func (c *Client) retry(path string) (*Response, error) { return c.retryUntil(path, nil) }

// watchStep is how often a wait of a watch's read asks whether the watch has ended.
const watchStep = 250 * time.Millisecond

// ErrWatchEnded joins the error of a read that a watch's retries could not finish because the watch
// ended first (its --timeout, or whatever its Until answers): the read gave nothing.
var ErrWatchEnded = errors.New("the watch ended before the server answered")

// retryUntil is retry. A watch's operation read passes stop: the read is then repeated with no cap of
// tries until stop answers true, which returns the last error joined with ErrWatchEnded (§8.6 item 2).
func (c *Client) retryUntil(path string, stop func() bool) (*Response, error) {
	ctx, disarm := c.arm()
	r, err := c.retryIn(ctx, path, stop)
	if disarm() { // a Ctrl-C taken while the read ended still stops the command
		return nil, c.cut(&Interrupted{Route: "GET /api/v1" + path})
	}
	return r, err
}

// retryIn is retry with the Ctrl-C of ctx: it ends the request in flight and the wait before a retry.
// renew is not cancelled: a refresh the identity provider already answered must be kept (§9.5).
func (c *Client) retryIn(ctx context.Context, path string, stop func() bool) (*Response, error) {
	delay, renewed := time.Second, false
	for try := 1; ; try++ {
		r, retry, err := c.once(ctx, path)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var p *Problem
		if c.signIn != nil && !renewed && !c.signIn.failed && errors.As(err, &p) && p.Status == http.StatusUnauthorized && c.signIn.tokens.RefreshToken != "" {
			renewed = true
			if rerr := c.renew(); rerr == nil {
				try--
				continue
			} else {
				fmt.Fprintf(c.Notices, "%s: %s.\n", c.Context.Where(), Shown(rerr))
			}
		}
		if !retry || stop == nil && try == 5 {
			return r, err
		}
		if stop == nil {
			fmt.Fprintf(c.Notices, "%s: %s; try %d of 5 in %s\n", c.Context.Where(), Shown(err), try+1, delay)
			if werr := waitFor(ctx, delay); werr != nil {
				return nil, werr
			}
		} else {
			fmt.Fprintf(c.Notices, "%s: %s; try %d in %s, until the watch ends\n", c.Context.Where(), Shown(err), try+1, delay)
			// A watch that ends (--timeout, or a Ctrl-C its caller takes itself) ends the wait within one
			// step, not after the whole delay. The steps are cheap: an Until that reads the server limits
			// its own reads.
			for left := delay; left > 0; {
				step := min(left, watchStep)
				if werr := waitFor(ctx, step); werr != nil {
					return nil, werr
				}
				if left -= step; stop() {
					return r, errors.Join(err, ErrWatchEnded)
				}
			}
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func (c *Client) once(ctx context.Context, path string) (*Response, bool, error) {
	route := "GET /api/v1" + path
	q, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.Context.Server, "/")+"/api/v1"+path, nil)
	if err != nil {
		return nil, false, c.Context.fail(err)
	}
	q.Header.Set("Authorization", "Bearer "+c.token)
	q.Header.Set("Accept", "application/json")
	r, err := c.hc.Do(q)
	if err != nil {
		err = transportError(c.Context, c.Context.Host(), err)
		var t *trust
		return nil, !errors.As(err, &t), c.Context.fail(fmt.Errorf("%s: %w", route, err))
	}
	defer r.Body.Close()
	c.observe(route, r)
	if r.StatusCode >= 300 && r.StatusCode < 400 {
		return nil, false, c.Context.fail(fmt.Errorf("%s answered %s redirecting to \"%s\"; bronzewardctl follows no redirect, and nothing was resent",
			route, printable(r.Status), quoted(r.Header.Get("Location"))))
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// A cut-off 4xx is still the refusal its status names, so it is not repeated.
		return nil, r.StatusCode < 400 || r.StatusCode >= 500,
			c.Context.fail(fmt.Errorf("%s answered %s, and reading the answer failed: %w", route, printable(r.Status), printableError(err)))
	}
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		if r.Header.Get("Bronzeward-Epoch") == "" {
			fmt.Fprintf(c.Notices, "%s: %s answered without Bronzeward-Epoch, a server defect\n", c.Context.Where(), route)
		}
		return &Response{Header: r.Header, Body: body, Moves: c.moves}, false, nil
	}
	if p := c.problem(route, r, body); p != nil {
		return nil, p.unavailable(), p
	}
	return nil, r.StatusCode >= 500, c.Context.fail(fmt.Errorf("%s answered %s, not a Bronzeward problem", route, printable(r.Status)))
}

// problem is the Bronzeward problem document an answer carries, or nil.
func (c *Client) problem(route string, r *http.Response, body []byte) *Problem {
	var doc map[string]any
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/problem+json" || json.Unmarshal(body, &doc) != nil {
		return nil
	}
	if t, _ := doc["type"].(string); !strings.HasPrefix(t, problemPrefix) {
		return nil
	}
	p := &Problem{Status: r.StatusCode, Doc: doc, Route: route, Where: c.Context.Where(), Subject: c.Subject()}
	c.signedIn(p)
	// Any answer 409 recovery-mode-active without a scope member is a recovery start before its
	// entry (§8.5 item 1).
	if p.Code() == "recovery-mode-active" && p.Doc["scope"] == nil {
		c.setStartMode("recovery")
	}
	return p
}

// unavailable: a 503 that a read retries and a write repeats (§8.2).
func (p *Problem) unavailable() bool {
	return p.Status == http.StatusServiceUnavailable &&
		slices.Contains([]string{"dependency-unavailable", "talos-access-unavailable", "transient-conflict"}, p.Code())
}

// observe applies the response headers every answer may carry: the epoch (§8.5), the recovery
// header and a deprecation (§7 item 4), shown once per route: the method and path, without the
// query, so the pages of one list warn once.
func (c *Client) observe(route string, r *http.Response) {
	route, _, _ = strings.Cut(route, "?")
	if e := r.Header.Get("Bronzeward-Epoch"); e != "" && e != c.Context.LastEpoch {
		if c.Context.LastEpoch != "" {
			fmt.Fprintf(c.Notices, "%s: The installation entered recovery mode or was restored; what was read before may no longer hold.\n", c.Context.Where())
		}
		c.Context.LastEpoch, c.changed, c.newEpoch = e, true, c.Context.LastEpoch != ""
		c.crossed = c.crossed || c.newEpoch
		if c.newEpoch {
			c.moves++
		}
	}
	c.recovery = c.recovery || r.Header.Get("Bronzeward-Recovery-Mode") == "true"
	if d := r.Header.Get("Deprecation"); d != "" && !c.warned[route] {
		c.warned[route] = true
		fmt.Fprintf(c.Notices, "%s: %s is deprecated (Deprecation: %s)\n", c.Context.Where(), route, printable(d))
	}
}

// banner shows the recovery banner once per command, with the start mode last shown.
func (c *Client) banner() {
	if c.recovery && !c.warned["recovery"] {
		c.warned["recovery"] = true
		fmt.Fprintf(c.Notices, "RECOVERY MODE is on in %s (start mode: %s).\n", c.Context.Where(), ShownStartMode(c.Context.StartMode))
	}
}

// Save keeps the epoch, start mode and maximum token lifetime in the contexts file, leaving its
// other fields as they are now. A context removed and added again under the same name for another server while the
// command ran keeps its own: what was read belongs to the old server.
func (c *Client) Save() error {
	if !c.changed {
		return nil
	}
	err := UpdateConfig(func(cfg *Config) error {
		if x := cfg.Find(c.Context.Name); x != nil && x.Server == c.Context.Server {
			x.LastEpoch, x.StartMode, x.MaxTokenLifetimeSeconds = c.Context.LastEpoch, c.Context.StartMode, c.Context.MaxTokenLifetimeSeconds
		}
		return nil
	})
	if err != nil {
		return c.Context.fail(fmt.Errorf("keeping the epoch, start mode and maximum token lifetime: %w", err))
	}
	if c.crossed {
		if err := dropEarlier(c.Context); err != nil {
			return c.Context.fail(fmt.Errorf("dropping the ETags kept before the epoch change: %w", err))
		}
	}
	return nil
}
