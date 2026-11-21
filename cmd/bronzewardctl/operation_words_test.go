package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// What an operation did, what comes next and who started it (client contracts A10,
// ginsys/bronzeward#1014): operation show prints the sentence and the next step for each pair the server
// can produce, operation list and show name who started it, an unknown pair prints its raw values and no
// next step, and -o json and -o yaml print the resource unchanged and read nothing more.

const (
	webWordsPath = "../../web/src/operation-words.ts"
	apiOpsPath   = "../../internal/api/operations.go"
)

// goStrings is the list the Go source assigns to name: the kinds and states the server can produce.
func goStrings(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\b` + name + `\s*=\s*\[\]string\{(.*?)\}`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no list named %s", apiOpsPath, name)
	}
	var out []string
	for _, s := range regexp.MustCompile(`"([a-z-]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}

type webRow struct{ kind, state, now, next string }

var (
	webKindStart = regexp.MustCompile(`(?m)^ {2}(?:"([a-z-]+)"|([a-z]+)): \{$`)
	webRowFull   = regexp.MustCompile(`(?m)^ {4}([a-z]+):\s*\[\s*("(?:[^"\\\n]|\\.)*")\s*,\s*("(?:[^"\\\n]|\\.)*"|undefined)\s*,?\s*\],?$`)
)

// webDecl is the table's declaration line, whole: nothing may follow its opening brace.
const webDecl = "const KNOWN: Record<string, Record<string, [now: string, next: string | undefined]>> = {"

// webWords reads the table of web/src/operation-words.ts: each `state: [now, next]` row of each kind, `next`
// being a string or `undefined`. It fails on a row it cannot take whole (a template string, a single quote, a
// row it cut short), and on a table that does not hold exactly the 16 pairs, each with a sentence. It guards
// against an honest edit of the table drifting from the Go table, not against a source written to deceive it:
// a carriage return, a block comment or a second declaration are refused as forms it does not read.
func webWords(src string) ([]webRow, error) {
	if strings.Contains(src, "\r") || strings.Contains(src, "/*") {
		return nil, fmt.Errorf("the source holds a carriage return or a block comment, which the reader does not read")
	}
	if strings.Count(src, "const KNOWN") != 1 {
		return nil, fmt.Errorf("the source must declare KNOWN exactly once")
	}
	start := strings.Index(src, "const KNOWN")
	end := strings.Index(src, "\n};\n")
	if start < 0 || end < start {
		return nil, fmt.Errorf("the KNOWN table was not found")
	}
	var lines []string
	for _, l := range strings.Split(src[start:end], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			lines = append(lines, l)
		}
	}
	block := strings.Join(lines, "\n")
	kinds := webKindStart.FindAllStringSubmatchIndex(block, -1)
	taken := webRowFull.FindAllStringSubmatchIndex(block, -1)
	// Blank what the reader took (each kind header and each row). Whatever is left must be a kind's closing
	// brace or the declaration line, at any indentation: any other text is table content the reader did not
	// read, whatever its layout.
	left := []byte(block)
	for _, m := range append(slices.Clone(kinds), taken...) {
		for i := m[0]; i < m[1]; i++ {
			left[i] = ' '
		}
	}
	for _, l := range strings.Split(string(left), "\n") {
		if l = strings.TrimSpace(l); l != "" && l != "}," && l != webDecl {
			return nil, fmt.Errorf("the table holds text the reader did not take: %q", l)
		}
	}
	var rows []webRow
	for _, m := range taken {
		kind := ""
		for _, k := range kinds {
			if k[0] < m[0] {
				if k[2] >= 0 {
					kind = block[k[2]:k[3]]
				} else {
					kind = block[k[4]:k[5]]
				}
			}
		}
		state, quotedNow, quotedNext := block[m[2]:m[3]], block[m[4]:m[5]], block[m[6]:m[7]]
		now, err := strconv.Unquote(quotedNow)
		if err != nil || now == "" {
			return nil, fmt.Errorf("row %s %s has no sentence: %v", kind, state, err)
		}
		next := ""
		if quotedNext != "undefined" {
			if next, err = strconv.Unquote(quotedNext); err != nil {
				return nil, fmt.Errorf("row %s %s: next step %s: %v", kind, state, quotedNext, err)
			}
		}
		rows = append(rows, webRow{kind, state, now, next})
	}
	if len(rows) != 16 {
		return nil, fmt.Errorf("read %d pairs of the web table; want 16", len(rows))
	}
	return rows, nil
}

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestOperationWordsMatchWeb: the CLI's table says, for each of the 16 pairs, what the web client's table
// says, word for word; the web table is read from its source, and the kinds and states are the server's.
func TestOperationWordsMatchWeb(t *testing.T) {
	web, err := webWords(readSource(t, webWordsPath))
	if err != nil {
		t.Fatal(err)
	}
	api := readSource(t, apiOpsPath)
	kinds, states := goStrings(t, api, "operationKinds"), goStrings(t, api, "operationStates")
	count := 0
	for _, r := range web {
		if !slices.Contains(kinds, r.kind) || !slices.Contains(states, r.state) {
			t.Errorf("web pair %s %s is not a kind and state of the server", r.kind, r.state)
		}
		got, ok := operationRow(r.kind, r.state)
		if !ok || got.now != r.now || got.next != r.next {
			t.Errorf("%s %s: CLI has %+v (known %v), web has now %q next %q", r.kind, r.state, got, ok, r.now, r.next)
		}
		count++
	}
	cli := 0
	for _, byState := range operationWords {
		cli += len(byState)
	}
	if cli != count {
		t.Errorf("the CLI table holds %d pairs, the web table %d", cli, count)
	}
	for kind := range operationWords {
		if !slices.Contains(kinds, kind) {
			t.Errorf("CLI kind %s is not a kind of the server", kind)
		}
	}
}

