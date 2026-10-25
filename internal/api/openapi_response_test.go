package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// checkResponse fails t when rec, the answer to r, is not one the description lists for r's
// operation: an undescribed status, a header the status does not describe or one it requires
// and the answer lacks, a media type the status does not list, or a JSON body its schema refuses. A request the server accepted must also fit the operation's request schema, so
// the description requires no member the server does without. env.do calls it for every request
// a test sends through it, so the API tests' own exchanges are what the description's schemas are
// checked against. A path outside prefix, or one no described path matches, is a test's own route
// and is not checked. It reports with Errorf: env.do runs in goroutines too.
func checkResponse(t *testing.T, r *http.Request, reqBody string, rec *httptest.ResponseRecorder) {
	t.Helper()
	doc, err := loadDescription()
	if err != nil {
		t.Error(err)
		return
	}
	for _, e := range exchangeErrors(doc, r, reqBody, rec) {
		t.Error(e)
	}
}

// exchangeErrors is checkResponse's findings against doc.
func exchangeErrors(doc map[string]any, r *http.Request, reqBody string, rec *httptest.ResponseRecorder) []string {
	path, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		return nil
	}
	tmpl := matchPath(doc, path)
	if tmpl == "" {
		return nil
	}
	where := r.Method + " " + tmpl + " " + fmt.Sprint(rec.Code)
	item, _ := doc["paths"].(map[string]any)[tmpl].(map[string]any)
	method := strings.ToLower(r.Method)
	if _, ok := item[method]; !ok && r.Method == http.MethodHead {
		method = "get" // http.ServeMux serves HEAD through every GET pattern; the answer carries no body
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		// The router answers a method the path does not serve; that answer is a problem.
		op = map[string]any{"responses": map[string]any{"default": map[string]any{"$ref": "#/components/responses/Problem"}}}
	}
	var errs []string
	if rec.Code < 300 {
		errs = append(paramErrors(doc, op, r, where), requestErrors(doc, op, reqBody, where)...)
	}
	responses, _ := op["responses"].(map[string]any)
	resp, ok := responses[fmt.Sprint(rec.Code)]
	if !ok {
		if resp, ok = responses["default"]; !ok || rec.Code < 400 {
			return append(errs, where+": the description lists no such status")
		}
	}
	described, _ := deref(doc, resp).(map[string]any)
	// The headers the client received: those set when the answer was written, not later.
	sent := rec.Result().Header
	errs = append(errs, headerErrors(doc, described, sent, where)...)
	content, _ := described["content"].(map[string]any)
	if len(content) == 0 {
		if rec.Body.Len() != 0 && r.Method != http.MethodHead {
			errs = append(errs, fmt.Sprintf("%s: the description lists no body, the answer has %d bytes", where, rec.Body.Len()))
		}
		return errs
	}
	mt, _, err := mime.ParseMediaType(sent.Get("Content-Type"))
	if err != nil {
		return append(errs, fmt.Sprintf("%s: Content-Type %q: %v", where, sent.Get("Content-Type"), err))
	}
	media, ok := content[mt].(map[string]any)
	if !ok {
		return append(errs, fmt.Sprintf("%s: the description lists no %s body", where, mt))
	}
	if mt == "text/event-stream" || r.Method == http.MethodHead { // a HEAD answer's body is not sent
		return errs
	}
	body, err := decodeJSON(rec.Body.Bytes())
	if err != nil {
		return append(errs, fmt.Sprintf("%s: the body is not JSON: %v", where, err))
	}
	for _, e := range validate(doc, media["schema"], body, "body") {
		errs = append(errs, where+": "+e)
	}
	return errs
}

// contractHeaders are the answer headers persistence and API §9 defines. An answer carrying one
// must find it described for its status; headerErrors also checks every described one.
var contractHeaders = []string{"Bronzeward-Epoch", "Bronzeward-Recovery-Mode", "Bronzeward-Request-ID", "Cache-Control", "ETag",
	"Idempotent-Replayed", "Location", "WWW-Authenticate"}

