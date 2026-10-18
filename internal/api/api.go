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
	"sync"
	"sync/atomic"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
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
	DecryptStaging(ctx context.Context, ct provider.Ciphertext) ([]byte, error)
	TalosAccess(ctx context.Context, cluster string) (provider.TalosAccess, error)
}

// Executor is what observations and dispatch need of *provider.Executor (persistence-api.md §3.3,
// execution-recovery.md §3.1): the executor identity's read of a cluster's Talos access, and its
// decryption of a bound artifact under the artifact key.
type Executor interface {
	TalosAccess(ctx context.Context, cluster string) (provider.TalosAccess, error)
	DecryptArtifact(ctx context.Context, ct provider.Ciphertext, keyVersion int) (provider.Artifact, error)
	ArtifactKey() string
}

// Publishers are publication's provider identities (compilation.md §1): the metadata identity's
// reads by name, and the compiler identity's pinned reads and encryption under the artifact key.
type Publishers struct {
	Meta     compile.MetadataReader
	Compiler interface {
		compile.PinnedReader
		compile.ArtifactEncrypter
	}
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

// deps are what ingestion, publication and observations need: the provider clients, the claim
// timers and this process as the owner of the claims and jobs it takes. With no provider
// configured, ing, pub and exe are nil and the ingestion and publication routes answer 503. The runners and the publish worker
// live for life, the server's lifetime, and runs counts them; wake tells the worker a job was
// queued, and adopt tells the adoption loop a plan was approved.
type deps struct {
	ing      Ingester
	pub      *publishClients
	exe      Executor
	timers   config.Ingestion
	owner    staging.Owner
	life     context.Context
	runs     *sync.WaitGroup
	wake     chan struct{}
	adopt    chan struct{}
	dispatch chan struct{}    // tells the executor loop a plan was approved
	exec     config.Execution // the plan defaults and the transport maximum (execution-recovery.md §5.2)
	// preEntry is true while a process started with the recovery-start flag has not committed its
	// entry: every route but the entry is refused (§12.2).
	preEntry *atomic.Bool
	// recoveryStart marks a process started with the flag, the only one that serves the entry;
	// start is the epoch it read at its start, which its entry compares (§12.2 T9 step 1); onEntry
	// runs once its entry has committed; entry is the epoch its entry minted.
	recoveryStart bool
	start         string
	onEntry       func()
	entry         *entryState
}

// options are nil or false in production; tests set them.
type options struct {
	logf           func(format string, args ...any)                                                     // the server log; log.Printf by default
	extra          []*route                                                                             // routes beyond §9.2
	noKeyLock      bool                                                                                 // the key-lock control (§7.2, §16)
	noRevokerCheck bool                                                                                 // T5c's revoking-human lock control
	shareExit      bool                                                                                 // the exit's installation-lock control: FOR SHARE in place of FOR UPDATE (T11, §16)
	shareRevoke    bool                                                                                 // the identity revocation's installation-lock control: FOR SHARE in place of FOR UPDATE (T5c, §16)
	noActOrder     bool                                                                                 // the act-order lock control (GET /acts)
	noDraftLock    bool                                                                                 // the draft read-lock control (GET /drafts)
	noProcessEpoch bool                                                                                 // the process-epoch control: drops `$my_epoch = $current_epoch` at a job claim, a takeover, an ingestion start, a commitment and an attempt (§5.1, §16)
	noEpochTerm    bool                                                                                 // the epoch-term control at an attempt: drops the owner token's epoch from its fence (§5.1, DB row 027)
	noScopeGate    bool                                                                                 // the scope-gate control at commitment (execution-recovery.md §3.2 comparison 6, §7.5)
	noMachineLock  bool                                                                                 // the machine-lock control at commitment (choice §10.24, §3.2 comparison 2)
	noReadLock     string                                                                               // the named lock's control at commitment and attempt (§3.2, §9.2): "installation state", "assignment head", "plan state", "rollout" or, at an attempt, "machine"
	readThenWrite  bool                                                                                 // the owner-token control at an attempt (§3.3 comparison 7, DS row 018)
	noPlanCheck    bool                                                                                 // the in-transaction plan-check control at an attempt (§3.3 comparison 1, DS row 006)
	latestAccess   bool                                                                                 // the control that sends with the Talos access read again at send (PA §16)
	dispatchRoute  bool                                                                                 // the route control: a plan's observation dials the machine's current endpoint, resolved at dispatch, not the plan's route (PA §3.3, §16)
	staleAccess    *provider.TalosAccess                                                                // the stale-credential control: an observation uses this access instead of reading the latest (PA §16)
	afterEffect    func()                                                                               // runs in the transaction, after the effect, act and record
	beforeRead     func()                                                                               // runs when a read route starts, after routing
	beforeCommit   func(attempt int) error                                                              // fails an attempt before COMMIT
	commit         func(*sql.Tx) error                                                                  // replaces (*sql.Tx).Commit
	onRunner       func(job)                                                                            // takes each job instead of the runner
	onPublish      func()                                                                               // runs instead of waking the publish worker
	onIdle         func()                                                                               // runs when the publish worker found no job, before it waits
	onAdoptIdle    func()                                                                               // runs after each pass of the adoption loop, before it waits
	onDriftIdle    func()                                                                               // runs after each pass of the drift loop, before it waits
	onExecIdle     func()                                                                               // runs after each pass of the executor loop, before it waits
	dialApply      applyDialer                                                                          // talos.DialApplier by default (sender.go)
	afterStage     func()                                                                               // runs when a job is staged, before T1
	beforeT1       func()                                                                               // runs before T1 begins
	stopAt         func(step string) bool                                                               // a draft update's ingestion stops at step, as a killed process would
	dial           func(ctx context.Context, talosconfig []byte, endpoint string) (talos.Reader, error) // talos.Dial by default
	nodeTimeout    time.Duration                                                                        // bounds each node request; nodeTimeout by default
	quoteErrors    bool                                                                                 // the pass-through control (§16): a node read's failure logs its error text
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
	fpKey        string        // the digest key that computed fingerprint, "transit/<key>@v<N>"; empty for SHA-256
	actID        string        // the act of the transaction attempt in progress
	ingest       *draftIngest  // a draft update's ingestion, from its prepare to its end
	mark         *markEvidence // a scope mark's observation and dependency check, from its prepare
}

type ctxKey struct{}

func requestOf(r *http.Request) *request { return r.Context().Value(ctxKey{}).(*request) }

// New returns the /api/v1 handler. ing, pub, exe and ic are nil without a provider. epoch is the
// one this process read at its start: it owns claims and jobs under it, and under no later one
// (§5.1). With a provider, it starts the publish worker and the adoption and drift loops. The
// ingest runners, the worker and the loops stop when life ends. A recovery start (recovery is
// true) starts none of them and serves only the entry until its entry commits (§12.2); the loops
// start at the normal restart that entry allows (execution-recovery.md §7.2). onEntry runs once a
// recovery start's entry has committed, after it owns under the new epoch.
func New(life context.Context, db *sql.DB, a Authenticator, cfg config.Auth, ex config.Execution, ing Ingester, pub *Publishers,
	exe Executor, ic *config.Ingestion, epoch string, recovery bool, onEntry func()) http.Handler {
	d := deps{ing: ing, exe: exe, life: life, exec: ex, preEntry: new(atomic.Bool), recoveryStart: recovery, start: epoch,
		onEntry: onEntry, entry: new(entryState)}
	d.preEntry.Store(recovery)
	if pub != nil {
		d.pub = &publishClients{meta: pub.Meta, reader: pub.Compiler, encrypter: pub.Compiler}
	}
	if ic != nil {
		d.timers = *ic
		d.owner = staging.Owner{ID: ic.Instance + "/" + strconv.Itoa(os.Getpid()) + "/" + rand.Text(), Epoch: epoch}
	}
	api := newAPI(db, a, cfg, d, options{})
	if recovery {
		return api
	}
	api.startPublisher()
	api.startAdopter()
	api.startDrifter()
	api.startExecutor()
	api.startRecoveryReads()
	return api
}

func newAPI(db *sql.DB, a Authenticator, cfg config.Auth, d deps, o options) *API {
	if o.logf == nil {
		o.logf = log.Printf
	}
	if o.dial == nil {
		o.dial = talos.Dial
	}
	if o.nodeTimeout == 0 {
		o.nodeTimeout = nodeTimeout
	}
	if d.life == nil {
		d.life = context.Background()
	}
	if d.runs == nil {
		d.runs = &sync.WaitGroup{}
	}
	if d.wake == nil {
		d.wake = make(chan struct{}, 1)
	}
	if d.adopt == nil {
		d.adopt = make(chan struct{}, 1)
	}
	if d.dispatch == nil {
		d.dispatch = make(chan struct{}, 1)
	}
	if d.preEntry == nil {
		d.preEntry = new(atomic.Bool)
	}
	api := &API{db: db, authn: a, denied: auth.NewDenied(cfg.DeniedSubjects), issuer: cfg.OIDC.Issuer, mux: http.NewServeMux(), d: d, o: o}
	for _, rt := range append(routes(), o.extra...) {
		api.mux.Handle(rt.method+" "+prefix+rt.pattern, api.handle(rt))
	}
	api.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		api.problem(w, requestOf(r), api.unrouted(refuse(http.StatusNotFound, "not-found", "no route")))
	})
	return api
}

