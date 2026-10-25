package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding"
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
	"github.com/ginsys/bronzeward/internal/provider"
)

// input is a mutating route's body, decoded strictly (§9.1) and checked before any transaction.
type input interface{ check(a *API) error }

// keyedInput is a keyed route's input: keyedDigest is the digest key's HMAC over material
// followed by the unextracted input its keyed member carries, in place of that member in the
// canonical body (§7.1).
type keyedInput interface {
	input
	keyedDigest(ctx context.Context, h ingest.HMAC, material []byte, version int) (provider.Digest, error)
}

const maxBody = 1 << 20

var errNotObject = errors.New("the body must be one JSON object of this route's members")

// decodeBody reads r's body into in and returns its RFC 8785 canonical form (§7.1). It refuses a
// content type other than JSON, a body over maxBody, a member at any level that is unknown,
// repeated or differently cased (encoding/json would accept the last two), and trailing data. Its errors name
// no value (§9.4). A non-empty omit is a keyed route's document member (§7.1): it is decoded into
// in but left out of the canonical form. A noBody route takes no body at all, and no content type.
func decodeBody(r *http.Request, in input, omit string) ([]byte, error) {
	if _, ok := in.(*noBody); ok {
		var one [1]byte
		n, err := io.ReadFull(r.Body, one[:])
		if n != 0 {
			return nil, errors.New("this route takes no body")
		}
		// Only a clean end is an empty body; a failed read must not commit a removal.
		if !errors.Is(err, io.EOF) {
			return nil, errors.New("the body could not be read")
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

// exactMembers refuses a member, at any level of the body, whose name is not exactly one of its
// object's JSON field names, or that repeats; a repeated key of a map member is refused too. It
// follows in's Go type: a value whose type decodes itself (json.Unmarshaler or
// encoding.TextUnmarshaler, as the document member does) is left to that type, and so is a value
// of an interface type, which takes any member. A value of another shape than its type is left to
// the decoder, which refuses it. The field names it matches are fieldTypes', for the field shapes
// request types use.
func exactMembers(b []byte, in input) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errNotObject
	}
	return exactValue(b, reflect.TypeOf(in).Elem(), "")
}

var (
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// exactValue is exactMembers for the value b of type t at path, the member names leading to it ("" at
// the top level, so that a top-level refusal names the member alone).
func exactValue(b []byte, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if pt := reflect.PointerTo(t); pt.Implements(jsonUnmarshaler) || pt.Implements(textUnmarshaler) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		return exactObject(b, path, fieldTypes(t), nil)
	case reflect.Map:
		return exactObject(b, path, nil, t.Elem())
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil // a []byte is one base64 string
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
			return nil
		}
		for i := 0; dec.More(); i++ {
			var e json.RawMessage
			if err := dec.Decode(&e); err != nil {
				return errNotObject
			}
			if err := exactValue(e, t.Elem(), fmt.Sprintf("%s[%d].", strings.TrimSuffix(path, "."), i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// exactObject walks the object b: a struct's, whose members must be exactly fields' names, or a
// map's (fields nil), whose values are of type elem. It refuses a repeated member and checks each
// member's value against its type. No error names a map's key or a nested unknown member, which
// are the client's text, not field names. A b that is not an object is left to the decoder.
func exactObject(b []byte, path string, fields map[string]reflect.Type, elem reflect.Type) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return errNotObject
		}
		name, _ := tok.(string)
		ft, at := elem, path+"*."
		if fields != nil {
			var ok bool
			if ft, ok = fields[name]; !ok {
				// Below the top level a name can spell a path into a document, so only the
				// containing object is named (client contracts' protected inputs).
				if path == "" {
					return fmt.Errorf("unknown member %q", name)
				}
				return fmt.Errorf("an unknown or differently cased member in %q", strings.TrimSuffix(path, "."))
			}
			at = path + name + "."
		}
		if seen[name] {
			if fields == nil {
				return fmt.Errorf("a key of member %q given twice", strings.TrimSuffix(path, "."))
			}
			return fmt.Errorf("member %q given twice", path+name)
		}
		seen[name] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return errNotObject
		}
		if err := exactValue(v, ft, at); err != nil {
			return err
		}
	}
	return nil
}

// fieldTypes maps the JSON names encoding/json decodes into t to their field types, for the field
// shapes request types use: an exported field's tag name, or its Go name when the tag names none,
// and the fields of an untagged embedded struct as its own. A field tagged "-" is not decoded. It
// does not resolve conflicting embedded names or invalid tag names as encoding/json does, and the
// walk takes a map's keys as strings: no request type has such a name or a non-string map key.
func fieldTypes(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				for n, et := range fieldTypes(ft) {
					if _, own := fields[n]; !own {
						fields[n] = et
					}
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f.Type
	}
	return fields
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
