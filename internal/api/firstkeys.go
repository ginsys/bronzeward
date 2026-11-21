package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos/signer"
)

// POST /clusters/{id}/first-keys supplies or replaces a cluster's Talos CA key from a first key
// (persistence-api.md §3.4). It is a keyed route (§7.1): the document is HMACed into the
// fingerprint and never stored, logged, returned or quoted. Everything that touches Talos or the
// provider runs in prepare, before the transaction: the document is extracted, a certificate signed
// from the key is tried at the endpoint, and only then is the key written to the provider with
// check-and-set against the version read. The transaction records the act and its FirstKeyAct row.
// A refusal or a failed try writes nothing, and the stored key stays as it was.
func firstKeySupply() effectRoute {
	return effectRoute{action: "cluster.first-key", input: func() input { return &firstKeyInput{} },
		prepare: prepareFirstKey, effect: recordFirstKey, keyed: "document"}
}

type firstKeyInput struct {
	Kind     string           `json:"kind"`
	Document firstKeyDocument `json:"document"`
	Endpoint string           `json:"endpoint"`
}

func (in *firstKeyInput) check(*API) error {
	if err := in.checkKey(); err != nil {
		return err
	}
	ep, err := talosEndpoint(in.Endpoint)
	if err != nil {
		return errors.New("endpoint must be an IP literal (an IPv6 literal in brackets) with an optional port from 1 to 65535")
	}
	in.Endpoint = ep
	return nil
}

// checkKey is what a first key's input must hold whatever the endpoint's grammar: a known kind, a
// document and an endpoint.
func (in *firstKeyInput) checkKey() error {
	switch signer.Kind(in.Kind) {
	case signer.MachineConfig, signer.SecretsBundle, signer.Talosconfig:
	default:
		return errors.New("kind must be one of machine-config, secrets-bundle, talosconfig")
	}
	if in.Document.size() == 0 {
		return errors.New("document is required")
	}
	if in.Endpoint == "" {
		return errors.New("endpoint is required (persistence-api.md §3.4)")
	}
	return nil
}

// keyedDigest covers the document. FingerprintMarks length-prefixes each string it is given and
// HMACs the lot, which is all a single document needs; the document's own type keeps its bytes here.
func (in *firstKeyInput) keyedDigest(ctx context.Context, h ingest.HMAC, material []byte, version int) (provider.Digest, error) {
	return ingest.FingerprintMarks(ctx, h, material, []string{string(in.Document.bytes())}, version)
}

// firstKeyDocument is a first key as supplied: a machine configuration, a secrets bundle or a
// talosconfig. Every fmt verb prints a placeholder and the marshallers fail, and its text sits behind
// a pointer to a string for the reason ingest.Unresolved's does. Only this file reads it.
type firstKeyDocument struct{ s *string }

var errFirstKeyDocument = errors.New("the document must be a non-empty JSON string of valid UTF-8 within the size limit")

// UnmarshalJSON takes the body's document member under ingest.Unresolved's limits: one JSON string
// of at most ingest.MaxDocument decoded bytes, valid UTF-8, with no U+FFFD and no U+0000. Every
// refusal is one fixed error that quotes nothing.
func (d *firstKeyDocument) UnmarshalJSON(b []byte) error {
	*d = firstKeyDocument{}
	var s string
	if !utf8.Valid(b) || len(b) == 0 || b[0] != '"' || json.Unmarshal(b, &s) != nil {
		return errFirstKeyDocument
	}
	if len(s) == 0 || len(s) > ingest.MaxDocument || strings.ContainsRune(s, utf8.RuneError) || strings.ContainsRune(s, 0) {
		return errFirstKeyDocument
	}
	d.s = &s
	return nil
}

func (d firstKeyDocument) size() int {
	if d.s == nil {
		return 0
	}
	return len(*d.s)
}

func (d firstKeyDocument) bytes() []byte {
	if d.s == nil {
		return nil
	}
	return []byte(*d.s)
}

