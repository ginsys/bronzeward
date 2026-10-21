package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// checkResponse fails t when rec, the answer to r, is not one the description lists for r's
// operation: an undescribed status, a media type the status does not list, or a JSON body its
// schema refuses. A request the server accepted must also fit the operation's request schema, so
// the description requires no member the server does without. env.do calls it for every request
// a test sends through it, so the API tests' own exchanges are what the description's schemas are
// checked against. A path outside prefix, or one no described path matches, is a test's own route
// and is not checked.
func checkResponse(t *testing.T, r *http.Request, reqBody string, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, e := range exchangeErrors(description(t), r, reqBody, rec) {
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
	op, ok := item[strings.ToLower(r.Method)].(map[string]any)
	if !ok {
		// The router answers a method the path does not serve; that answer is a problem.
		op = map[string]any{"responses": map[string]any{"default": map[string]any{"$ref": "#/components/responses/Problem"}}}
	}
	var errs []string
	if rec.Code < 300 {
		errs = requestErrors(doc, op, reqBody, where)
	}
	responses, _ := op["responses"].(map[string]any)
	resp, ok := responses[fmt.Sprint(rec.Code)]
	if !ok {
		if resp, ok = responses["default"]; !ok || rec.Code < 400 {
			return append(errs, where+": the description lists no such status")
		}
	}
	content, _ := deref(doc, resp).(map[string]any)["content"].(map[string]any)
	if len(content) == 0 {
		if rec.Body.Len() != 0 {
			errs = append(errs, fmt.Sprintf("%s: the description lists no body, the answer has %d bytes", where, rec.Body.Len()))
		}
		return errs
	}
	mt, _, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil {
		return append(errs, fmt.Sprintf("%s: Content-Type %q: %v", where, rec.Header().Get("Content-Type"), err))
	}
	media, ok := content[mt].(map[string]any)
	if !ok {
		return append(errs, fmt.Sprintf("%s: the description lists no %s body", where, mt))
	}
	if mt == "text/event-stream" {
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

func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
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
		"minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems"}
	annotationKeywords = []string{"description", "format", "examples", "deprecated"}
)

// validate returns every way v breaks schema, a JSON Schema in the subset above; at names v.
func validate(doc map[string]any, schema, v any, at string) []string {
	s, ok := schema.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: the schema is %T, not an object", at, schema)}
	}
	if ref, ok := s["$ref"].(string); ok {
		return validate(doc, pointer(doc, ref), v, at)
	}
	var errs []string
	if types, ok := s["type"]; ok && !hasType(types, v) {
		return []string{fmt.Sprintf("%s: %s is not of type %v", at, shortJSON(v), types)}
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
// UseNumber.
func sameValue(schema, v any) bool {
	if n, ok := v.(json.Number); ok {
		return fmt.Sprint(schema) == n.String()
	}
	return reflect.DeepEqual(schema, v)
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

// The validator's negative controls: each assertion keyword refuses the value it should.
func TestValidateRefusesEachKind(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"Item": map[string]any{"type": "object", "required": []any{"id"}, "additionalProperties": false,
			"properties": map[string]any{"id": map[string]any{"type": "string"}, "n": map[string]any{"type": "integer"}}},
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
	} {
		if errs := validate(doc, tc.schema, tc.v, "v"); len(errs) == 0 {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	if errs := validate(doc, ref, map[string]any{"id": "a", "n": num("2")}, "v"); len(errs) != 0 {
		t.Errorf("a valid value refused: %v", errs)
	}
	if errs := validate(doc, map[string]any{"type": []any{"string", "null"}}, nil, "v"); len(errs) != 0 {
		t.Errorf("null refused where allowed: %v", errs)
	}
}

// checkResponse's negative controls: each kind of mismatch, made on a real exchange shape, is
// reported, and the matching exchange is not.
func TestExchangeErrorsReportEachMismatch(t *testing.T) {
	t.Parallel()
	doc := description(t)
	cluster := `{"id":"cl_x","name":"n","endpoint":"https://e.example.test","contract":"v1.13","talosClusterId":"BWSYNTH-id"}`
	input := `{"name":"n","endpoint":"https://e.example.test","contract":"v1.13","talosClusterId":"BWSYNTH-id"}`
	answer := func(status int, ctype, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		if ctype != "" {
			rec.Header().Set("Content-Type", ctype)
		}
		rec.WriteHeader(status)
		rec.Body.WriteString(body)
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
		{"a problem type not described", input, answer(404, "application/problem+json",
			strings.Replace(problemBody, "not-found", "no-such-code", 1)), "is not one of"},
		{"an accepted request the schema refuses", `{"name":"n"}`, answer(201, "application/json", cluster), "request: member"},
	} {
		errs := exchangeErrors(doc, post(tc.req), tc.req, tc.rec)
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, tc.want) }) {
			t.Errorf("%s: got %q, want an error containing %q", tc.name, errs, tc.want)
		}
	}
}
