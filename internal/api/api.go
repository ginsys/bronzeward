// Package api serves /api/v1 (persistence-api.md §9): it authenticates every request (§10),
// checks each route's roles and the human-only rule (§10.3), records idempotency (§7) and acts
// (§10.5), and answers problem documents (§9.4).
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	pathpkg "path" // the tests declare path
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

const prefix = "/api/v1"

// Authenticator is what the API needs of *auth.Verifier.
type Authenticator interface {
	Authenticate(ctx context.Context, authorization string) (auth.Principal, error)
}

// Ingester is what ingestion needs of *provider.Ingestion (compilation.md §1, §2.3).
type Ingester interface {
	Digest(ctx context.Context, input []byte, version int) (provider.Digest, error)
	CreateGeneration(ctx context.Context, p provider.GenerationPath, v provider.Value) (provider.Generation, error)
	EncryptBaseline(ctx context.Context, plaintext []byte) (provider.Ciphertext, error)
	EncryptStaging(ctx context.Context, envelope []byte) (provider.Ciphertext, error)
}

type API struct {
	db     *sql.DB
	authn  Authenticator
	denied auth.Denied
	issuer string
	mux    *http.ServeMux
	d      deps
	o      options
}

// deps are what ingestion needs: the provider client, the claim timers and this process as the
// owner of the claims it creates. With no provider configured, ing is nil and the ingestion
// routes answer 503.
type deps struct {
	ing    Ingester
	timers config.Ingestion
	owner  staging.Owner
}

// options are nil or false in production; tests set them.
type options struct {
	logf           func(format string, args ...any) // the server log; log.Printf by default
	extra          []*route                         // routes beyond §9.2
	noKeyLock      bool                             // the key-lock control (§7.2, §16)
	noRevokerCheck bool                             // T5c's revoking-human lock control
	noActOrder     bool                             // the act-order lock control (GET /acts)
	noDraftLock    bool                             // the draft read-lock control (GET /drafts)
	afterEffect    func()                           // runs in the transaction, after the effect, act and record
	beforeRead     func()                           // runs when a read route starts, after routing
	beforeCommit   func(attempt int) error          // fails an attempt before COMMIT
	commit         func(*sql.Tx) error              // replaces (*sql.Tx).Commit
}

// request is one API request as it passes the checks.
type request struct {
	id           string // req_ identifier: the problem instance, the act's request id
	r            *http.Request
	route        *route
	principal    auth.Principal
	role         auth.Role // the first qualifying role (choice §17.20)
	epoch        string
	recovery     bool
	key, ifMatch string
	input        input
	material     []byte // the length-prefixed request without a keyed member (§7.1)
	fingerprint  []byte
	fpKey        string // the digest key that computed fingerprint, "transit/<key>@v<N>"; empty for SHA-256
	actID        string // the act of the transaction attempt in progress
}

type ctxKey struct{}

func requestOf(r *http.Request) *request { return r.Context().Value(ctxKey{}).(*request) }

// New returns the /api/v1 handler. ing and ic are nil without a provider. epoch is the one this
// process read at its start: it owns claims under it, and under no later one (§5.1).
func New(db *sql.DB, a Authenticator, cfg config.Auth, ing Ingester, ic *config.Ingestion, epoch string) http.Handler {
	d := deps{ing: ing}
	if ic != nil {
		d.timers = *ic
		d.owner = staging.Owner{ID: ic.Instance + "/" + strconv.Itoa(os.Getpid()) + "/" + rand.Text(), Epoch: epoch}
	}
	return newAPI(db, a, cfg, d, options{})
}

func newAPI(db *sql.DB, a Authenticator, cfg config.Auth, d deps, o options) *API {
	if o.logf == nil {
		o.logf = log.Printf
	}
	api := &API{db: db, authn: a, denied: auth.NewDenied(cfg.DeniedSubjects), issuer: cfg.OIDC.Issuer, mux: http.NewServeMux(), d: d, o: o}
	for _, rt := range append(routes(), o.extra...) {
		api.mux.Handle(rt.method+" "+prefix+rt.pattern, api.handle(rt))
	}
	api.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		api.problem(w, requestOf(r), refuse(http.StatusNotFound, "not-found", "no route"))
	})
	return api
}

