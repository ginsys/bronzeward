package api

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
)

// descriptionPath is the OpenAPI description of v1, relative to this package's directory.
const descriptionPath = "../../api/openapi.yaml"

// The extension fields an operation carries for the route table's facts that OpenAPI has no
// field for.
const (
	xRoles           = "x-bronzeward-roles"
	xHumanOnly       = "x-bronzeward-human-only"
	xIfMatch         = "x-bronzeward-if-match"
	xNormalStartOnly = "x-bronzeward-normal-start-only"
	// xRecoveryStartOnly marks the route a process started without the recovery-start flag
	// refuses (internal/api/api.go, the entryPattern check).
	xRecoveryStartOnly = "x-bronzeward-recovery-start-only"
)

var operationMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// routeFacts is what the description and the route table must agree on for one operation: the
// route's facts, and the request a client generator builds from the description's parameters and
// request body. pathParams are the template's wildcards, in order; key, ifMatchHeader and body
// are a required Idempotency-Key, a required If-Match and a request body.
type routeFacts struct {
	roles                                                  []string
	humanOnly, ifMatch, normalStartOnly, recoveryStartOnly bool
	pathParams                                             []string
	key, ifMatchHeader, body                               bool
}

func (f routeFacts) String() string {
	return fmt.Sprintf("roles %v, humanOnly %t, ifMatch %t, normalStartOnly %t, recoveryStartOnly %t, path parameters %v, Idempotency-Key %t, If-Match header %t, body %t",
		f.roles, f.humanOnly, f.ifMatch, f.normalStartOnly, f.recoveryStartOnly, f.pathParams, f.key, f.ifMatchHeader, f.body)
}

func (f routeFacts) equal(g routeFacts) bool {
	return slices.Equal(f.roles, g.roles) && f.humanOnly == g.humanOnly && f.ifMatch == g.ifMatch &&
		f.normalStartOnly == g.normalStartOnly && f.recoveryStartOnly == g.recoveryStartOnly && slices.Equal(f.pathParams, g.pathParams) && f.key == g.key &&
		f.ifMatchHeader == g.ifMatchHeader && f.body == g.body
}

var pathWildcard = regexp.MustCompile(`\{([^}]+)\}`)

// tableFacts returns routes()'s facts, keyed by "METHOD pattern".
func tableFacts(t *testing.T) map[string]routeFacts {
	t.Helper()
	out := map[string]routeFacts{}
	for _, rt := range routes() {
		k := rt.method + " " + rt.pattern
		if _, dup := out[k]; dup {
			t.Fatalf("the route table serves %s twice", k)
		}
		f := routeFacts{humanOnly: rt.humanOnly, ifMatch: rt.ifMatch, normalStartOnly: rt.normalStartOnly,
			recoveryStartOnly: rt.pattern == entryPattern, key: rt.mutating(), ifMatchHeader: rt.ifMatch}
		for _, r := range rt.roles {
			f.roles = append(f.roles, string(r))
		}
		for _, m := range pathWildcard.FindAllStringSubmatch(rt.pattern, -1) {
			f.pathParams = append(f.pathParams, m[1])
		}
		if rt.input != nil {
			_, none := rt.input().(*noBody)
			f.body = !none
		}
		out[k] = f
	}
	return out
}

var (
	descriptionOnce sync.Once
	descriptionDoc  map[string]any
	descriptionErr  error
)

// loadDescription returns the parsed description, read once per test binary. Callers must not
// modify it; describedFacts copies what it reads.
func loadDescription() (map[string]any, error) {
	descriptionOnce.Do(func() {
		raw, err := os.ReadFile(descriptionPath)
		if err != nil {
			descriptionErr = err
			return
		}
		descriptionErr = yaml.Unmarshal(raw, &descriptionDoc)
	})
	if descriptionErr != nil {
		return nil, fmt.Errorf("reading %s: %w", descriptionPath, descriptionErr)
	}
	return descriptionDoc, nil
}