const firstKeyDocumentText = "[first key]"

var errFirstKeyRender = errors.New("a first key is not marshalled")

func (firstKeyDocument) String() string               { return firstKeyDocumentText }
func (firstKeyDocument) GoString() string             { return firstKeyDocumentText }
func (firstKeyDocument) Format(f fmt.State, _ rune)   { io.WriteString(f, firstKeyDocumentText) }
func (firstKeyDocument) MarshalJSON() ([]byte, error) { return nil, errFirstKeyRender }
func (firstKeyDocument) MarshalText() ([]byte, error) { return nil, errFirstKeyRender }

// storedKey is what prepare left for the transaction: the stored key's version identity, whether it
// replaced a key and the CA certificate's SHA-256 fingerprint. It holds no value.
type storedKey struct {
	version     provider.CAKeyVersion
	replaced    bool
	fingerprint string
}

// prepareFirstKey runs the whole supply but the act: cluster, signer, extraction, the try, the read
// of the key being replaced and the write against its version. The order puts every refusal ahead of
// the write.
func prepareFirstKey(ctx context.Context, a *API, q *request) error {
	in := q.input.(*firstKeyInput)
	cluster := q.r.PathValue("id")
	notFound := refuse(http.StatusNotFound, "not-found", "no such cluster")
	if id.MustHave(cluster, id.Cluster) != nil {
		return notFound
	}
	// The cluster's Talos cluster ID, stored canonical, is what the endpoint's own must equal.
	var talosClusterID string
	switch err := a.db.QueryRowContext(ctx, `SELECT talos_cluster_id FROM cluster WHERE id = $1`, cluster).Scan(&talosClusterID); {
	case errors.Is(err, sql.ErrNoRows):
		return notFound.with("cluster", cluster)
	case err != nil:
		return fmt.Errorf("%w: %w", errUnavailable, err)
	}
	if a.d.sg == nil {
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"no signer identity is configured (provider.signerTokenFile); nothing was committed")
	}
	// A talosconfig is read at the endpoint, which may accept the connection and stall: bound the
	// extraction like every node read (Try is bounded by its certificate's lifetime).
	ca, err := nodeRead(ctx, a.o.nodeTimeout, func(c context.Context) (provider.CAKey, error) {
		return signer.Extract(c, signer.Kind(in.Kind), in.Document.bytes(), in.Endpoint)
	})
	if err == nil {
		err = signer.Try(ctx, ca, in.Endpoint, talosClusterID, time.Now())
	}
	if err != nil {
		return firstKeyRefusal(err)
	}
	fp, ok := caFingerprint(ca)
	if !ok {
		// The signer signed from this certificate and key, so a certificate that is not PEM is
		// a defect here and not a refusal of the document.
		return errors.New("the extracted CA certificate is not PEM")
	}
	// A replacement is written against the version read, so two supplies racing for one cluster
	// cannot both win: the second finds the path moved and is refused (409), nothing written.
	prev, err := a.d.sg.ReadCAKey(ctx, cluster)
	cas, replaced := 0, false
	switch {
	case errors.Is(err, provider.ErrAbsent):
	case err != nil:
		return providerRefusal(err)
	default:
		cas, replaced = prev.Version().Version, true
	}
	v, err := a.d.sg.WriteCAKey(ctx, cluster, ca, cas)
	if err != nil {
		return writeRefusal(err)
	}
	q.stored = &storedKey{version: v, replaced: replaced, fingerprint: fp}
	return nil
}

// firstKeyRefusal is the answer to a failed extraction or try: the signer's own rules are 422, and a
// node that did not answer or did not accept the key is 503. Neither quotes the document.
func firstKeyRefusal(err error) error {
	var rf *signer.Refusal
	switch {
	case errors.As(err, &rf):
		return refuse(http.StatusUnprocessableEntity, "validation-failed", "the first key is refused; nothing was stored").with("rule", rf.Rule)
	case errors.Is(err, signer.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "talos-access-unavailable",
			"the endpoint did not answer, or did not accept a certificate signed from the key; check the key and the endpoint; nothing was stored")
	}
	return err
}

