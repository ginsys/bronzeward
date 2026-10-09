package ingest

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/ginsys/bronzeward/internal/provider"
)

// HMAC is the provider's keyed digest (compilation.md §4.1): HMAC-SHA-256 under the digest key at
// version, or the latest when version is 0. (*provider.Ingestion).Digest is one.
type HMAC func(ctx context.Context, input []byte, version int) (provider.Digest, error)

// Fingerprint is persistence-api.md §7.1's fingerprint of a request whose body carries
// unextracted input: HMAC over material, the caller's length-prefixed encoding of the request
// without its document, followed by the document, itself length-prefixed. It is computed here so
// that no other package holds the document's bytes. version is the recorded key version for a
// retry, or 0 for a new request.
func Fingerprint(ctx context.Context, h HMAC, material []byte, u Unresolved, version int) (provider.Digest, error) {
	doc := u.bytes()
	if len(doc) == 0 {
		return provider.Digest{}, ErrEmptyInput
	}
	return fingerprint(ctx, h, material, doc, version)
}

// FingerprintRequest is the fingerprint of a request on a keyed route whose body carries no
// document (a source machine ingestion): HMAC over material followed by a zero length, which no
// request with a document, never empty, encodes.
func FingerprintRequest(ctx context.Context, h HMAC, material []byte, version int) (provider.Digest, error) {
	return fingerprint(ctx, h, material, nil, version)
}

// FingerprintMarks is the fingerprint of a request on a keyed route whose marks are its
// unextracted input (a mark on a paused ingestion, compilation §3.6): a mark path can spell an
// extracted value, so the marks are HMACed, each length-prefixed, in place of a document.
func FingerprintMarks(ctx context.Context, h HMAC, material []byte, marks []string, version int) (provider.Digest, error) {
	if len(marks) == 0 {
		return provider.Digest{}, ErrEmptyInput
	}
	var enc []byte
	for _, m := range marks {
		enc = binary.BigEndian.AppendUint64(enc, uint64(len(m)))
		enc = append(enc, m...)
	}
	defer clear(enc)
	return fingerprint(ctx, h, material, enc, version)
}

func fingerprint(ctx context.Context, h HMAC, material, doc []byte, version int) (provider.Digest, error) {
	in := make([]byte, 0, len(material)+8+len(doc))
	in = append(in, material...)
	in = binary.BigEndian.AppendUint64(in, uint64(len(doc)))
	in = append(in, doc...)
	d, err := h(ctx, in, version)
	clear(in)
	if err != nil {
		return provider.Digest{}, errors.Join(errors.New("ingest: the request fingerprint could not be computed"), err)
	}
	return d, nil
}