// description is loadDescription for a test's own goroutine, which a failure stops.
func description(t *testing.T) map[string]any {
	t.Helper()
	doc, err := loadDescription()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// describedFacts returns the description's facts, keyed as tableFacts keys them, and every
// malformed operation it met. A path parameter must be required; an Idempotency-Key or If-Match
// parameter counts only when it is a required header; a request body must be required. Parameters
// are described per operation: a path item's own parameters, which OpenAPI applies to all its
// operations, are refused rather than merged.
func describedFacts(doc map[string]any) (map[string]routeFacts, []string) {
	out := map[string]routeFacts{}
	var bad []string
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		item, _ := paths[p].(map[string]any)
		if _, ok := item["parameters"]; ok {
			bad = append(bad, p+": parameters on the path item, not on its operations")
		}
		for _, m := range operationMethods {
			raw, ok := item[m]
			if !ok {
				continue
			}
			op, _ := raw.(map[string]any)
			k := strings.ToUpper(m) + " " + p
			var f routeFacts
			// An empty list is GET /identity's: the one route that needs no role (§10.3).
			roles, ok := op[xRoles].([]any)
			if !ok {
				bad = append(bad, k+": no "+xRoles)
			}
			for _, r := range roles {
				s, ok := r.(string)
				if !ok {
					bad = append(bad, fmt.Sprintf("%s: role %v is not a string", k, r))
				}
				f.roles = append(f.roles, s)
			}
			for name, dst := range map[string]*bool{xHumanOnly: &f.humanOnly, xIfMatch: &f.ifMatch, xNormalStartOnly: &f.normalStartOnly,
				xRecoveryStartOnly: &f.recoveryStartOnly} {
				switch v := op[name].(type) {
				case nil:
				case bool:
					*dst = v
				default:
					bad = append(bad, fmt.Sprintf("%s: %s is %v, not a boolean", k, name, v))
				}
			}
			params, _ := op["parameters"].([]any)
			for _, raw := range params {
				p, _ := deref(doc, raw).(map[string]any)
				name, _ := p["name"].(string)
				required := p["required"] == true
				switch in := p["in"]; {
				case in == "path":
					if !required {
						bad = append(bad, fmt.Sprintf("%s: path parameter %s is not required", k, name))
					}
					f.pathParams = append(f.pathParams, name)
				case in == "header" && (name == "Idempotency-Key" || name == "If-Match"):
					// Present counts, so a generator's view matches; an optional one is refused.
					if name == "Idempotency-Key" {
						f.key = true
					} else {
						f.ifMatchHeader = true
					}
					if !required {
						bad = append(bad, fmt.Sprintf("%s: header parameter %s is not required", k, name))
					}
				}
			}
			if raw, ok := op["requestBody"]; ok {
				rb, _ := deref(doc, raw).(map[string]any)
				if f.body = true; rb["required"] != true {
					bad = append(bad, k+": the request body is not required")
				}
			}
			out[k] = f
		}
	}
	return out, bad
}

