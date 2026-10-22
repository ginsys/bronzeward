package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/ginsys/bronzeward/internal/auth"
)

// route is one row of persistence-api.md §9.2.
type route struct {
	method, pattern string      // pattern is under prefix, in net/http's syntax
	roles           []auth.Role // the qualifying roles, in the route's listed order (§10.3, choice §17.20)
	humanOnly       bool        // automation is refused whatever its roles (§10.3)
	ifMatch         bool        // If-Match is required
	exclusive       bool        // the transaction takes the installation state FOR UPDATE (§5)
	normalStartOnly bool        // a recovery start refuses it after its entry too (ruling X3, §12.2)
	action          string      // the act's action (§10.5)
	// keyed names the body member that carries unextracted input: the request is fingerprinted
	// with the digest key (§7.1), its input implements keyedInput, and that member is left out
	// of the canonical body the rest of the fingerprint covers. Empty for SHA-256.
	keyed string
	input func() input
	// prepare runs after the key's lookup and before the transaction; it may write its own
	// short transaction, as §10's first-use insert does.
	prepare func(ctx context.Context, a *API, q *request) error
	effect  effectFunc // a mutating route's transaction body
	read    readFunc   // a read route
}

func (rt *route) mutating() bool { return rt.method != http.MethodGet }

// entryPattern is recovery-mode entry's route, the one a recovery start serves before its entry
// commits (§12.2).
const entryPattern = "/recovery/entries"

// effectFunc is a mutating route's work inside its transaction (§5): after the key lock and the
// installation state, before the act and the record.
type effectFunc func(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error)

type readFunc func(a *API, w http.ResponseWriter, q *request)

// result is what an effect commits and answers.
type result struct {
	status         int
	location, etag string
	body           any
	subjects       []string // the act's subject identifiers
	operation      string   // a 202's operation, recorded with the idempotency record (§7.1)
	// atActOrder runs once the act-order lock, the transaction's last (§5 rule 5), is held, before
	// body is encoded: the writes whose recorded time must follow every lock wait (rule 4). It may
	// complete body, and writes only rows whose references the effect already holds locked.
	atActOrder func(ctx context.Context, tx *sql.Tx) error
	// afterCommit runs once, after this request's own COMMIT is confirmed, outside the
	// transaction; never for a rolled-back attempt or a replay.
	afterCommit func()
}

var anyRole = []auth.Role{auth.Viewer, auth.Author, auth.Publisher, auth.Approver, auth.RecoveryAdmin}

