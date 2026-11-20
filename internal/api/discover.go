package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos/discover"
	"github.com/ginsys/bronzeward/internal/talos/signer"
)

// discoverDeadline bounds the whole discovery of a cluster, from the first dial to the last member read
// (persistence-api.md §9.2, POST /discoveries). A member not reached by then is listed unanswered, and
// the answer says it is incomplete. maxDiscoveryMembers is the most members a cluster may list: more is
// a refusal, raised before any member is dialled. About 100 machines is the v1 target.
const (
	discoverDeadline    = 60 * time.Second
	maxDiscoveryMembers = 128
)

// postDiscovery is POST /discoveries (§9.2): it finds a cluster's identity and members from a first key,
// the way `bronzewardctl cluster discover` does from a talosconfig, without recording anything. It is the
// one POST that only reads, so the route is read-only: no Idempotency-Key, no expected epoch, no act, no
// record, and no replay that would serve a stale member list. The document is extracted, a reader
// certificate is signed from the key it holds, the cluster is read with that certificate, and the key,
// the certificate and the document are dropped with the request. It uses no provider: nothing is read from
// or written to the stored keys.
func postDiscovery(a *API, w http.ResponseWriter, q *request) {
	w.Header().Set("Cache-Control", "no-store")
	body, err := a.discoverCluster(q)
	if err != nil {
		var ref *refusal
		if !errors.As(err, &ref) {
			ref = refuse(http.StatusInternalServerError, "internal-error", "the discovery failed; nothing was recorded")
		}
		a.problem(w, q, ref) // logs the refusal, which names no value
		return
	}
	a.o.logf("%s %s %s: 200 discovery by %s, %d members, complete %t", q.id, q.r.Method, q.r.URL.EscapedPath(), q.principal.ID, len(body.Members), body.Complete)
	writeJSON(w, "application/json", http.StatusOK, body)
}

// discoveryMember is one member of a discovered cluster. SMBIOSUUID and Platform are empty when the
// member did not answer, or reported none.
type discoveryMember struct {
	NodeID     string `json:"nodeId"`
	Hostname   string `json:"hostname"`
	Address    string `json:"address"`
	Role       string `json:"role"`
	SMBIOSUUID string `json:"smbiosUuid"`
	Platform   string `json:"platform"`
	Answered   bool   `json:"answered"`
}

// discoveryBody is the answer: what `bronzewardctl cluster discover` reports. Complete is false when the
// deadline ended the walk before every member was read, so a member that did not answer may be reachable.
type discoveryBody struct {
	ClusterID          string            `json:"clusterId"`
	Version            string            `json:"version"`
	Contract           string            `json:"contract"`
	Supported          bool              `json:"supported"`
	Endpoint           string            `json:"endpoint"`
	KubernetesEndpoint string            `json:"kubernetesEndpoint"`
	Members            []discoveryMember `json:"members"`
	Complete           bool              `json:"complete"`
}

// discoverCluster is postDiscovery's work: the request's checks, then the three steps. Every error that
// is not a refusal is a defect.
func (a *API) discoverCluster(q *request) (discoveryBody, error) {
	if q.r.URL.RawQuery != "" {
		return discoveryBody{}, refuse(http.StatusBadRequest, "invalid-request", "a discovery takes no query")
	}
	q.input = q.route.input()
	_, err := decodeBody(q.r, q.input, "")
	if err == nil {
		err = q.input.check(a)
	}
	if err != nil {
		var ref *refusal
		if !errors.As(err, &ref) {
			ref = refuse(http.StatusBadRequest, "invalid-request", err.Error())
		}
		return discoveryBody{}, ref
	}
	in := q.input.(*firstKeyInput)
	// The deadline covers the whole call, the extraction included: the call lasts the deadline at most.
	dctx, cancel := context.WithTimeout(q.r.Context(), a.o.discoverWithin)
	defer cancel()
	// A talosconfig is read at the endpoint, which may accept the connection and stall: the extraction is
	// bounded like every node read, inside the deadline. The walk below has the deadline alone.
	ca, err := nodeRead(dctx, a.o.nodeTimeout, func(c context.Context) (provider.CAKey, error) {
		return signer.Extract(c, signer.Kind(in.Kind), in.Document.bytes(), in.Endpoint)
	})
	if err != nil {
		return discoveryBody{}, discoveryRefusal(err)
	}
	// The certificate covers the whole discovery and the rounding of its whole-second encoding.
	cert, err := signer.Sign(ca, role.Reader, time.Now(), a.o.discoverWithin+signer.RoundingSlack, a.d.maxCert)
	if errors.Is(err, signer.ErrLifetime) {
		return discoveryBody{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"the deployment's certificate lifetime is shorter than a discovery needs; nothing was recorded")
	}
	if err != nil {
		return discoveryBody{}, discoveryRefusal(&signer.Refusal{Rule: signer.RuleUnusable}) // a key that parses and cannot sign is an unusable document
	}
	tc, err := cert.Config(in.Endpoint)
	if err != nil {
		return discoveryBody{}, err
	}
	defer clear(tc)
	res, err := discover.DiscoverLimited(dctx, tc, "", in.Endpoint, maxDiscoveryMembers)
	if err == nil && res.ClusterID == "" {
		err = errors.New("the endpoint gave no cluster ID")
	}
	if err != nil {
		return discoveryBody{}, discoveryRefusal(err)
	}
	body := discoveryBody{ClusterID: res.ClusterID, Version: res.Version, Contract: res.Contract, Supported: res.Supported,
		Endpoint: res.Endpoint, KubernetesEndpoint: res.KubernetesEndpoint, Members: make([]discoveryMember, 0, len(res.Members)), Complete: !res.Cut}
	for _, m := range res.Members {
		body.Members = append(body.Members, discoveryMember{NodeID: m.NodeID, Hostname: m.Hostname, Address: m.Address, Role: m.Role,
			SMBIOSUUID: m.SMBIOSUUID, Platform: m.Platform, Answered: m.Answered})
	}
	return body, nil
}

// discoveryRefusal is the answer to a failed extraction or discovery. The signer's own rules are 422,
// as they are for a first key; a cluster with more members than the bound is 422 too. Anything else is
// 503 talos-access-unavailable, whether the endpoint did not answer, did not accept the key or did not
// give the cluster's identity: a refused key is not told apart from an unreachable endpoint. No answer
// quotes the document or a node's text.
func discoveryRefusal(err error) error {
	var many *discover.TooManyMembers
	var rf *signer.Refusal
	switch {
	case errors.As(err, &many):
		return refuse(http.StatusUnprocessableEntity, "validation-failed", "the cluster lists more members than a discovery reads; nothing was recorded").
			with("rule", "too-many-members").with("limit", many.Limit)
	case errors.As(err, &rf):
		return refuse(http.StatusUnprocessableEntity, "validation-failed", "the first key is refused; nothing was recorded").with("rule", rf.Rule)
	}
	return refuse(http.StatusServiceUnavailable, "talos-access-unavailable",
		"the endpoint did not answer, did not accept a certificate signed from the key, or did not give the cluster's identity; check the key and the endpoint; nothing was recorded")
}