// routeTableDiff reports, in a stable order, every operation the table serves that the
// description lacks or describes differently, and every operation the description has that the
// table does not serve.
func routeTableDiff(table, described map[string]routeFacts) []string {
	var diff []string
	for _, k := range slices.Sorted(maps.Keys(table)) {
		d, ok := described[k]
		switch {
		case !ok:
			diff = append(diff, "served but not described: "+k)
		case !d.equal(table[k]):
			diff = append(diff, fmt.Sprintf("%s: the table has %v, the description %v", k, table[k], d))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(described)) {
		if _, ok := table[k]; !ok {
			diff = append(diff, "described but not served: "+k)
		}
	}
	return diff
}

func TestDescriptionMatchesRouteTable(t *testing.T) {
	t.Parallel()
	described, bad := describedFacts(description(t))
	for _, b := range bad {
		t.Error(b)
	}
	for _, d := range routeTableDiff(tableFacts(t), described) {
		t.Error(d)
	}
}

// The comparison's negative controls: each kind of difference, made on a copy of the real
// description's facts, is reported, and only it.
func TestRouteTableDiffReportsEachDifference(t *testing.T) {
	t.Parallel()
	table := tableFacts(t)
	described, _ := describedFacts(description(t))
	if d := routeTableDiff(table, described); len(d) != 0 {
		t.Fatalf("the description differs already: %v", d)
	}
	keys := slices.Sorted(maps.Keys(table))
	var roles, humanOnly, ifMatch, normalStartOnly, recoveryStartOnly string
	for _, k := range keys {
		f := table[k]
		if len(f.roles) > 1 && roles == "" {
			roles = k
		}
		if f.humanOnly && humanOnly == "" {
			humanOnly = k
		}
		if f.ifMatch && ifMatch == "" {
			ifMatch = k
		}
		if f.normalStartOnly && normalStartOnly == "" {
			normalStartOnly = k
		}
		if f.recoveryStartOnly && recoveryStartOnly == "" {
			recoveryStartOnly = k
		}
	}
	if roles == "" || humanOnly == "" || ifMatch == "" || normalStartOnly == "" || recoveryStartOnly == "" {
		t.Fatal("the table has no route for one of the controls")
	}
	var params, body, read string
	for _, k := range keys {
		f := table[k]
		if len(f.pathParams) > 1 && params == "" {
			params = k
		}
		if f.body && body == "" {
			body = k
		}
		if !f.key && read == "" {
			read = k
		}
	}
	if params == "" || body == "" || read == "" {
		t.Fatal("the table has no route for one of the request controls")
	}
	edit := func(k string, change func(*routeFacts)) map[string]routeFacts {
		c := maps.Clone(described)
		f := c[k]
		f.roles = slices.Clone(f.roles)
		f.pathParams = slices.Clone(f.pathParams)
		change(&f)
		c[k] = f
		return c
	}
	removed := maps.Clone(described)
	delete(removed, keys[0])
	added := maps.Clone(described)
	added["GET /not-served"] = routeFacts{roles: []string{"viewer"}}
	for _, tc := range []struct {
		name      string
		described map[string]routeFacts
		want      string
	}{
		{"an operation missing", removed, "served but not described: " + keys[0]},
		{"an operation the table does not serve", added, "described but not served: GET /not-served"},
		{"a role dropped", edit(roles, func(f *routeFacts) { f.roles = f.roles[1:] }), roles + ":"},
		{"roles reordered", edit(roles, func(f *routeFacts) { slices.Reverse(f.roles) }), roles + ":"},
		{"humanOnly cleared", edit(humanOnly, func(f *routeFacts) { f.humanOnly = false }), humanOnly + ":"},
		{"ifMatch cleared", edit(ifMatch, func(f *routeFacts) { f.ifMatch = false }), ifMatch + ":"},
		{"normalStartOnly cleared", edit(normalStartOnly, func(f *routeFacts) { f.normalStartOnly = false }), normalStartOnly + ":"},
		{"recoveryStartOnly cleared", edit(recoveryStartOnly, func(f *routeFacts) { f.recoveryStartOnly = false }), recoveryStartOnly + ":"},
		{"recoveryStartOnly set elsewhere", edit(read, func(f *routeFacts) { f.recoveryStartOnly = true }), read + ":"},
		{"a path parameter renamed", edit(params, func(f *routeFacts) { f.pathParams[1] += "x" }), params + ":"},
		{"path parameters reordered", edit(params, func(f *routeFacts) { slices.Reverse(f.pathParams) }), params + ":"},
		{"the Idempotency-Key dropped", edit(ifMatch, func(f *routeFacts) { f.key = false }), ifMatch + ":"},
		{"an Idempotency-Key on a read", edit(read, func(f *routeFacts) { f.key = true }), read + ":"},
		{"the If-Match header dropped", edit(ifMatch, func(f *routeFacts) { f.ifMatchHeader = false }), ifMatch + ":"},
		{"the request body dropped", edit(body, func(f *routeFacts) { f.body = false }), body + ":"},
		{"a request body on a read", edit(read, func(f *routeFacts) { f.body = true }), read + ":"},
	} {
		d := routeTableDiff(table, tc.described)
		if len(d) != 1 || !strings.HasPrefix(d[0], tc.want) {
			t.Errorf("%s: got %q, want one difference starting %q", tc.name, d, tc.want)
		}
	}
}

// describedFacts' negative controls: each change a client generator would follow, made to one
// operation of a copy of the real description, is reported for that operation, and only it.
func TestDescribedFactsReadRequestShape(t *testing.T) {
	t.Parallel()
	doc := description(t)
	table := tableFacts(t)
	var k string
	for _, key := range slices.Sorted(maps.Keys(table)) {
		if f := table[key]; f.ifMatchHeader && len(f.pathParams) > 0 && f.body {
			k = key
			break
		}
	}
	if k == "" {
		t.Fatal("the table has no route with If-Match, a path parameter and a body")
	}
	method, path, _ := strings.Cut(k, " ")
	paths := doc["paths"].(map[string]any)
	item := paths[path].(map[string]any)
	op := item[strings.ToLower(method)].(map[string]any)
	// replace returns op's parameters with the one named name replaced by with, or dropped.
	replace := func(name string, with map[string]any) []any {
		var out []any
		for _, raw := range op["parameters"].([]any) {
			p, _ := deref(doc, raw).(map[string]any)
			switch {
			case p["name"] != name:
				out = append(out, raw)
			case with != nil:
				out = append(out, with)
			}
		}
		return out
	}
	pathParam := table[k].pathParams[0]
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"a path parameter renamed", func(c map[string]any) {
			c["parameters"] = replace(pathParam, map[string]any{"name": pathParam + "x", "in": "path", "required": true})
		}},
		{"the If-Match parameter removed", func(c map[string]any) { c["parameters"] = replace("If-Match", nil) }},
		{"the Idempotency-Key not required", func(c map[string]any) {
			c["parameters"] = replace("Idempotency-Key", map[string]any{"name": "Idempotency-Key", "in": "header"})
		}},
		{"the request body removed", func(c map[string]any) { delete(c, "requestBody") }},
		{"the request body not required", func(c map[string]any) {
			rb := maps.Clone(deref(doc, c["requestBody"]).(map[string]any))
			delete(rb, "required")
			c["requestBody"] = rb
		}},
	} {
		c := maps.Clone(op)
		tc.change(c)
		changedItem := maps.Clone(item)
		changedItem[strings.ToLower(method)] = c
		changedPaths := maps.Clone(paths)
		changedPaths[path] = changedItem
		changed := maps.Clone(doc)
		changed["paths"] = changedPaths
		described, bad := describedFacts(changed)
		d := append(routeTableDiff(table, described), bad...)
		if len(d) != 1 || !strings.HasPrefix(d[0], k+":") {
			t.Errorf("%s: got %q, want one difference starting %q", tc.name, d, k+":")
		}
	}
}