// TestWebWordsReaderRefusesWhatItCannotRead: the reader fails on a table it cannot take whole, so a match
// that read nothing never passes for a table. Each case changes a copy of the real source.
func TestWebWordsReaderRefusesWhatItCannotRead(t *testing.T) {
	src := readSource(t, webWordsPath)
	if _, err := webWords(src); err != nil {
		t.Fatalf("the real source: %v", err)
	}
	for _, c := range []struct{ name, old, new string }{
		{"a row removed", `    queued: ["Accepted; no worker has taken it yet.", "Wait; nothing to do."],` + "\n", ""},
		{"a template string", `["Accepted; no worker has taken it yet.", "Wait; nothing to do."]`, "[`Accepted; no worker has taken it yet.`, \"Wait; nothing to do.\"]"},
		{"a single-quoted string", `"Wait for the controller."],` + "\n    sending", `'Wait for the controller.'],` + "\n    sending"},
		{"an empty sentence", `["Accepted; no worker has taken it yet.",`, `["",`},
		// A row in a form the whole-row pattern cannot take, added beside a real one: it must be counted and refused.
		{"a quoted state key", "  ingest: {\n", "  ingest: {\n    \"queued\": [\"New web sentence.\", \"New web step.\"],\n"},
		{"a row in another layout", "  ingest: {\n", "  ingest: {\n    queued: [\"New web sentence.\",\n      \"New web step.\"],\n"},
		{"a row with extra indentation", "  ingest: {\n", "  ingest: {\n      queued: [\"New web sentence.\", \"New web step.\"],\n"},
		{"a row at no indentation", "  ingest: {\n", "  ingest: {\nqueued: [\"New web sentence.\", \"New web step.\"],\n"},
		{"a pair after the declaration's brace", "undefined]>> = {\n", "undefined]>> = { extra: { queued: [\"Hidden sentence.\", undefined] },\n"},
		{"a carriage return ending a comment", "  ingest: {\n", "  ingest: {\n    // comment\r    queued: [\"Hidden sentence.\", undefined],\n"},
		{"a commented copy of the table", "const KNOWN", "/* const KNOWN */\nconst KNOWN"},
		{"a block comment", "  ingest: {\n", "  ingest: {\n    /* note */\n"},
		{"a second declaration", "const known =", "const KNOWN = {};\nconst known ="},
		{"a kind added", "  adopt: {\n", "  adopt2: {\n    queued: [\"New web sentence.\", undefined],\n  },\n  adopt: {\n"},
	} {
		if !strings.Contains(src, c.old) {
			t.Fatalf("%s: the case no longer matches the source", c.name)
		}
		if _, err := webWords(strings.Replace(src, c.old, c.new, 1)); err == nil {
			t.Errorf("%s: the reader took the changed table", c.name)
		}
	}
}

func wordsOp(id, kind, state, subject, createdBy string) string {
	return `{"id":"` + id + `","kind":"` + kind + `","state":"` + state + `","epoch":"ep_BWSYNTH-1","lastEvent":2,"subject":` + subject +
		`,"createdBy":` + createdBy + `,"createdAt":"2026-11-19T10:00:00Z","result":null,"error":null}`
}

func wordsSubject(kind string) string {
	switch kind {
	case "publish":
		return `{"draft":"drf_a","draftRevision":3}`
	case "ingest":
		return `{"draft":"drf_a","draftRevision":3,"ingestion":"ing_a"}`
	}
	return `{"plan":"pln_BWSYNTH","machine":"mch_a"}`
}

