package api

import (
	"fmt"
	"maps"
	"os"
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
)

var operationMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// routeFacts is what the description and the route table must agree on for one operation.
type routeFacts struct {
	roles                               []string
	humanOnly, ifMatch, normalStartOnly bool
}

func (f routeFacts) String() string {
	return fmt.Sprintf("roles %v, humanOnly %t, ifMatch %t, normalStartOnly %t", f.roles, f.humanOnly, f.ifMatch, f.normalStartOnly)
}

func (f routeFacts) equal(g routeFacts) bool {
	return slices.Equal(f.roles, g.roles) && f.humanOnly == g.humanOnly && f.ifMatch == g.ifMatch &&
		f.normalStartOnly == g.normalStartOnly
}

// tableFacts returns routes()'s facts, keyed by "METHOD pattern".
func tableFacts(t *testing.T) map[string]routeFacts {
	t.Helper()
	out := map[string]routeFacts{}
	for _, rt := range routes() {
		k := rt.method + " " + rt.pattern
		if _, dup := out[k]; dup {
			t.Fatalf("the route table serves %s twice", k)
		}
		f := routeFacts{humanOnly: rt.humanOnly, ifMatch: rt.ifMatch, normalStartOnly: rt.normalStartOnly}
		for _, r := range rt.roles {
			f.roles = append(f.roles, string(r))
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

// description returns the parsed description, read once per test binary. Callers must not
// modify it; describedFacts copies what it reads.
func description(t *testing.T) map[string]any {
	t.Helper()
	descriptionOnce.Do(func() {
		raw, err := os.ReadFile(descriptionPath)
		if err != nil {
			descriptionErr = err
			return
		}
		descriptionErr = yaml.Unmarshal(raw, &descriptionDoc)
	})
	if descriptionErr != nil {
		t.Fatalf("reading %s: %v", descriptionPath, descriptionErr)
	}
	return descriptionDoc
}

// describedFacts returns the description's facts, keyed as tableFacts keys them, and every
// malformed operation it met.
func describedFacts(doc map[string]any) (map[string]routeFacts, []string) {
	out := map[string]routeFacts{}
	var bad []string
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		item, _ := paths[p].(map[string]any)
		for _, m := range operationMethods {
			raw, ok := item[m]
			if !ok {
				continue
			}
			op, _ := raw.(map[string]any)
			k := strings.ToUpper(m) + " " + p
			var f routeFacts
			roles, ok := op[xRoles].([]any)
			if !ok || len(roles) == 0 {
				bad = append(bad, k+": no "+xRoles)
			}
			for _, r := range roles {
				s, ok := r.(string)
				if !ok {
					bad = append(bad, fmt.Sprintf("%s: role %v is not a string", k, r))
				}
				f.roles = append(f.roles, s)
			}
			for name, dst := range map[string]*bool{xHumanOnly: &f.humanOnly, xIfMatch: &f.ifMatch, xNormalStartOnly: &f.normalStartOnly} {
				switch v := op[name].(type) {
				case nil:
				case bool:
					*dst = v
				default:
					bad = append(bad, fmt.Sprintf("%s: %s is %v, not a boolean", k, name, v))
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
	var roles, humanOnly, ifMatch, normalStartOnly string
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
	}
	if roles == "" || humanOnly == "" || ifMatch == "" || normalStartOnly == "" {
		t.Fatal("the table has no route for one of the controls")
	}
	edit := func(k string, change func(*routeFacts)) map[string]routeFacts {
		c := maps.Clone(described)
		f := c[k]
		f.roles = slices.Clone(f.roles)
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
	} {
		d := routeTableDiff(table, tc.described)
		if len(d) != 1 || !strings.HasPrefix(d[0], tc.want) {
			t.Errorf("%s: got %q, want one difference starting %q", tc.name, d, tc.want)
		}
	}
}
