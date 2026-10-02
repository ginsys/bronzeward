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