func wordsServer(t *testing.T, kind, state, createdBy string) func() []string {
	t.Helper()
	return titleServer(t, func(a map[string]string) {
		op := wordsOp("op_x", kind, state, wordsSubject(kind), createdBy)
		a["GET /api/v1/operations"] = titleItems(op)
		a["GET /api/v1/operations/op_x"] = op
	})
}

var nobody = `{"principal":"usr_ada","role":"author"}`

// TestOperationShowWords: operation show prints the sentence for each of the 16 pairs, and the table's next
// step as a Next row for a state whose outcome carries none; an operation that ended without success, or is
// unresolved, has its outcome's next step alone.
func TestOperationShowWords(t *testing.T) {
	rows := 0
	for kind, byState := range operationWords {
		for state, w := range byState {
			rows++
			t.Run(kind+"-"+state, func(t *testing.T) {
				wordsServer(t, kind, state, `null`)
				code, out, errOut := runCtl(t, "operation", "show", "op_x")
				if code != 0 || !regexp.MustCompile(`(?m)^Now +`+regexp.QuoteMeta(w.now)+`$`).MatchString(out) {
					t.Fatalf("exit %d, want the Now row %q in\n%s\nerr\n%s", code, w.now, out, errOut)
				}
				hasRow := regexp.MustCompile(`(?m)^Next +`).MatchString(out)
				switch {
				case endsWithItsOwnNext(state):
					if hasRow {
						t.Errorf("a Next row beside the outcome's own next step:\n%s", out)
					}
				case w.next == "":
					if hasRow {
						t.Errorf("a Next row for a pair with no next step:\n%s", out)
					}
				case !regexp.MustCompile(`(?m)^Next +` + regexp.QuoteMeta(w.next) + `$`).MatchString(out):
					t.Errorf("want the Next row %q in\n%s", w.next, out)
				}
			})
		}
	}
	if rows != 16 {
		t.Errorf("ran %d pairs; want 16", rows)
	}
}

// TestOperationShowNextStepHasOneOwner: an apply-config that failed shows one next-step line, the outcome's
// own; a failed publish shows the table's text and no line that sends the reader to operation show.
func TestOperationShowNextStepHasOneOwner(t *testing.T) {
	wordsServer(t, "apply-config", "failed", `null`)
	_, out, _ := runCtl(t, "operation", "show", "op_x")
	if strings.Count(out, "create a new plan")+strings.Count(out, "before a new plan") != 1 || strings.Contains(out, "Read the machine's drift") ||
		!strings.Contains(out, "bronzewardctl machine show 'mch_a'") {
		t.Errorf("a failed apply-config:\n%s", out)
	}
	wordsServer(t, "publish", "failed", `null`)
	_, out, _ = runCtl(t, "operation", "show", "op_x")
	if !strings.Contains(out, "Fix the cause named above and publish again; a publish is safe to repeat.") || strings.Contains(out, "operation show") || strings.Contains(out, "Read it:") {
		t.Errorf("a failed publish:\n%s", out)
	}
}

// TestOperationShowUnknownPair: an unknown kind or state, or a name the table could inherit, prints both raw
// values and no next step.
func TestOperationShowUnknownPair(t *testing.T) {
	for _, c := range []struct{ kind, state, want string }{
		{"frobnicate", "sideways", "An operation of kind frobnicate in state sideways; this command has no description of it."},
		{"publish", "sideways", "A publish operation in state sideways; this command has no description of it."},
		{"frobnicate", "running", "An operation of kind frobnicate in state running; this command has no description of it."},
		{"adopt", "queued", "An adopt operation in state queued; this command has no description of it."},
		{"constructor", "toString", "An operation of kind constructor in state toString; this command has no description of it."},
		// A state that ends an operation without success, on a pair the table does not know, gets no command
		// from the outcome either: the server's plan and machine would otherwise make one up.
		{"frobnicate", "failed", "An operation of kind frobnicate in state failed; this command has no description of it."},
		{"frobnicate", "cancelled", "An operation of kind frobnicate in state cancelled; this command has no description of it."},
		{"constructor", "failed", "An operation of kind constructor in state failed; this command has no description of it."},
		{"publish", "rejected", "A publish operation in state rejected; this command has no description of it."},
	} {
		wordsServer(t, c.kind, c.state, `null`)
		code, out, errOut := runCtl(t, "operation", "show", "op_x")
		if code != 0 || !strings.Contains(out, "Now  ") || !strings.Contains(out, c.want) || regexp.MustCompile(`(?m)^Next`).MatchString(out) ||
			strings.Contains(out, "bronzewardctl ") {
			t.Errorf("%s %s: exit %d, want %q and no Next row in\n%s\nerr\n%s", c.kind, c.state, code, c.want, out, errOut)
		}
		if operationNextStep(c.kind, c.state) != "" {
			t.Errorf("%s %s: a next step for an unknown pair", c.kind, c.state)
		}
	}
}

