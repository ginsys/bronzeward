package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/talos"
)

// Inventory (POST /clusters, POST /machines) and draft creation are T11 (§5): the key lock, the
// installation state FOR SHARE (which lookup takes), the effect, then the act and the record.

func clusterCreation() effectRoute {
	return effectRoute{action: "cluster.create", input: func() input { return &clusterInput{} }, effect: createCluster}
}

func machineInventory() effectRoute {
	return effectRoute{action: "machine.inventory", input: func() input { return &machineInput{} }, effect: inventoryMachine}
}

func draftCreation() effectRoute {
	return effectRoute{action: "draft.create", input: func() input { return &draftInput{} }, effect: createDraft}
}

// effectRoute is what a mutating route adds to its §9.2 row.
type effectRoute struct {
	action string
	input  func() input
	effect effectFunc
}

func (er effectRoute) on(rt *route) *route {
	rt.action, rt.input, rt.effect = er.action, er.input, er.effect
	return rt
}

// text checks a free-text member: present, not blank, at most max bytes of UTF-8, no control
// characters. Its errors name the member, never the value (§9.4).
func text(member, v string, max int) error {
	switch {
	case strings.TrimSpace(v) == "":
		return fmt.Errorf("%s must be given and not blank", member)
	case len(v) > max || !utf8.ValidString(v) || strings.ContainsFunc(v, isControl):
		return fmt.Errorf("%s must be at most %d bytes of text without control characters", member, max)
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r < 0xa0) }

type clusterInput struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Contract string `json:"contract"`
}

var (
	contractShape = regexp.MustCompile(`^v[0-9]{1,4}\.[0-9]{1,4}$`)
	endpointShape = regexp.MustCompile(`^https://[^/?#@\s\x00-\x1f\x7f]+$`)
	// hostShape is a DNS name (an IPv4 address included): dot-separated labels of letters, digits
	// and inner hyphens, each at most 63 bytes.
	hostShape = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

func (in *clusterInput) check(*API) error {
	if err := text("name", in.Name, 128); err != nil {
		return err
	}
	if !validEndpoint(in.Endpoint) {
		return errors.New("endpoint must be https://<host>[:<port>], with no path, query, fragment or user information")
	}
	if !contractShape.MatchString(in.Contract) {
		return errors.New("contract must be a Talos contract minor, as v1.13")
	}
	return nil
}

// validEndpoint accepts the cluster's Kubernetes API endpoint as https://host[:port] only; the
// database's check is the same shape.
func validEndpoint(s string) bool {
	if len(s) > 512 || !endpointShape.MatchString(s) {
		return false
	}
	// url.Parse refuses a port that is not decimal. The authority itself is checked here: a DNS
	// name or a bracketed IPv6 address without a zone, then a port in range if there is a colon,
	// and nothing else, so that it rebuilds to exactly what was given.
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	h := u.Hostname()
	authority := h
	if strings.HasPrefix(u.Host, "[") {
		if !strings.Contains(h, ":") || net.ParseIP(h) == nil {
			return false
		}
		authority = "[" + h + "]"
	} else if len(h) > 253 || !hostShape.MatchString(h) {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
		authority += ":" + p
	}
	return u.Host == authority
}

type clusterBody struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Contract string `json:"contract"`
}