// headerErrors reports a contract header h carries that resp does not describe, a header resp
// requires that h lacks, and a header value resp's schema for it refuses.
func headerErrors(doc, resp map[string]any, h http.Header, where string) []string {
	described := map[string]map[string]any{}
	hs, _ := resp["headers"].(map[string]any)
	for name, v := range hs {
		d, _ := deref(doc, v).(map[string]any)
		described[http.CanonicalHeaderKey(name)] = d
	}
	names := slices.Collect(maps.Keys(described))
	for _, name := range contractHeaders {
		names = append(names, http.CanonicalHeaderKey(name))
	}
	slices.Sort(names)
	var errs []string
	for _, name := range slices.Compact(names) {
		d, ok := described[name]
		values := h.Values(name)
		switch {
		case !ok && len(values) > 0:
			errs = append(errs, fmt.Sprintf("%s: header %s is not described", where, name))
		case ok && len(values) == 0 && d["required"] == true:
			errs = append(errs, fmt.Sprintf("%s: required header %s is missing", where, name))
		case ok:
			for _, v := range values {
				for _, e := range validate(doc, d["schema"], v, "header "+name) {
					errs = append(errs, where+": "+e)
				}
			}
		}
	}
	return errs
}

// requestErrors reports how reqBody, a body the server accepted, does not fit op's request
// schema, or that op describes no body and reqBody has one.
func requestErrors(doc, op map[string]any, reqBody, where string) []string {
	rb, _ := deref(doc, op["requestBody"]).(map[string]any)
	content, _ := rb["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		if reqBody != "" {
			return []string{where + ": the description lists no request body, the server accepted one"}
		}
		return nil
	}
	v, err := decodeJSON([]byte(reqBody))
	if err != nil {
		return []string{fmt.Sprintf("%s: the accepted request body is not JSON: %v", where, err)}
	}
	var errs []string
	for _, e := range validate(doc, media["schema"], v, "request") {
		errs = append(errs, where+": "+e)
	}
	return errs
}

// paramErrors reports a query parameter of r, a request the server accepted, that op does not
// describe, and a query or header value op's schema for it refuses. An undescribed request header
// is not reported: the route walk sends Idempotency-Key and If-Match everywhere and a read ignores
// them; routeFacts holds each route's described headers to the table.
func paramErrors(doc, op map[string]any, r *http.Request, where string) []string {
	described := map[string]map[string]any{}
	ps, _ := op["parameters"].([]any)
	for _, p := range ps {
		d, _ := deref(doc, p).(map[string]any)
		name, _ := d["name"].(string)
		if in := d["in"]; in == "header" {
			described["header "+http.CanonicalHeaderKey(name)] = d
		} else if in == "query" {
			described["query "+name] = d
		}
	}
	var errs []string
	for _, name := range slices.Sorted(maps.Keys(r.URL.Query())) {
		if _, ok := described["query "+name]; !ok {
			errs = append(errs, fmt.Sprintf("%s: query parameter %s is not described", where, name))
		}
	}
	for _, at := range slices.Sorted(maps.Keys(described)) {
		in, name, _ := strings.Cut(at, " ")
		values := r.Header.Values(name)
		if in == "query" {
			values = r.URL.Query()[name]
		}
		schema := described[at]["schema"] // validated as written: a $ref's siblings apply too
		resolved, _ := deref(doc, schema).(map[string]any)
		for _, s := range values {
			var v any = s
			if _, err := strconv.ParseInt(s, 10, 64); err == nil && resolved["type"] == "integer" {
				v = json.Number(s)
			}
			for _, e := range validate(doc, schema, v, at) {
				errs = append(errs, where+": "+e)
			}
		}
	}
	return errs
}

// decodeJSON decodes b as exactly one JSON value, refusing anything after it.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data follows the JSON value")
	}
	return v, nil
}

// matchPath returns the described path template that path matches, preferring the one with the
// most literal segments, or "" when none does.
func matchPath(doc map[string]any, path string) string {
	segs := strings.Split(path, "/")
	best, bestLiterals := "", -1
	for tmpl := range doc["paths"].(map[string]any) {
		ts := strings.Split(tmpl, "/")
		if len(ts) != len(segs) {
			continue
		}
		literals := 0
		for i, s := range ts {
			if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && segs[i] != "" {
				continue
			}
			if s != segs[i] {
				literals = -1
				break
			}
			literals++
		}
		if literals > bestLiterals {
			best, bestLiterals = tmpl, literals
		}
	}
	return best
}