// TestOperationStartedBy: the list has a STARTED BY column and the show names who started the operation: the
// principal and role, or for an apply-config or an adopt the controller created, the controller; show adds the
// plan and the person that created it. Never the plan in the list, never "none" for either of those kinds.
func TestOperationStartedBy(t *testing.T) {
	for _, c := range []struct {
		name, kind, state, createdBy string
		list, show                   string
	}{
		{"a publish by a person", "publish", "succeeded", nobody, `usr_ada \(author\)$`, `by usr_ada \(author\)`},
		{"an ingest by a person", "ingest", "succeeded", nobody, `usr_ada \(author\)$`, `by usr_ada \(author\)`},
		{"an apply the controller made", "apply-config", "completed", `null`, `the controller$`, `by the controller, committing plan pln_BWSYNTH \(created by svc_BWSYNTH \(publisher\)\)`},
		{"an adoption the controller made", "adopt", "completed", `null`, `the controller$`, `by the controller, adopting under plan pln_BWSYNTH \(created by svc_BWSYNTH \(publisher\)\)`},
		{"a publish with no creator", "publish", "succeeded", `null`, `none$`, `by none`},
	} {
		t.Run(c.name, func(t *testing.T) {
			wordsServer(t, c.kind, c.state, c.createdBy)
			code, out, errOut := runCtl(t, "operation", "list")
			if code != 0 || !regexp.MustCompile(`(?m)^OPERATION .*STARTED BY$`).MatchString(out) || !regexp.MustCompile(`(?m)^op_x .*`+c.list).MatchString(out) {
				t.Errorf("list: exit %d, want a STARTED BY cell ending the row with %q (never the plan) in\n%s\nerr\n%s", code, c.list, out, errOut)
			}
			code, out, errOut = runCtl(t, "operation", "show", "op_x")
			if code != 0 || !regexp.MustCompile(`(?m)^Created .*`+c.show).MatchString(out) {
				t.Errorf("show: exit %d, want the Created row to hold %q in\n%s\nerr\n%s", code, c.show, out, errOut)
			}
		})
	}
}

// A plan that cannot be read leaves the controller alone, with no plan named and no failure said.
func TestOperationStartedByPlanUnread(t *testing.T) {
	titleServer(t, func(a map[string]string) {
		op := wordsOp("op_x", "apply-config", "completed", wordsSubject("apply-config"), `null`)
		a["GET /api/v1/operations/op_x"] = op
		delete(a, "GET /api/v1/plans/pln_BWSYNTH")
	})
	code, out, errOut := runCtl(t, "operation", "show", "op_x")
	if code != 0 || !regexp.MustCompile(`(?m)^Created .* by the controller, committing plan pln_BWSYNTH\s`).MatchString(out) || strings.Contains(out, "created by") || errOut != "" {
		t.Errorf("exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

// -o json and -o yaml print the resource unchanged, with no sentence, next step or column, and read neither
// the plan nor the principals.
func TestOperationWordsJSONAndYAMLUnchanged(t *testing.T) {
	var served map[string]string
	seen := titleServer(t, func(a map[string]string) {
		op := wordsOp("op_x", "apply-config", "completed", wordsSubject("apply-config"), `null`)
		a["GET /api/v1/operations"] = titleItems(op)
		a["GET /api/v1/operations/op_x"] = op
		served = a
	})
	for _, args := range [][]string{{"operation", "list"}, {"operation", "show", "op_x"}} {
		key := "GET /api/v1/operations"
		if args[1] == "show" {
			key += "/op_x"
		}
		code, out, errOut := runCtl(t, append(args, "-o", "json")...)
		var got, want any
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil || json.Unmarshal([]byte(served[key]), &want) != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%v -o json: exit %d, out\n%s\nwant the served resource\n%s\nerr\n%s", args, code, out, served[key], errOut)
		}
		code, out, errOut = runCtl(t, append(args, "-o", "yaml")...)
		if code != 0 || !strings.Contains(out, "id: op_x") || strings.Contains(out, "Now") || strings.Contains(out, "Next") || strings.Contains(out, "STARTED BY") || errOut != "" {
			t.Errorf("%v -o yaml: exit %d, out\n%s\nerr\n%s", args, code, out, errOut)
		}
	}
	for _, p := range []string{"/plans/pln_BWSYNTH", "/principals"} {
		if n := reads(seen(), p); n != 0 {
			t.Errorf("-o json and yaml read %s %d times; want none", p, n)
		}
	}
}