// ServeHTTP authenticates first, then reads the installation state for the epoch headers, then
// routes (§10: every request authenticates, an unknown path included).
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := &request{id: id.New(id.Request), r: r}
	p, err := a.authn.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		a.o.logf("%s %s %s: %v", q.id, r.Method, r.URL.EscapedPath(), err)
		a.problem(w, q, authRefusal(err))
		return
	}
	q.principal = p
	if err := a.db.QueryRowContext(r.Context(), `SELECT epoch, recovery_mode FROM installation_state`).Scan(&q.epoch, &q.recovery); err != nil {
		a.o.logf("%s: installation state: %v", q.id, err)
		a.problem(w, q, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the database could not be read; nothing was committed"))
		return
	}
	if ref := staleToken(q); ref != nil {
		a.problem(w, q, ref)
		return
	}
	setEpoch(w, q)
	// ServeMux would answer an unclean path with a redirect, skipping the problem document, its
	// log line and the route's role check: no route answers one.
	if p := r.URL.Path; p != cleanPath(p) {
		a.problem(w, q, refuse(http.StatusNotFound, "not-found", "the path is not in clean form"))
		return
	}
	a.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, q)))
}

// cleanPath is ServeMux's: p with no empty, "." or ".." element, keeping a trailing slash.
func cleanPath(p string) string {
	if p == "" || p[0] != '/' {
		p = "/" + p
	}
	c := pathpkg.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c
}

// staleToken refuses a service principal whose token is from an epoch other than q's (§10.2): an
// entry can commit between the token's verification and a later read of the installation state.
func staleToken(q *request) *refusal {
	if q.principal.Kind == auth.Service && q.principal.Epoch != q.epoch {
		return refuse(http.StatusUnauthorized, "unauthenticated", "")
	}
	return nil
}

func authRefusal(err error) *refusal {
	switch {
	case errors.Is(err, auth.ErrIdentityRevoked):
		return refuse(http.StatusForbidden, "identity-revoked", "")
	case errors.Is(err, auth.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "authentication could not reach the identity provider or the database; nothing was committed")
	default:
		return refuse(http.StatusUnauthorized, "unauthenticated", "")
	}
}

// setEpoch writes §9.1's headers, for responses to authenticated requests only.
func setEpoch(w http.ResponseWriter, q *request) {
	w.Header().Set("Bronzeward-Epoch", q.epoch)
	if q.recovery {
		w.Header().Set("Bronzeward-Recovery-Mode", "true")
	} else {
		w.Header().Del("Bronzeward-Recovery-Mode")
	}
}

func (a *API) handle(rt *route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := requestOf(r)
		q.r, q.route = r, rt
		if ref := authorize(q); ref != nil {
			a.problem(w, q, ref)
			return
		}
		if rt.mutating() {
			if ref := preconditions(q); ref != nil {
				a.problem(w, q, ref)
				return
			}
		}
		switch {
		case rt.effect != nil:
			a.mutate(w, q)
		case rt.read != nil:
			rt.read(a, w, q)
		default:
			a.problem(w, q, refuse(http.StatusNotImplemented, "not-implemented",
				rt.method+" "+prefix+rt.pattern+" is routed and role-checked; its handler lands with the issue that owns it"))
		}
	}
}

// authorize is §10.3's check before any transaction. It records the act's role: the first
// qualifying one in the route's listed order (choice §17.20).
func authorize(q *request) *refusal {
	rt := q.route
	if rt.humanOnly && q.principal.Kind != auth.Human {
		return refuse(http.StatusForbidden, "forbidden", "this route is for humans only").with("roles", rt.roles).with("humanOnly", true)
	}
	for _, want := range rt.roles {
		if slices.Contains(q.principal.Roles, want) {
			q.role = want
			return nil
		}
	}
	return refuse(http.StatusForbidden, "forbidden", "no role of this credential qualifies").with("roles", rt.roles)
}

var keyShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// preconditions checks a mutating request's headers (§7.1, §9.2); none needs a transaction.
func preconditions(q *request) *refusal {
	h := q.r.Header
	switch keys := h.Values("Idempotency-Key"); {
	case len(keys) == 0 || (len(keys) == 1 && keys[0] == ""):
		return refuse(http.StatusPreconditionRequired, "idempotency-key-required", "")
	case len(keys) > 1 || !keyShape.MatchString(keys[0]):
		return refuse(http.StatusBadRequest, "invalid-request", "Idempotency-Key must be one header of 16 to 128 characters of [A-Za-z0-9_-]")
	default:
		q.key = keys[0]
	}
	if q.route.ifMatch {
		switch ms := h.Values("If-Match"); {
		case len(ms) == 0 || (len(ms) == 1 && ms[0] == ""):
			return refuse(http.StatusPreconditionRequired, "precondition-required", "")
		case len(ms) > 1:
			return refuse(http.StatusBadRequest, "invalid-request", "If-Match must be one header")
		default:
			q.ifMatch = ms[0]
		}
	}
	return nil
}