// An optional Idempotency-Key or If-Match is one a client generator still emits; describedFacts
// refuses it on a route that takes neither, where reading it as absent would match the table.
func TestDescribedFactsRefuseOptionalHeaderParameters(t *testing.T) {
	t.Parallel()
	doc := description(t)
	paths := doc["paths"].(map[string]any)
	for _, name := range []string{"Idempotency-Key", "If-Match"} {
		item := maps.Clone(paths["/clusters"].(map[string]any))
		op := maps.Clone(item["get"].(map[string]any))
		params, _ := op["parameters"].([]any)
		op["parameters"] = append(slices.Clone(params), map[string]any{"name": name, "in": "header", "schema": map[string]any{"type": "string"}})
		item["get"] = op
		changedPaths := maps.Clone(paths)
		changedPaths["/clusters"] = item
		changed := maps.Clone(doc)
		changed["paths"] = changedPaths
		if _, bad := describedFacts(changed); !slices.Contains(bad, "GET /clusters: header parameter "+name+" is not required") {
			t.Errorf("an optional %s on GET /clusters not refused: %q", name, bad)
		}
	}
}

// A path item's own parameters apply to all its operations; describedFacts refuses them instead
// of reading facts without them.
func TestDescribedFactsRefusePathItemParameters(t *testing.T) {
	t.Parallel()
	doc := description(t)
	paths := doc["paths"].(map[string]any)
	item := maps.Clone(paths["/clusters"].(map[string]any))
	item["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/IdempotencyKey"}}
	changedPaths := maps.Clone(paths)
	changedPaths["/clusters"] = item
	changed := maps.Clone(doc)
	changed["paths"] = changedPaths
	if _, bad := describedFacts(changed); !slices.Contains(bad, "/clusters: parameters on the path item, not on its operations") {
		t.Errorf("path-item parameters not refused: %q", bad)
	}
}