// preEntryRefusal is §12.2's refusal of every request but liveness and the entry before a
// recovery start's entry commits.
func preEntryRefusal() *refusal {
	return refuse(http.StatusConflict, "recovery-mode-active",
		"this process started with the recovery-start flag serves only the recovery entry until it commits")
}

// unrouted is the refusal of a request no route takes: ref, or before a recovery start's entry
// commits, preEntryRefusal (§12.2: every request but liveness and the entry).
func (a *API) unrouted(ref *refusal) *refusal {
	if a.d.preEntry.Load() {
		return preEntryRefusal()
	}
	return ref
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
		a.problem(w, q, a.unrouted(refuse(http.StatusNotFound, "not-found", "the path is not in clean form")))
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
		// Before its entry commits, a recovery start serves the entry only: the restored epoch is
		// still current, so nothing else is recorded or disclosed (§12.2).
		if a.d.preEntry.Load() && rt.pattern != entryPattern {
			a.problem(w, q, preEntryRefusal())
			return
		}
		// After it, the acts a recovery start does not serve wait for the normal restart (§12.2).
		if a.d.recoveryStart && rt.normalStartOnly {
			a.problem(w, q, normalStartRefusal())
			return
		}
		// A process started without the flag refuses the entry before it records anything, its
		// principal included, and replays no entry record (§12.2, §12.4).
		if rt.pattern == entryPattern && !a.d.recoveryStart {
			a.problem(w, q, notRecoveryStart())
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
