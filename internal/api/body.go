package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/gowebpki/jcs"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// input is a mutating route's body, decoded strictly (§9.1) and checked before any transaction.
type input interface{ check(a *API) error }

// documentInput is a keyed route's input: its document is the unextracted input the digest key's
// HMAC covers in place of the canonical body's member (§7.1).
type documentInput interface {
	input
	document() ingest.Unresolved
}

const maxBody = 1 << 20

var errNotObject = errors.New("the body must be one JSON object of this route's members")

// decodeBody reads r's body into in and returns its RFC 8785 canonical form (§7.1). It refuses a
// content type other than JSON, a body over maxBody, a member that is unknown, repeated or
// differently cased (encoding/json would accept the last two), and trailing data. Its errors name
// no value (§9.4). A non-empty omit is a keyed route's document member (§7.1): it is decoded into
// in but left out of the canonical form. A noBody route takes no body at all, and no content type.
func decodeBody(r *http.Request, in input, omit string) ([]byte, error) {
	if _, ok := in.(*noBody); ok {
		var one [1]byte
		if n, _ := io.ReadFull(r.Body, one[:]); n != 0 {
			return nil, errors.New("this route takes no body")
		}
		return []byte{}, nil
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return nil, errors.New("Content-Type must be application/json")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, errors.New("the body could not be read")
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("the body is larger than %d bytes", maxBody)
	}
	if err := exactMembers(b, in); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(in); err != nil {
		return nil, errNotObject
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errNotObject
	}
	// The document is dropped before anything here decodes a value: it stays raw bytes, read
	// only by its own type (compilation §2.1), which also refuses U+0000 in it.
	rest := b
	if omit != "" {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(b, &members); err != nil {
			return nil, errNotObject
		}
		clear(members[omit])
		delete(members, omit)
		if rest, err = json.Marshal(members); err != nil {
			return nil, errNotObject
		}
		clear(b)
	}
	var v any
	if err := json.Unmarshal(rest, &v); err != nil {
		return nil, errNotObject
	}
	if holdsNUL(v) {
		return nil, errors.New("a string holds U+0000, which cannot be stored")
	}
	canon, err := jcs.Transform(rest)
	if err != nil {
		return nil, errNotObject
	}
	return canon, nil
}

// holdsNUL reports a string, or a member name, anywhere in v that holds U+0000: PostgreSQL text
// cannot store it, and the request would fail inside its transaction instead of here.
func holdsNUL(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.ContainsRune(v, 0)
	case []any:
		return slices.ContainsFunc(v, holdsNUL)
	case map[string]any:
		for k, e := range v {
			if strings.ContainsRune(k, 0) || holdsNUL(e) {
				return true
			}
		}
	}
	return false
}

// exactMembers refuses a top-level member whose name is not exactly one of in's JSON field
// names, or that repeats.
func exactMembers(b []byte, in input) error {
	allowed := map[string]bool{}
	t := reflect.TypeOf(in).Elem()
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			allowed[name] = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errNotObject
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return errNotObject
		}
		name, _ := tok.(string)
		switch {
		case !allowed[name]:
			return fmt.Errorf("unknown member %q", name)
		case seen[name]:
			return fmt.Errorf("member %q given twice", name)
		}
		seen[name] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return errNotObject
		}
	}
	return nil
}

var wildcard = regexp.MustCompile(`\{([a-z]+)\}`)

// fingerprint is §7.1's: SHA-256 over material.
func fingerprint(q *request, canon []byte) []byte {
	sum := sha256.Sum256(material(q, canon))
	return sum[:]
}

// material is what §7.1's fingerprint covers: the method, the route template, its path
// parameters, If-Match and the canonical body, each length-prefixed so that no two requests share
// an encoding. A keyed route's HMAC covers it followed by the document.
func material(q *request, canon []byte) []byte {
	var b []byte
	put := func(s string) {
		b = binary.BigEndian.AppendUint64(b, uint64(len(s)))
		b = append(b, s...)
	}
	put(q.route.method)
	put(q.route.pattern)
	for _, m := range wildcard.FindAllStringSubmatch(q.route.pattern, -1) {
		put(m[1])
		put(q.r.PathValue(m[1]))
	}
	put(q.ifMatch)
	put(string(canon))
	return b
}