// deref follows local "#/..." references until the value is not one.
func deref(doc map[string]any, v any) any {
	for {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v
		}
		v = pointer(doc, ref)
	}
}

func pointer(doc map[string]any, ref string) any {
	var v any = doc
	for _, tok := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		m, _ := v.(map[string]any)
		v = m[tok]
	}
	return v
}

// The schema keywords validate understands. Every other keyword in the description fails
// TestDescriptionUsesSupportedKeywords, so a schema never relies on one validate ignores.
var (
	assertionKeywords = []string{"$ref", "type", "properties", "required", "additionalProperties", "items", "enum", "const", "oneOf",
		"minLength", "maxLength", "pattern", "minimum", "maximum", "minItems", "maxItems"}
	annotationKeywords = []string{"description", "format", "examples", "deprecated"}
)

// validate returns every way v breaks schema, a JSON Schema in the subset above; at names v.
func validate(doc map[string]any, schema, v any, at string) []string {
	s, ok := schema.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: the schema is %T, not an object", at, schema)}
	}
	// A $ref's siblings still apply in OpenAPI 3.1 (JSON Schema 2020-12): both must accept v.
	var errs []string
	if ref, ok := s["$ref"].(string); ok {
		errs = validate(doc, pointer(doc, ref), v, at)
	}
	if types, ok := s["type"]; ok && !hasType(types, v) {
		return append(errs, fmt.Sprintf("%s: %s is not of type %v", at, shortJSON(v), types))
	}
	if enum, ok := s["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return sameValue(e, v) }) {
		errs = append(errs, fmt.Sprintf("%s: %s is not one of %v", at, shortJSON(v), enum))
	}
	if c, ok := s["const"]; ok && !sameValue(c, v) {
		errs = append(errs, fmt.Sprintf("%s: %s is not %v", at, shortJSON(v), c))
	}
	if one, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, alt := range one {
			if len(validate(doc, alt, v, at)) == 0 {
				n++
			}
		}
		if n != 1 {
			errs = append(errs, fmt.Sprintf("%s: %s matches %d of oneOf's %d schemas", at, shortJSON(v), n, len(one)))
		}
	}
	outside := func(n float64, low, high string) {
		if b, ok := bound(s, low); ok && n < b {
			errs = append(errs, fmt.Sprintf("%s: %s is below %s %v", at, shortJSON(v), low, b))
		}
		if b, ok := bound(s, high); ok && n > b {
			errs = append(errs, fmt.Sprintf("%s: %s is above %s %v", at, shortJSON(v), high, b))
		}
	}
	switch v := v.(type) {
	case string:
		outside(float64(utf8.RuneCountInString(v)), "minLength", "maxLength")
		if p, ok := s["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err != nil {
				errs = append(errs, fmt.Sprintf("%s: pattern %q does not compile: %v", at, p, err))
			} else if !re.MatchString(v) {
				errs = append(errs, fmt.Sprintf("%s: %s does not match %s", at, shortJSON(v), p))
			}
		}
	case json.Number:
		n, _ := v.Float64()
		outside(n, "minimum", "maximum")
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]any)
		for _, req := range required {
			if _, ok := v[req.(string)]; !ok {
				errs = append(errs, fmt.Sprintf("%s: member %q is missing", at, req))
			}
		}
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if ps, ok := props[k]; ok {
				errs = append(errs, validate(doc, ps, v[k], at+"."+k)...)
				continue
			}
			switch extra := s["additionalProperties"].(type) {
			case bool:
				if !extra {
					errs = append(errs, fmt.Sprintf("%s: member %q is not described", at, k))
				}
			case map[string]any:
				errs = append(errs, validate(doc, extra, v[k], at+"."+k)...)
			}
			// Absent, it admits any member, as in JSON Schema; a schema that lists properties or
			// says type object must state it (TestDescriptionUsesSupportedKeywords).
		}
	case []any:
		outside(float64(len(v)), "minItems", "maxItems")
		if items, ok := s["items"]; ok {
			for i, e := range v {
				errs = append(errs, validate(doc, items, e, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return errs
}

// bound is schema's numeric keyword key, as YAML decoded it.
func bound(s map[string]any, key string) (float64, bool) {
	switch b := s[key].(type) {
	case int:
		return float64(b), true
	case float64:
		return b, true
	}
	return 0, false
}

func hasType(types, v any) bool {
	list, ok := types.([]any)
	if !ok {
		list = []any{types}
	}
	for _, t := range list {
		switch t {
		case "null":
			if v == nil {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "number":
			if _, ok := v.(json.Number); ok {
				return true
			}
		case "integer":
			if n, ok := v.(json.Number); ok {
				if _, err := n.Int64(); err == nil {
					return true
				}
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		}
	}
	return false
}

// sameValue compares a schema value, as YAML decoded it, with a JSON value decoded with
// UseNumber. A JSON number equals only a numeric schema value of the same magnitude.
func sameValue(schema, v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return reflect.DeepEqual(schema, v)
	}
	switch schema.(type) {
	case int, int64, uint64, float64:
	default:
		return false
	}
	a, okA := new(big.Rat).SetString(fmt.Sprint(schema))
	b, okB := new(big.Rat).SetString(n.String())
	return okA && okB && a.Cmp(b) == 0
}

func shortJSON(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 80 {
		return string(b[:80]) + "…"
	}
	return string(b)
}

// Every schema in the description uses only the keywords validate understands, every local
// reference resolves, and every object schema says whether it admits members it does not list.
func TestDescriptionUsesSupportedKeywords(t *testing.T) {
	t.Parallel()
	doc := description(t)
	known := append(slices.Clone(assertionKeywords), annotationKeywords...)
	var walk func(v any, at string, schema bool)
	walk = func(v any, at string, schema bool) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && pointer(doc, ref) == nil {
				t.Errorf("%s: $ref %s does not resolve", at, ref)
			}
			if !schema {
				for _, k := range slices.Sorted(maps.Keys(v)) {
					walk(v[k], at+"/"+k, k == "schema")
				}
				return
			}
			for _, k := range slices.Sorted(maps.Keys(v)) {
				if !slices.Contains(known, k) {
					t.Errorf("%s: schema keyword %q is not one validate understands", at, k)
				}
			}
			if _, ok := v["properties"]; ok || hasType(v["type"], map[string]any{}) {
				if _, ok := v["additionalProperties"]; !ok {
					t.Errorf("%s: an object schema without additionalProperties", at)
				}
			}
			for k, sub := range v {
				switch k {
				case "properties":
					for name, p := range sub.(map[string]any) {
						walk(p, at+"/properties/"+name, true)
					}
				case "items", "additionalProperties":
					if _, ok := sub.(map[string]any); ok {
						walk(sub, at+"/"+k, true)
					}
				case "oneOf":
					for i, alt := range sub.([]any) {
						walk(alt, fmt.Sprintf("%s/oneOf/%d", at, i), true)
					}
				}
			}
		case []any:
			for i, e := range v {
				walk(e, fmt.Sprintf("%s/%d", at, i), false)
			}
		}
	}
	walk(doc["paths"], "#/paths", false)
	components, _ := doc["components"].(map[string]any)
	for name, s := range components["schemas"].(map[string]any) {
		walk(s, "#/components/schemas/"+name, true)
	}
	for _, k := range []string{"responses", "parameters", "headers"} {
		walk(components[k], "#/components/"+k, false)
	}
}

// The problem types the description lists are the codes problem.go answers with a title, so a
// new code fails here, not only when some API test happens to return it.
func TestDescriptionProblemTypesMatchTitles(t *testing.T) {
	t.Parallel()
	enum, _ := pointer(description(t), "#/components/schemas/Problem/properties/type/enum").([]any)
	var described []string
	for _, v := range enum {
		s, _ := v.(string)
		described = append(described, s)
	}
	var served []string
	for code := range titles {
		served = append(served, "urn:bronzeward:problem:"+code)
	}
	slices.Sort(described)
	slices.Sort(served)
	if !slices.Equal(described, served) {
		t.Errorf("the description lists %v, problem.go answers %v", described, served)
	}
}

// Every component the description defines is reached from its paths, so none describes what no
// answer or request uses: a component only an unreached one refers to is unreached too.
func TestDescriptionComponentsAreReferenced(t *testing.T) {
	t.Parallel()
	doc := description(t)
	refs := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && !refs[ref] {
				refs[ref] = true
				walk(pointer(doc, ref))
			}
			for _, sub := range v {
				walk(sub)
			}
		case []any:
			for _, sub := range v {
				walk(sub)
			}
		}
	}
	walk(doc["paths"])
	components, _ := doc["components"].(map[string]any)
	for _, kind := range slices.Sorted(maps.Keys(components)) {
		if kind == "securitySchemes" { // named by security requirements, not by $ref
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(components[kind].(map[string]any))) {
			if !refs["#/components/"+kind+"/"+name] {
				t.Errorf("components/%s/%s is reached from no path", kind, name)
			}
		}
	}
}