func createCluster(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*clusterInput)
	b := clusterBody{ID: id.New(id.Cluster), Name: in.Name, Endpoint: in.Endpoint, Contract: in.Contract}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cluster (id, name, endpoint, contract, created_at) VALUES ($1, $2, $3, $4, now())`,
		b.ID, b.Name, b.Endpoint, b.Contract); err != nil {
		return result{}, err
	}
	return result{status: http.StatusCreated, location: prefix + "/clusters/" + b.ID, body: b, subjects: []string{b.ID}}, nil
}

type machineInput struct {
	Cluster       string  `json:"cluster"`
	SMBIOSUUID    string  `json:"smbiosUuid"`
	Serial        *string `json:"serial"`
	TalosEndpoint string  `json:"talosEndpoint"`
}

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (in *machineInput) check(*API) error {
	if id.MustHave(in.Cluster, id.Cluster) != nil {
		return errors.New("cluster must be a cl identifier")
	}
	in.SMBIOSUUID = strings.ToLower(in.SMBIOSUUID)
	switch in.SMBIOSUUID {
	case "":
		return errors.New("smbiosUuid is required (persistence-api.md §7.3)")
	case "00000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff":
		return errors.New("smbiosUuid is SMBIOS's value for no UUID; such a machine cannot be inventoried (persistence-api.md §7.3)")
	}
	if !uuidShape.MatchString(in.SMBIOSUUID) {
		return errors.New("smbiosUuid must be a UUID in its 8-4-4-4-12 hexadecimal form")
	}
	if in.Serial != nil {
		if err := text("serial", *in.Serial, 128); err != nil {
			return err
		}
	}
	ep, err := talosEndpoint(in.TalosEndpoint)
	if err != nil {
		return err
	}
	in.TalosEndpoint = ep
	return nil
}

// talosEndpoint checks a machine's Talos endpoint and returns its stored form, host:port
// (persistence-api.md §3.3). The error never quotes the value.
func talosEndpoint(s string) (string, error) {
	if s == "" {
		return "", errors.New("talosEndpoint is required (persistence-api.md §3.3)")
	}
	ep, err := talos.ParseEndpoint(s)
	if err != nil {
		return "", errors.New("talosEndpoint must be an IP literal (an IPv6 literal in brackets) with an optional port from 1 to 65535")
	}
	return ep, nil
}

type hardware struct {
	SMBIOSUUID string  `json:"smbiosUuid"`
	Serial     *string `json:"serial"`
}

type applied struct {
	Release string `json:"release"`
	Source  string `json:"source"`
}

// machineBody is §9.3's machine resource. openDrift stays null until drift records exist.
type machineBody struct {
	ID            string    `json:"id"`
	Cluster       string    `json:"cluster"`
	Hardware      hardware  `json:"hardware"`
	TalosEndpoint string    `json:"talosEndpoint"`
	Desired       *string   `json:"desired"`
	Applied       *applied  `json:"applied"`
	Frozen        bool      `json:"frozen"`
	ScopeState    string    `json:"scopeState"`
	OpenDrift     *struct{} `json:"openDrift"`
}

// inventoryMachine records a machine with its MachineState. The SMBIOS UUID's unique index is §7.3's
// natural key: a concurrent request for the same UUID waits on this one's uncommitted row, and
// whichever inserts second finds the committed machine and is refused naming it. A machine
// inventoried in recovery mode starts pre-restore unaccounted (§12.2; execution and recovery §7.4),
// read from the installation state this transaction holds FOR SHARE.
func inventoryMachine(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*machineInput)
	if err := clusterExists(ctx, tx, in.Cluster); err != nil {
		return result{}, err
	}
	b := machineBody{ID: id.New(id.Machine), Cluster: in.Cluster, Hardware: hardware{SMBIOSUUID: in.SMBIOSUUID, Serial: in.Serial},
		TalosEndpoint: in.TalosEndpoint, ScopeState: "normal"}
	if q.recovery {
		b.ScopeState = "pre-restore-unaccounted"
	}
	// No conflict target: the SMBIOS UUID's index is the only one a fresh identifier can meet.
	var inserted string
	err := tx.QueryRowContext(ctx, `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now()) ON CONFLICT DO NOTHING RETURNING id`,
		b.ID, b.Cluster, b.Hardware.SMBIOSUUID, b.Hardware.Serial, b.ScopeState, b.TalosEndpoint).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM machine WHERE smbios_uuid = $1`, b.Hardware.SMBIOSUUID).Scan(&existing); err != nil {
			return result{}, err
		}
		return result{}, refuse(http.StatusConflict, "conflict", "a machine with this SMBIOS UUID is already inventoried").with("machine", existing)
	}
	if err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_state (machine) VALUES ($1)`, b.ID); err != nil {
		return result{}, err
	}
	return result{status: http.StatusCreated, location: prefix + "/machines/" + b.ID, body: b, subjects: []string{b.ID}}, nil
}

// clusterExists refuses a cluster the database does not hold. No cluster is ever deleted (§3),
// so the answer holds for the rest of the transaction.
func clusterExists(ctx context.Context, tx *sql.Tx, cluster string) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cluster WHERE id = $1)`, cluster).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return refuse(http.StatusNotFound, "not-found", "no such cluster").with("cluster", cluster)
	}
	return nil
}

type draftInput struct {
	Cluster string `json:"cluster"`
	Title   string `json:"title"`
}

func (in *draftInput) check(*API) error {
	if id.MustHave(in.Cluster, id.Cluster) != nil {
		return errors.New("cluster must be a cl identifier")
	}
	return text("title", in.Title, 256)
}

type draftEntry struct {
	Kind     string `json:"kind"`
	Machine  string `json:"machine"`
	Revision string `json:"revision"`
}

// draftBody is §9.3's draft resource, with its title.
type draftBody struct {
	ID       string       `json:"id"`
	Cluster  string       `json:"cluster"`
	Title    string       `json:"title"`
	State    string       `json:"state"`
	Revision int          `json:"revision"`
	Entries  []draftEntry `json:"entries"`
}

var tokenEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// etagToken is the random part of a strong ETag, replaced on every write (§4.1, choice §17.2).
func etagToken() string {
	var b [16]byte
	rand.Read(b[:]) // never returns an error (crypto/rand, Go 1.24+)
	return tokenEncoding.EncodeToString(b[:])
}

func etag(revision int, token string) string { return `"` + strconv.Itoa(revision) + "-" + token + `"` }

func createDraft(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*draftInput)
	if err := clusterExists(ctx, tx, in.Cluster); err != nil {
		return result{}, err
	}
	b := draftBody{ID: id.New(id.Draft), Cluster: in.Cluster, Title: in.Title, State: "open", Revision: 1, Entries: []draftEntry{}}
	token := etagToken()
	if _, err := tx.ExecContext(ctx, `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, $3, 'open', 1, $4, now())`, b.ID, b.Cluster, b.Title, token); err != nil {
		return result{}, err
	}
	return result{status: http.StatusCreated, location: prefix + "/drafts/" + b.ID, etag: etag(1, token), body: b, subjects: []string{b.ID}}, nil
}