// providerRefusal is the answer to a failed read of the stored key, which changes nothing: an
// unavailable provider, a refused policy or an answer that is not the provider's is 503.
func providerRefusal(err error) error {
	switch {
	case errors.Is(err, provider.ErrUnavailable), errors.Is(err, provider.ErrDenied), errors.Is(err, provider.ErrProtocol), errors.Is(err, provider.ErrStatus):
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the provider could not read the CA key; nothing was stored")
	}
	return err
}

// writeRefusal is the answer to a failed write. Only a check-and-set refusal says the key was not
// written, so a moved path is 409 with nothing stored. Every other failure leaves the write
// unknown (the provider may have stored the version and lost or garbled its answer), so it is 503
// and says what a failed commit says: the key may be stored without its act.
func writeRefusal(err error) error {
	switch {
	case errors.Is(err, provider.ErrExists):
		return refuse(http.StatusConflict, "conflict", "the cluster's CA key changed while this first key was supplied; nothing was stored")
	case errors.Is(err, provider.ErrUnavailable), errors.Is(err, provider.ErrDenied), errors.Is(err, provider.ErrProtocol), errors.Is(err, provider.ErrStatus):
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the provider did not confirm the write; "+keyWithoutAct)
	}
	return err
}

const keyWithoutAct = "the CA key may have been stored without its act; it is not used until a supply is recorded; read GET /clusters/{id}/talos-access and supply again"

// keyStoredWithoutAct is the problem for a first key whose transaction failed after prepare stored
// the key. It keeps ref's status, code and members; ref's own detail stays only where the request
// itself was refused (an epoch mismatch), and otherwise gives way to the true account.
func keyStoredWithoutAct(ref *refusal, refused bool) *refusal {
	r := *ref
	switch {
	case refused && r.detail != "":
		r.detail += "; " + keyWithoutAct
	default:
		r.detail = keyWithoutAct
	}
	return &r
}

// caFingerprint is the SHA-256 of the DER of the CA certificate's first PEM block, in lower-case hex.
func caFingerprint(k provider.CAKey) (string, bool) {
	b, _ := pem.Decode(k.Crt())
	if b == nil || b.Type != "CERTIFICATE" {
		return "", false
	}
	sum := sha256.Sum256(b.Bytes)
	return hex.EncodeToString(sum[:]), true
}

// firstKeyBody is the answer (§3.4): the stored key's version identity and whether it replaced one.
// The path is the cluster's and is not told; the document, the certificate and the key are never in it.
type firstKeyBody struct {
	Cluster      string       `json:"cluster"`
	Kind         string       `json:"kind"`
	CAKeyVersion caKeyVersion `json:"caKeyVersion"`
	Replaced     bool         `json:"replaced"`
}

type caKeyVersion struct {
	Version     int       `json:"version"`
	CreatedTime time.Time `json:"createdTime"`
}

// recordFirstKey writes the FirstKeyAct row for the supply prepare stored. The act is the
// transaction's own; no machine or cluster row changes.
func recordFirstKey(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*firstKeyInput)
	cluster := q.r.PathValue("id")
	s := q.stored
	if s == nil {
		return result{}, errors.New("a first key reached its transaction with nothing stored")
	}
	created := s.version.CreatedTime.UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO first_key_act (act, cluster, kind, replaced, ca_fingerprint, key_path, key_version, key_created_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		q.actID, cluster, in.Kind, s.replaced, s.fingerprint, s.version.Path, s.version.Version, created); err != nil {
		return result{}, err
	}
	return result{status: http.StatusOK, subjects: []string{cluster}, body: firstKeyBody{Cluster: cluster, Kind: in.Kind,
		CAKeyVersion: caKeyVersion{Version: s.version.Version, CreatedTime: created}, Replaced: s.replaced}}, nil
}