// The validator's negative controls: each assertion keyword refuses the value it should.
func TestValidateRefusesEachKind(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"Item": map[string]any{"type": "object", "required": []any{"id"}, "additionalProperties": false,
			"properties": map[string]any{"id": map[string]any{"type": "string"}, "n": map[string]any{"type": "integer"}}},
		"Name": map[string]any{"type": "string"},
	}}}
	ref := map[string]any{"$ref": "#/components/schemas/Item"}
	num := func(s string) any { return json.Number(s) }
	for _, tc := range []struct {
		name   string
		schema map[string]any
		v      any
	}{
		{"a wrong type", map[string]any{"type": "string"}, num("1")},
		{"null where not allowed", map[string]any{"type": "string"}, nil},
		{"a fraction for an integer", map[string]any{"type": "integer"}, num("1.5")},
		{"a missing required member", ref, map[string]any{"n": num("1")}},
		{"an undescribed member", ref, map[string]any{"id": "a", "x": true}},
		{"a wrong member type", ref, map[string]any{"id": "a", "n": "1"}},
		{"a wrong item", map[string]any{"type": "array", "items": ref}, []any{map[string]any{}}},
		{"a value outside enum", map[string]any{"enum": []any{"a", "b"}}, "c"},
		{"a value other than const", map[string]any{"const": "a"}, "b"},
		{"no oneOf alternative", map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "boolean"}}}, num("1")},
		{"two oneOf alternatives", map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"enum": []any{"a"}}}}, "a"},
		{"a string below minLength", map[string]any{"minLength": 2}, "é"},
		{"a string above maxLength", map[string]any{"maxLength": 1}, "ab"},
		{"a string outside its pattern", map[string]any{"pattern": "^[a-z]+$"}, "aB"},
		{"a number below minimum", map[string]any{"minimum": 1}, num("0")},
		{"a number above maximum", map[string]any{"maximum": 604800}, num("604801")},
		{"a fractional bound", map[string]any{"maximum": 1.5}, num("2")},
		{"an array below minItems", map[string]any{"minItems": 1}, []any{}},
		{"an array above maxItems", map[string]any{"maxItems": 1}, []any{"a", "b"}},
		{"an additional member the schema refuses", map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			map[string]any{"a": num("1")}},
		{"a value a $ref's sibling refuses", map[string]any{"$ref": "#/components/schemas/Name", "enum": []any{"a"}}, "b"},
		{"a value the $ref refuses and its sibling admits", map[string]any{"$ref": "#/components/schemas/Name", "enum": []any{"a", 1}}, num("1")},
		{"a number against a string enum value", map[string]any{"enum": []any{"1"}}, num("1")},
		{"a number against a string const", map[string]any{"const": "1"}, num("1")},
		{"a number other than the enum value", map[string]any{"enum": []any{1}}, num("2")},
	} {
		if errs := validate(doc, tc.schema, tc.v, "v"); len(errs) == 0 {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	if errs := validate(doc, ref, map[string]any{"id": "a", "n": num("2")}, "v"); len(errs) != 0 {
		t.Errorf("a valid value refused: %v", errs)
	}
	for _, tc := range []struct {
		name   string
		schema map[string]any
		v      any
	}{
		{"null where allowed", map[string]any{"type": []any{"string", "null"}}, nil},
		{"a string at its bounds", map[string]any{"minLength": 1, "maxLength": 1}, "é"},
		{"a string matching its pattern", map[string]any{"pattern": "^[a-z]+$"}, "ab"},
		{"a number at its bounds", map[string]any{"minimum": 1, "maximum": 604800}, num("604800")},
		{"an array at its bounds", map[string]any{"minItems": 1, "maxItems": 1}, []any{"a"}},
		{"an additional member the schema admits", map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			map[string]any{"a": "b"}},
		{"a value a $ref and its sibling admit", map[string]any{"$ref": "#/components/schemas/Name", "enum": []any{"a"}}, "a"},
		{"a number written otherwise than its enum value", map[string]any{"enum": []any{1.0}}, num("1.0")},
		{"a number equal to a const", map[string]any{"const": 5}, num("5")},
	} {
		if errs := validate(doc, tc.schema, tc.v, "v"); len(errs) != 0 {
			t.Errorf("%s: refused: %v", tc.name, errs)
		}
	}
}

// The description's bounds are the server's: at each bound and just past it, the described schema
// admits a value exactly when the server's own check does.
func TestDescriptionBoundsMatchServer(t *testing.T) {
	t.Parallel()
	doc := description(t)
	schema := func(ref string) map[string]any {
		s, ok := pointer(doc, ref).(map[string]any)
		if !ok {
			t.Fatalf("%s: no schema", ref)
		}
		return s
	}
	agree := func(what string, s map[string]any, v any, served bool) {
		t.Helper()
		if described := len(validate(doc, s, v, "v")) == 0; described != served {
			t.Errorf("%s: the description admits it %t, the server %t", what, described, served)
		}
	}
	// acts.go's limit check: 1, a literal there, to maxLimit.
	limit := schema("#/components/parameters/Limit/schema")
	for _, n := range []int{0, 1, maxLimit, maxLimit + 1} {
		agree(fmt.Sprintf("limit %d", n), limit, json.Number(fmt.Sprint(n)), n >= 1 && n <= maxLimit)
	}
	key := schema("#/components/parameters/IdempotencyKey/schema")
	for _, k := range []string{strings.Repeat("a", 15), strings.Repeat("a", 16), strings.Repeat("a", 128), strings.Repeat("a", 129),
		"0123456789abcde!", strings.Repeat("é", 16)} {
		agree(fmt.Sprintf("the Idempotency-Key %q", k), key, k, keyShape.MatchString(k))
	}
	// Every layer either side names, and two names that are not layers: a layer added to only one
	// of fragmentLayers and the description fails.
	fragments := schema("#/components/schemas/AssignmentInput/properties/fragments")
	props, _ := fragments["properties"].(map[string]any)
	layers := slices.Concat(fragmentLayers, slices.Collect(maps.Keys(props)), []string{"machine-intrinsic", "Global"})
	for _, l := range slices.Compact(slices.Sorted(slices.Values(layers))) {
		in := assignmentInput{Fragments: map[string][]string{l: {"base"}}}
		agree(fmt.Sprintf("a fragments member %q", l), fragments, map[string]any{l: []any{"base"}}, in.check(nil) == nil)
	}
	// Beside a layer that selects one, so the per-layer rule decides and not the total's.
	in := assignmentInput{Fragments: map[string][]string{"global": {}, "site": {"base"}}}
	agree("an empty fragments layer", fragments, map[string]any{"global": []any{}, "site": []any{"base"}}, in.check(nil) == nil)
	// A selected name's shape, and the 256 a list can reach alone: the total across lists is prose.
	profiles := schema("#/components/schemas/AssignmentInput/properties/profiles")
	layer := schema("#/components/schemas/LayerFragments")
	for _, n := range []string{"", "a", "a-b", "0a", strings.Repeat("a", 63), strings.Repeat("a", 64), "-a", "a-", "A", "a_b", "é"} {
		in := assignmentInput{Profiles: []string{n}}
		agree(fmt.Sprintf("a profile named %q", n), profiles, []any{n}, in.check(nil) == nil)
		in = assignmentInput{Fragments: map[string][]string{"site": {n}}}
		agree(fmt.Sprintf("a fragment named %q", n), layer, []any{n}, in.check(nil) == nil)
	}
	for _, n := range []int{256, 257} {
		var list []string
		var described []any
		for i := range n {
			list = append(list, fmt.Sprintf("n%d", i))
			described = append(described, list[i])
		}
		in := assignmentInput{Profiles: list}
		agree(fmt.Sprintf("%d profiles", n), profiles, described, in.check(nil) == nil)
		in = assignmentInput{Fragments: map[string][]string{"site": list}}
		agree(fmt.Sprintf("%d fragments in one layer", n), layer, described, in.check(nil) == nil)
	}
}

// ReferenceInput admits a declaration exactly when the server's declaration check does. The check
// (checkDeclarations, internal/ingest) is reached through ingest.Stored, which holds a stream's
// declarations to it before anything else, here with a stream that uses the one declared name.
func TestDescriptionReferenceInputMatchesServer(t *testing.T) {
	t.Parallel()
	doc := description(t)
	s, ok := pointer(doc, "#/components/schemas/ReferenceInput").(map[string]any)
	if !ok {
		t.Fatal("no ReferenceInput schema")
	}
	const absent = "(absent)"
	for _, kind := range []string{"string", "integer", "boolean", "mapping", "secret"} {
		for _, version := range []int64{0, 1} {
			for _, enc := range []any{absent, nil, "", "base64", "hex"} {
				v := map[string]any{"kind": kind, "version": json.Number(fmt.Sprint(version))}
				r := ingest.Reference{Kind: provider.Kind(kind), Version: version}
				if enc != absent {
					v["encoding"] = enc
					r.Encoding, _ = enc.(string)
				}
				_, err := ingest.Stored("a: !bwref ref\n", ingest.Declarations{References: map[string]ingest.Reference{"ref": r}})
				if described := len(validate(doc, s, v, "v")) == 0; described != (err == nil) {
					t.Errorf("kind %s, version %d, encoding %v: the description admits it %t, the server %t", kind, version, enc, described, err == nil)
				}
			}
		}
	}
}

// checkResponse's negative controls: each kind of mismatch, made on a real exchange shape, is
// reported, and the matching exchange is not.
func TestExchangeErrorsReportEachMismatch(t *testing.T) {
	t.Parallel()
	doc := description(t)
	cluster := `{"id":"cl_x","name":"n","endpoint":"https://e.example.test","contract":"v1.13","talosClusterId":"BWSYNTH-id"}`
	input := `{"name":"n","endpoint":"https://e.example.test","contract":"v1.13","talosClusterId":"BWSYNTH-id"}`
	answer := func(status int, ctype, body string, headers ...string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		if ctype != "" {
			rec.Header().Set("Content-Type", ctype)
		}
		rec.Header().Set("Bronzeward-Epoch", "ep_x")
		rec.Header().Set("Bronzeward-Request-ID", "req_x")
		rec.Header().Set("Cache-Control", "no-store")
		if status == http.StatusCreated {
			rec.Header().Set("Location", prefix+"/clusters/cl_x")
		}
		for i := 0; i+1 < len(headers); i += 2 {
			if headers[i+1] == "" {
				rec.Header().Del(headers[i])
			} else {
				rec.Header().Set(headers[i], headers[i+1])
			}
		}
		rec.WriteHeader(status)
		rec.Body.WriteString(body)
		return rec
	}
	late := func(rec *httptest.ResponseRecorder, name, value string) *httptest.ResponseRecorder {
		rec.Header().Set(name, value)
		return rec
	}
	post := func(body string) *http.Request {
		return httptest.NewRequest(http.MethodPost, prefix+"/clusters", strings.NewReader(body))
	}
	if errs := exchangeErrors(doc, post(input), input, answer(201, "application/json", cluster)); len(errs) != 0 {
		t.Fatalf("a described exchange refused: %v", errs)
	}
	problemBody := `{"type":"urn:bronzeward:problem:not-found","title":"t","status":404,"instance":"req"}`
	if errs := exchangeErrors(doc, post(input), input, answer(404, "application/problem+json", problemBody)); len(errs) != 0 {
		t.Fatalf("a described problem refused: %v", errs)
	}
	for _, tc := range []struct {
		name, req string
		rec       *httptest.ResponseRecorder
		want      string
	}{
		{"an undescribed status", input, answer(299, "application/json", cluster), "lists no such status"},
		{"an undescribed media type", input, answer(201, "text/plain", cluster), "lists no text/plain body"},
		{"a body the schema refuses", input, answer(201, "application/json", `{"id":"cl_x"}`), "is missing"},
		{"a body with data after its value", input, answer(201, "application/json", cluster+`{"x":1}`), "the body is not JSON"},
		{"an accepted request with data after its value", input + " x", answer(201, "application/json", cluster), "the accepted request body is not JSON"},
		{"a problem type not described", input, answer(404, "application/problem+json",
			strings.Replace(problemBody, "not-found", "no-such-code", 1)), "is not one of"},
		{"an accepted request the schema refuses", `{"name":"n"}`, answer(201, "application/json", cluster), "request: member"},
		{"an undescribed header", input, answer(201, "application/json", cluster, "ETag", `"v1"`), "header Etag is not described"},
		{"a missing required header", input, answer(201, "application/json", cluster, "Location", ""), "required header Location is missing"},
		{"a required header set after the answer was written", input, late(answer(201, "application/json", cluster, "Location", ""),
			"Location", prefix+"/clusters/cl_x"), "required header Location is missing"},
		{"a header value the schema refuses", input, answer(201, "application/json", cluster, "Bronzeward-Recovery-Mode", "yes"),
			"header Bronzeward-Recovery-Mode"},
		{"an undescribed problem header", input, answer(404, "application/problem+json", problemBody, "Location", "/x"),
			"header Location is not described"},
	} {
		errs := exchangeErrors(doc, post(tc.req), tc.req, tc.rec)
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, tc.want) }) {
			t.Errorf("%s: got %q, want an error containing %q", tc.name, errs, tc.want)
		}
	}
	// A request's query and headers: HEAD is answered as its GET, without a body.
	req := func(method, target string, headers ...string) *http.Request {
		r := httptest.NewRequest(method, prefix+target, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		return r
	}
	page := `{"items":[` + cluster + `],"next":"c"}`
	for _, tc := range []struct {
		name string
		r    *http.Request
		rec  *httptest.ResponseRecorder
		want string // "" when the exchange is described
	}{
		{"a HEAD answered as its GET", req(http.MethodHead, "/clusters/cl_x"), answer(200, "application/json", ""), ""},
		{"a HEAD answer's media type not described", req(http.MethodHead, "/clusters/cl_x"), answer(200, "text/plain", ""),
			"lists no text/plain body"},
		{"a page with its query", req(http.MethodGet, "/clusters?limit=5&cursor=c"), answer(200, "application/json", page), ""},
		{"a query parameter not described", req(http.MethodGet, "/clusters?state=open"), answer(200, "application/json", page),
			"query parameter state is not described"},
		{"a query value the schema refuses", req(http.MethodGet, "/clusters?limit=0"), answer(200, "application/json", page), "query limit"},
		{"a header value the schema refuses", req(http.MethodPost, "/clusters", "Idempotency-Key", "short"),
			answer(201, "application/json", cluster), "header Idempotency-Key"},
	} {
		body := ""
		if tc.r.Method == http.MethodPost {
			body = input
		}
		errs := exchangeErrors(doc, tc.r, body, tc.rec)
		switch {
		case tc.want == "" && len(errs) != 0:
			t.Errorf("%s: refused: %q", tc.name, errs)
		case tc.want != "" && !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, tc.want) }):
			t.Errorf("%s: got %q, want an error containing %q", tc.name, errs, tc.want)
		}
	}
}