// routes returns §9.2's table in its order. A route with neither effect nor read answers
// 501 not-implemented until the issue that owns it lands (overview decision 1).
func routes() []*route {
	g := func(p string) *route { return &route{method: http.MethodGet, pattern: p, roles: anyRole} }
	m := func(method, p string, roles ...auth.Role) *route {
		return &route{method: method, pattern: p, roles: roles}
	}
	human := func(rt *route) *route { rt.humanOnly = true; return rt }
	ifm := func(rt *route) *route { rt.ifMatch = true; return rt }
	exclusive := func(rt *route) *route { rt.exclusive = true; return rt }
	normal := func(rt *route) *route { rt.normalStartOnly = true; return rt }
	const post, put, del = http.MethodPost, http.MethodPut, http.MethodDelete
	author, publisher, approver, recovery := auth.Author, auth.Publisher, auth.Approver, auth.RecoveryAdmin

	read := func(rt *route, fn readFunc) *route { rt.read = fn; return rt }

	rs := []*route{
		read(g("/clusters"), listClusters), read(g("/clusters/{id}"), getCluster),
		read(g("/machines"), listMachines), read(g("/machines/{id}"), getMachine),
		read(g("/machines/{id}/observations"), listObservations), read(g("/machines/{id}/timeline"), listTimeline),
	}
	for _, kind := range []string{"fragment", "profile", "assignment"} {
		for _, p := range []string{"/" + kind + "s", "/" + kind + "s/{id}", "/" + kind + "s/{id}/revisions", "/" + kind + "-revisions/{id}"} {
			rs = append(rs, read(g(p), sourceReads[p]))
		}
	}
	rs = append(rs,
		read(g("/drafts"), listDrafts), read(g("/drafts/{id}"), getDraft),
		read(g("/ingestions"), listIngestions), read(g("/ingestions/{id}"), getIngestion), read(g("/releases"), listReleases), read(g("/releases/{id}"), getRelease),
		read(g("/releases/{id}/machines/{m}/review"), getReview),
		read(g("/plans"), listPlans), read(g("/plans/{id}"), getPlan), read(g("/approvals/{id}"), getApproval), read(g("/operations"), listOperations),
		read(g("/operations/{id}"), getOperation), read(g("/operations/{id}/events"), getOperationEvents),
		read(g("/acts"), listActs), read(g("/recovery"), getRecovery),
		read(g("/dependencies"), listDependencies), read(g("/dependencies/{id}"), getDependency),
		read(g("/dependencies/{id}/releases"), dependencyReleases), read(g("/dependencies/{id}/alerts"), listAlerts(true)),
		read(g("/dependency-alerts"), listAlerts(false)),
		ingestionStart().on(ifm(human(m(post, "/ingestions", author)))),
		read(human(m(http.MethodGet, "/ingestions/{id}/review", author)), getIngestionReview),
		ingestionMark().on(human(m(post, "/ingestions/{id}/marks", author))),
		ingestionContinuation().on(human(m(post, "/ingestions/{id}/continuations", author))),
		ingestionTakeover().on(human(m(post, "/ingestions/{id}/takeovers", author))),
		ingestionAbandonment().on(human(m(post, "/ingestions/{id}/abandonments", author))),
		clusterCreation().on(human(m(post, "/clusters", author))),
		machineInventory().on(human(m(post, "/machines", author))),
		endpointReplacement().on(human(m(post, "/machines/{id}/talos-endpoints", author))),
		draftCreation().on(m(post, "/drafts", author)),
	)
	rs = append(rs,
		fragmentUpdate().on(ifm(m(put, "/drafts/{id}/fragments/{name}", author))),
		fragmentRemoval().on(ifm(m(del, "/drafts/{id}/fragments/{name}", author))),
		profileUpdate().on(ifm(m(put, "/drafts/{id}/profiles/{name}", author))),
		profileRemoval().on(ifm(m(del, "/drafts/{id}/profiles/{name}", author))),
		assignmentUpdate().on(ifm(m(put, "/drafts/{id}/assignments/{machine}", author))),
		assignmentRemoval().on(ifm(m(del, "/drafts/{id}/assignments/{machine}", author))),
	)
	rs = append(rs,
		draftDiscard().on(ifm(m(post, "/drafts/{id}/discard", author))),
		publicationRequest().on(ifm(m(post, "/drafts/{id}/publications", publisher))),
		normal(planCreation().on(m(post, "/plans", publisher))),
		// The creator is a publisher (choice §17.22); whether this one created the plan is the
		// handler's check, in its transaction (§10.3).
		planCancellation().on(m(post, "/plans/{id}/cancellations", publisher, approver, recovery)),
		normal(planApproval().on(human(m(post, "/plans/{id}/approvals", approver)))),
		approvalRevocation().on(m(post, "/approvals/{id}/revocations", approver, recovery)),
		machineFreeze().on(m(post, "/machines/{id}/freezes", author, publisher, approver, recovery)),
		normal(machineUnfreeze().on(m(post, "/machines/{id}/unfreezes", approver))),
		recoveryEntry(), exclusive(exitRoute().on(human(m(post, exitPattern, recovery)))),
		scopeMarkRoute().on(human(m(post, "/recovery/scopes/{machine}/marks", recovery))),
		scopeReleaseRoute().on(human(m(post, "/recovery/scopes/{machine}/releases", recovery))),
		scopeAccountingRoute().on(human(m(post, "/recovery/accountings", recovery))),
		accountingDecisionRoute().on(human(m(post, "/operations/{id}/attempts/{attempt}/accountings", recovery))),
		operationTakeover().on(human(m(post, "/operations/{id}/takeovers", recovery))),
		operationResolution().on(human(m(post, "/operations/{id}/resolutions", recovery))),
		identityRevocations(),
	)
	return rs
}
