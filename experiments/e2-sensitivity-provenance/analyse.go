package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// A rep is one representation of sensitivity the experiment compares (issue 4's alternatives):
//
//	none               nothing redacted: the sensitivity control, every value must show
//	value              the values the resolver returned (and the base's secrets bundle), matched
//	                   as text: the baseline design 6.9 calls insufficient
//	resolution-path    the paths bwref recorded when it resolved each fragment, applied as they
//	                   are to what talosctl composed
//	schema             the fields the Talos machinery's types mark secret
//	composed-path      the tracer pass: each output leaf attributed to the reference occurrence it
//	                   came from, through talosctl's own composition; messages by template
//	path+schema        composed-path and schema together
//	path+schema+value  both, and value matching over what they leave
type rep struct {
	name                                string
	value, resolution, composed, schema bool
}

var reps = []rep{
	{name: "none"},
	{name: "value", value: true},
	{name: "resolution-path", resolution: true},
	{name: "schema", schema: true},
	{name: "composed-path", composed: true},
	{name: "path+schema", composed: true, schema: true},
	{name: "path+schema+value", composed: true, schema: true, value: true},
}

func (r rep) paths() bool { return r.resolution || r.composed || r.schema }

// baseLeak is what every cell expects of the base's own secrets: a representation that knows
// neither the schema nor the secrets bundle leaves them in the support data.
var baseLeak = map[string]string{"none": "leak", "value": "clean", "resolution-path": "leak", "schema": "clean",
	"composed-path": "leak", "path+schema": "clean", "path+schema+value": "clean"}

var artifactNames = []string{"diff", "errors", "log", "support"}

const pairedToken = "<redacted:paired>"

// A view is one configuration text with every representation's token for each of its leaves.
type view struct {
	text      string
	tree      *Tree
	comp      map[string]string
	res       map[string]string
	schema    map[string]bool
	schemaErr error
}

func newView(text string, emb []Embedded) (*view, error) {
	t, err := parseTree([]byte(text), emb)
	if err != nil {
		return nil, err
	}
	v := &view{text: text, tree: t, comp: map[string]string{}, res: map[string]string{}, schema: map[string]bool{}}
	set, err := schemaPaths([]byte(text))
	if err != nil {
		v.schemaErr = err
		return v, nil
	}
	for _, l := range t.Leaves {
		if set[l.Doc+" "+l.Path] {
			v.schema[l.Pos] = true
		}
	}
	return v, nil
}

func (v *view) tokens(r rep) map[string]string {
	t := map[string]string{}
	if r.schema {
		for pos := range v.schema {
			t[pos] = "<redacted:schema>"
		}
	}
	if r.resolution {
		for pos, tok := range v.res {
			t[pos] = tok
		}
	}
	if r.composed {
		for pos, tok := range v.comp {
			t[pos] = tok
		}
	}
	return t
}

func pathUnder(p, q string) bool {
	return p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(p, q+"|") || strings.HasPrefix(p, q+"[")
}

// setResolution applies resolution records as they are: every leaf at or under a recorded path.
func (v *view) setResolution(recs []record, version func(string) string) {
	for _, l := range v.tree.Leaves {
		for _, r := range recs {
			if l.Doc == r.doc && pathUnder(stripIndex(l.Path), r.path) {
				v.res[l.Pos] = "<redacted:" + r.ref + "@" + version(r.ref) + ">"
			}
		}
	}
}

func (v *view) setComposed(attrs []Attribution, c *cell) {
	for _, a := range attrs {
		var toks []string
		for _, id := range a.Tracers {
			toks = append(toks, tracerToken(c.tracer(id)))
		}
		v.comp[a.Pos] = strings.Join(toks, "")
	}
}

// pair extends two sides' tokens so that a leaf sensitive on one side is redacted on the other at
// the same path too: a diff that showed `wipe: false` against `wipe: <redacted>` would disclose a
// boolean. It returns how many leaves only the pairing redacted.
func pair(a, b *view, ta, tb map[string]string) int {
	n := 0
	extend := func(x, y *view, tx, ty map[string]string) {
		sens := map[string]bool{}
		for _, l := range y.tree.Leaves {
			if _, ok := ty[l.Pos]; ok {
				sens[l.Doc+" "+l.Path] = true
			}
		}
		for _, l := range x.tree.Leaves {
			if _, ok := tx[l.Pos]; !ok && sens[l.Doc+" "+l.Path] {
				tx[l.Pos] = pairedToken
				n++
			}
		}
	}
	ca, cb := copyTokens(ta), copyTokens(tb)
	extend(a, b, ta, cb)
	extend(b, a, tb, ca)
	return n
}

func copyTokens(t map[string]string) map[string]string {
	c := make(map[string]string, len(t))
	for k, v := range t {
		c[k] = v
	}
	return c
}

// show renders a view under a representation with the given tokens.
func show(v *view, r rep, tokens map[string]string, emb []Embedded, vm *valueMatcher) (string, int, error) {
	text := v.text
	if r.paths() {
		b, err := render([]byte(v.text), emb, tokens)
		if err != nil {
			return "", 0, err
		}
		text = string(b)
	}
	n := 0
	if r.value {
		text, n = vm.redact(text)
	}
	return text, n, nil
}

// udiff is GNU diff's unified form of two texts, as the operator would see a change.
func udiff(dir, la, lb, a, b string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fa, fb := filepath.Join(dir, la+".yaml"), filepath.Join(dir, lb+".yaml")
	if err := os.WriteFile(fa, []byte(a), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(fb, []byte(b), 0o644); err != nil {
		return "", err
	}
	out, err := exec.Command("diff", "-U3", "--label", la, "--label", lb, "--", fa, fb).Output()
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
		err = nil
	}
	return string(out), err
}

type table struct {
	b strings.Builder
}

func (t *table) row(cells ...string) {
	for i, c := range cells {
		if c == "" {
			c = "-"
		}
		if i > 0 {
			t.b.WriteByte('\t')
		}
		t.b.WriteString(strings.NewReplacer("\t", " ", "\n", " ").Replace(c))
	}
	t.b.WriteByte('\n')
}

// analyse measures one cell: provenance through composition, every representation's redacted diff,
// errors, log and support data, what the oracle finds in each, the cell's expectations and its
// controls. It writes <cell>/analysis/ and <artifacts>/<rep>/<case>/<base>/.
// An output is a cell's real composition with its provenance: the tracer attribution of every
// leaf (direct: through the trace pass; all: with the flip passes' booleans), and every way the
// trace pass failed to reproduce it.
type output struct {
	view        *view
	direct, all []Attribution
	fidelity    []string
}

func (c *cell) output(emb []Embedded, recs []record) (*output, error) {
	o := &output{}
	realText, realOK := c.composed("real/tag")
	traceText, traceOK := c.composed("trace/tag")
	if realOK != traceOK {
		o.fidelity = append(o.fidelity, fmt.Sprintf("the real pass composed: %v, the trace pass: %v", realOK, traceOK))
	}
	if !realOK {
		return o, nil
	}
	out, err := newView(realText, emb)
	if err != nil {
		return nil, fmt.Errorf("real output: %w", err)
	}
	o.view = out
	out.setResolution(recs, c.version)
	if !traceOK {
		return o, nil
	}
	trace, err := parseTree([]byte(traceText), emb)
	if err != nil {
		return nil, fmt.Errorf("trace output: %w", err)
	}
	attrs, fails := attribute(out.tree, trace, c.tracers)
	o.fidelity = append(o.fidelity, fails...)
	o.direct = attrs
	o.all = append([]Attribution(nil), attrs...)
	for _, t := range c.tracers {
		if t.Kind != "bool" {
			continue
		}
		flipText, ok := c.composed(fmt.Sprintf("flip-%d/tag", t.ID))
		if !ok {
			o.fidelity = append(o.fidelity, fmt.Sprintf("flip pass %d did not compose", t.ID))
			continue
		}
		flip, err := parseTree([]byte(flipText), emb)
		if err != nil {
			return nil, err
		}
		fa, ff := attributeFlip(trace, flip, t)
		o.all = append(o.all, fa...)
		o.fidelity = append(o.fidelity, ff...)
	}
	out.setComposed(o.all, c)
	return o, nil
}

func analyse(dir, baseSecretsPath, artifacts, baseName, caseName string) error {
	c, err := loadCell(dir)
	if err != nil {
		return err
	}
	baseSecrets, err := readBaseSecrets(baseSecretsPath)
	if err != nil {
		return err
	}
	emb := c.caseDef.Embedded
	baseText, ok := c.read("base.yaml")
	if !ok {
		return fmt.Errorf("%s: no base.yaml", dir)
	}
	// The oracle's public text: the base and the authored fragments. A twelve-byte window of a value
	// that either also holds is not evidence of that value (a whole value always is).
	canonical := c.canonical()
	public := baseText + "\n" + canonical
	all := append(append([]Secret(nil), c.secrets...), baseSecrets...)
	var values []string
	for _, s := range all {
		if s.Kind == "str" || s.Kind == "bytes" {
			values = append(values, s.Value)
		}
	}
	vm := newValueMatcher(values)
	version := c.version
	ana := c.path("analysis")
	if err := os.MkdirAll(ana, 0o755); err != nil {
		return err
	}
	var controls, expectations, leaks table
	control := func(name, result, detail string) { controls.row(baseName, caseName, name, result, detail) }
	expect := func(item, want, got string) {
		m := "yes"
		if want != got {
			m = "no"
		}
		expectations.row(baseName, caseName, item, want, got, m)
	}

	// Views: the base, the real composition and the resolved fragments.
	base, err := newView(baseText, emb)
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}
	_, realOK := c.composed("real/tag")
	traceText, traceOK := c.composed("trace/tag")
	realRecs, err := c.records("real/tag")
	if err != nil {
		return err
	}
	traceRecs, err := c.records("trace/tag")
	if err != nil {
		return err
	}
	o, err := c.output(emb, realRecs)
	if err != nil {
		return err
	}
	out, outAttrs, fidelity := o.view, o.all, o.fidelity
	if realOK {
		if traceOK {
			// Control: the fidelity check must fire when the trace pass does not carry a tracer where
			// the real pass differs.
			result := "not-applicable"
			for _, a := range o.direct {
				t := c.tracer(a.Tracers[0])
				if t.Kind != "str" {
					continue
				}
				corrupt, err := render([]byte(traceText), emb, map[string]string{a.Pos: "zz-corrupted-tracer"})
				if err != nil {
					return err
				}
				ct, err := parseTree(corrupt, emb)
				if err != nil {
					return err
				}
				result = "did-not-fire"
				if _, f := attribute(out.tree, ct, c.tracers); len(f) > 0 {
					result = "fired"
				}
				break
			}
			control("fidelity-check-fires", result, "a trace leaf's tracer replaced by a non-tracer")
		}
	}

	// Resolved fragments: resolution records and tracer attribution apply to each on its own.
	type fragView struct {
		name string
		v    *view
	}
	var frags []fragView
	for _, f := range c.fragments {
		rt, ok := c.read("real/tag/frag", f)
		if !ok {
			continue
		}
		v, err := newView(rt, emb)
		if err != nil {
			return fmt.Errorf("fragment %s: %w", f, err)
		}
		var recs []record
		for _, r := range realRecs {
			if r.fragment == f {
				recs = append(recs, r)
			}
		}
		v.setResolution(recs, version)
		tt, ok := c.read("trace/tag/frag", f)
		if !ok {
			fidelity = append(fidelity, "fragment "+f+": the trace pass left no resolved fragment")
		} else {
			tr, err := parseTree([]byte(tt), emb)
			if err != nil {
				return err
			}
			attrs, fails := attribute(v.tree, tr, c.tracers)
			for i := range fails {
				fails[i] = "fragment " + f + ": " + fails[i]
			}
			fidelity = append(fidelity, fails...)
			for _, t := range c.tracers {
				if t.Kind != "bool" {
					continue
				}
				ft, ok := c.read(fmt.Sprintf("flip-%d/tag/frag", t.ID), f)
				if !ok {
					fidelity = append(fidelity, fmt.Sprintf("fragment %s: flip pass %d left no resolved fragment", f, t.ID))
				} else {
					fl, err := parseTree([]byte(ft), emb)
					if err != nil {
						return err
					}
					fa, ff := attributeFlip(tr, fl, t)
					attrs = append(attrs, fa...)
					fidelity = append(fidelity, ff...)
				}
			}
			v.setComposed(attrs, c)
		}
		frags = append(frags, fragView{f, v})
	}

	// Controls on the inputs.
	identity := "pass"
	for _, v := range append([]*view{base, out}, func() []*view {
		var vs []*view
		for _, f := range frags {
			vs = append(vs, f.v)
		}
		return vs
	}()...) {
		if v == nil {
			continue
		}
		if b, err := render([]byte(v.text), emb, nil); err != nil || string(b) != v.text {
			identity = "fail"
		}
	}
	control("render-identity", identity, "every configuration re-rendered with nothing redacted is the text talosctl or bwref wrote")
	strip := func(recs []record) []string {
		var s []string
		for _, r := range recs {
			ref := r.ref
			if i := strings.LastIndex(ref, "~"); i >= 0 {
				ref = ref[:i]
			}
			s = append(s, r.fragment+" "+ref+" "+r.doc+" "+r.path)
		}
		sort.Strings(s)
		return s
	}
	if strings.Join(strip(realRecs), "\n") == strings.Join(strip(traceRecs), "\n") {
		control("resolution-records-match", "pass", fmt.Sprintf("%d records", len(realRecs)))
	} else {
		control("resolution-records-match", "fail", "the trace pass resolved other paths than the real one")
	}
	// What the trace pass may hold of a case value: a boolean (its tracer is the value itself; the
	// flip pass attributes it) and whatever the author wrote literally in the fragments. Every other
	// form of a value in it would mean the tracers did not replace it.
	var refSecrets []Secret
	exempt := 0
	for _, s := range c.secrets {
		if s.Kind == "bool" || strings.Contains(canonical, s.Value) {
			exempt++
			continue
		}
		refSecrets = append(refSecrets, s)
	}
	traceTexts := []string{traceText}
	for _, f := range c.fragments {
		t, _ := c.read("trace/tag/frag", f)
		traceTexts = append(traceTexts, t)
	}
	for _, f := range []string{"patch.err", "validate.txt"} {
		t, _ := c.read("trace/tag", f)
		traceTexts = append(traceTexts, t)
	}
	if n := len(scan(strings.Join(traceTexts, "\n"), refSecrets, public)); n == 0 {
		control("trace-holds-no-secret", "pass", fmt.Sprintf("the trace pass's compositions, fragments and messages hold no form of %d case values (%d exempt: booleans, authored literals)", len(refSecrets), exempt))
	} else {
		control("trace-holds-no-secret", "fail", fmt.Sprintf("%d findings", n))
	}
	if n := len(scan(baseText, refSecrets, "")); n == 0 {
		control("base-holds-no-case-secret", "pass", "")
	} else {
		control("base-holds-no-case-secret", "fail", fmt.Sprintf("%d findings", n))
	}
	uncovered := 0
	for _, l := range base.tree.Leaves {
		for _, s := range baseSecrets {
			if l.Node.Value == s.Value && !base.schema[l.Pos] {
				uncovered++
			}
		}
	}
	switch {
	case base.schemaErr != nil:
		control("schema-covers-base-secrets", "fail", base.schemaErr.Error())
	case len(base.schema) == 0 || uncovered > 0:
		control("schema-covers-base-secrets", "fail", fmt.Sprintf("%d schema leaves, %d secrets-bundle values outside them", len(base.schema), uncovered))
	default:
		control("schema-covers-base-secrets", "pass", fmt.Sprintf("%d schema leaves hold every secrets-bundle value", len(base.schema)))
	}

	// Messages: the patch and validate steps of the real and the trace pass.
	type msgStep struct {
		name, file, rc string
		real, trace    step
	}
	steps := []*msgStep{{name: "patch", file: "patch.err", rc: "patch.rc"}, {name: "validate", file: "validate.txt", rc: "validate.rc"}}
	for _, s := range steps {
		s.real = c.run("real/tag", s.file, s.rc)
		s.trace = c.run("trace/tag", s.file, s.rc)
	}
	tokenFor := func(id int) string { return tracerToken(c.tracer(id)) }
	message := func(r rep, s *msgStep) (string, string) {
		if !s.real.ran {
			return "", "skipped"
		}
		text, outcome := s.real.msg, "raw"
		if r.composed {
			trc := s.trace.rc
			if !s.trace.ran {
				trc = -2
			}
			text, outcome = redactMessage(s.real.msg, s.trace.msg, s.real.rc, trc, c.tracers, tokenFor)
		}
		if r.value {
			text, _ = vm.redact(text)
		}
		return text, outcome
	}
	for _, s := range steps {
		if _, o := message(reps[4], s); o == "redacted" {
			trc := s.trace.rc
			_, got := redactMessage("E2SP-CONTROL "+s.real.msg, s.trace.msg, s.real.rc, trc, c.tracers, tokenFor)
			result := "did-not-fire"
			if got == "withheld" {
				result = "fired"
			}
			control("template-check-fires-"+s.name, result, "a real message with text its template does not have")
		}
	}

	// Provenance: per tracer, where it ended up.
	var prov table
	prov.row("occurrence", "ref", "version", "modifier", "leaf", "fragment", "fragment-sha256", "source-doc", "source-path",
		"status", "output-doc", "output-path", "overridden-by")
	fragIndex := map[string]int{}
	for i, f := range c.fragments {
		fragIndex[f] = i + 1
	}
	status := map[string]map[string]bool{"effective": {}, "overridden": {}, "unresolved": {}}
	var effectiveDeps, reproDeps []string
	for _, o := range c.occs {
		reproDeps = append(reproDeps, o.ref+"@"+o.version)
		base := []string{strconv.Itoa(o.index), o.ref, o.version, o.modifier}
		src := []string{o.fragment, o.sha, o.doc, o.path}
		if o.res == "no" {
			prov.row(append(append(append(base, "-"), src...), "unresolved", "-", "-", "-")...)
			status["unresolved"][o.ref] = true
			continue
		}
		for _, t := range c.tracers {
			if t.Occ != o.index {
				continue
			}
			row := append(append(append([]string(nil), base...), t.Leaf), src...)
			if !traceOK {
				prov.row(append(row, "not-composed", "-", "-", "-")...)
				continue
			}
			var at []Attribution
			for _, a := range outAttrs {
				for _, id := range a.Tracers {
					if id == t.ID {
						at = append(at, a)
					}
				}
			}
			if len(at) > 0 {
				for _, a := range at {
					prov.row(append(row, "effective", a.Doc, a.Path, "-")...)
				}
				status["effective"][o.ref] = true
				effectiveDeps = append(effectiveDeps, o.ref+"@"+o.version)
				continue
			}
			prov.row(append(row, "overridden", "-", "-", overriddenBy(c, t, fragIndex[o.fragment], emb))...)
			status["overridden"][o.ref] = true
		}
	}
	var deps table
	deps.row("record", "reference", "fragments")
	for _, d := range uniq(effectiveDeps) {
		deps.row("effective", d, "-")
	}
	for _, d := range uniq(reproDeps) {
		var fs []string
		for _, o := range c.occs {
			if o.ref+"@"+o.version == d {
				fs = append(fs, o.fragment+"@"+o.sha[:12])
			}
		}
		deps.row("reproduction", d, list(fs))
	}

	// Artifacts per representation.
	var logRecords []string
	for _, r := range realRecs {
		logRecords = append(logRecords, fmt.Sprintf("resolve fragment=%s ref=%s@%s doc=%s path=%s\n", r.fragment, r.ref, version(r.ref), r.doc, r.path))
	}
	keys := func(m map[string]bool) string {
		var s []string
		for k := range m {
			s = append(s, k)
		}
		return list(s)
	}
	provSummary := fmt.Sprintf("provenance effective=%s overridden=%s unresolved=%s\n",
		keys(status["effective"]), keys(status["overridden"]), keys(status["unresolved"]))
	refLeak := map[string]string{}
	baseLeaked := map[string]string{}
	outcomes := map[string]string{}
	pairedCount, combinedSubs := 0, 0
	for _, r := range reps {
		texts := map[string]string{}
		// diff
		failClosed := r.composed && len(fidelity) > 0
		var subs int
		switch {
		case failClosed:
			texts["diff"] = "<withheld: the tracer pass did not reproduce the composition; see the fidelity failures>\n"
		case out == nil:
			texts["diff"] = fmt.Sprintf("(no composition: talosctl machineconfig patch exited %d)\n", steps[0].real.rc)
		default:
			ta, tb := base.tokens(r), out.tokens(r)
			n := 0
			if r.paths() {
				n = pair(base, out, ta, tb)
			}
			if r.name == "composed-path" {
				pairedCount = n
				if n > 0 {
					// Control: without the pairing, the diff shows the base's value where the output's
					// is redacted.
					sa, _, _ := show(base, r, base.tokens(r), emb, vm)
					sb, _, _ := show(out, r, out.tokens(r), emb, vm)
					d, err := udiff(filepath.Join(ana, "render", "per-side"), "base", "output", sa, sb)
					if err != nil {
						return err
					}
					exposed := 0
					for _, l := range base.tree.Leaves {
						if _, ok := ta[l.Pos]; ok && ta[l.Pos] == pairedToken && strings.Contains(d, "-") &&
							strings.Contains(d, lastKey(l.Path)+": "+l.Node.Value) {
							exposed++
						}
					}
					result := "did-not-fire"
					if exposed > 0 {
						result = "fired"
					}
					control("per-side-diff-exposes-base-value", result, fmt.Sprintf("%d base leaves shown beside a redacted output leaf without pairing", exposed))
				}
			}
			sa, s1, err := show(base, r, ta, emb, vm)
			if err != nil {
				return err
			}
			sb, s2, err := show(out, r, tb, emb, vm)
			if err != nil {
				return err
			}
			subs += s1 + s2
			d, err := udiff(filepath.Join(ana, "render", r.name), "base", "output", sa, sb)
			if err != nil {
				return err
			}
			texts["diff"] = d
		}
		// errors
		var eb strings.Builder
		for _, s := range steps {
			text, outcome := message(r, s)
			if r.name == "composed-path" {
				outcomes[s.name] = outcome
			}
			if !s.real.ran {
				fmt.Fprintf(&eb, "--- %s (not run) ---\n", s.name)
				continue
			}
			fmt.Fprintf(&eb, "--- %s (exit %d, %s) ---\n", s.name, s.real.rc, outcome)
			eb.WriteString(text)
			if text != "" && !strings.HasSuffix(text, "\n") {
				eb.WriteByte('\n')
			}
		}
		texts["errors"] = eb.String()
		// log
		var lb strings.Builder
		for _, l := range logRecords {
			lb.WriteString(l)
		}
		fmt.Fprintf(&lb, "compose fragments=%s exit=%d\n", strings.Join(c.fragments, ","), steps[0].real.rc)
		for _, s := range steps {
			text, outcome := message(r, s)
			if !s.real.ran {
				fmt.Fprintf(&lb, "%s not run\n", s.name)
				continue
			}
			fmt.Fprintf(&lb, "%s exit=%d message=%s\n", s.name, s.real.rc, outcome)
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				if line != "" {
					fmt.Fprintf(&lb, "| %s\n", line)
				}
			}
		}
		lb.WriteString(provSummary)
		texts["log"] = lb.String()
		// support
		var sb strings.Builder
		sb.WriteString("### configuration\n")
		switch {
		case failClosed:
			sb.WriteString("<withheld: the tracer pass did not reproduce the composition>\n")
		case out == nil:
			sb.WriteString("(no composition)\n")
		default:
			s, n, err := show(out, r, out.tokens(r), emb, vm)
			if err != nil {
				return err
			}
			subs += n
			sb.WriteString(s)
		}
		for _, f := range frags {
			fmt.Fprintf(&sb, "### fragment %s, resolved\n", f.name)
			if r.schema && f.v.schemaErr != nil {
				// Not the machinery's error: a decode error quotes the value.
				sb.WriteString("(schema redaction unavailable: the Talos machinery cannot load this fragment)\n")
			}
			if failClosed {
				sb.WriteString("<withheld: the tracer pass did not reproduce the resolution>\n")
				continue
			}
			s, n, err := show(f.v, r, f.v.tokens(r), emb, vm)
			if err != nil {
				return err
			}
			subs += n
			sb.WriteString(s)
		}
		sb.WriteString("### provenance\n")
		sb.WriteString(prov.b.String())
		sb.WriteString("### dependencies\n")
		sb.WriteString(deps.b.String())
		sb.WriteString("### errors\n")
		sb.WriteString(texts["errors"])
		sb.WriteString("### log\n")
		sb.WriteString(texts["log"])
		texts["support"] = sb.String()
		if r.name == "path+schema+value" {
			combinedSubs = subs
		}

		adir := filepath.Join(artifacts, r.name, caseName, baseName)
		if err := os.MkdirAll(adir, 0o755); err != nil {
			return err
		}
		refLeak[r.name], baseLeaked[r.name] = "clean", "clean"
		for _, a := range artifactNames {
			if err := os.WriteFile(filepath.Join(adir, a+".txt"), []byte(texts[a]), 0o644); err != nil {
				return err
			}
			for _, f := range scan(texts[a], all, public) {
				leaks.row(baseName, caseName, r.name, a, f.Secret, f.Class, f.Form, strconv.Itoa(f.Count))
				if f.Class == "ref" {
					refLeak[r.name] = "leak"
				} else {
					baseLeaked[r.name] = "leak"
				}
			}
		}
	}

	// Expectations.
	native := "rejected"
	if _, ok := c.composed("real/literal"); ok {
		native = "invalid"
		if v := c.run("real/literal", "validate.txt", "validate.rc"); v.ran && v.rc == 0 {
			native = "pass"
		}
	}
	want, _ := c.infoValue("native", "")
	expect("native", want, native)
	literalText, literalOK := c.composed("real/literal")
	literalErr, _ := c.read("real/literal", "patch.err")
	for _, cand := range candidates {
		got := "differs"
		rc := c.run("real/"+cand, "resolve.err", "resolve.rc")
		candText, candOK := c.composed("real/" + cand)
		candErr, _ := c.read("real/"+cand, "patch.err")
		switch {
		case rc.ran && rc.rc != 0:
			got = "refused"
		case !candOK && !literalOK && candErr == literalErr:
			got = "rejected"
		case candOK && literalOK && candText == literalText:
			got = "parity"
		}
		w, _ := c.infoValue("premise", cand)
		expect("premise."+cand, w, got)
	}
	for _, k := range []string{"effective", "overridden", "unresolved"} {
		w, _ := c.infoValue("expect", k)
		expect(k, w, keys(status[k]))
	}
	for _, s := range steps {
		w, ok := c.infoValue("expect", "message."+s.name)
		if !ok {
			w = "missing"
		}
		expect("message."+s.name, w, outcomes[s.name])
	}
	for _, r := range reps {
		w, ok := c.infoValue("expect", "leak."+r.name)
		if !ok {
			w = "missing"
		}
		expect("leak."+r.name, w, refLeak[r.name])
	}
	for _, r := range reps {
		// With no composition the artifacts hold no configuration, so none of the base's secrets.
		want := baseLeak[r.name]
		if out == nil {
			want = "clean"
		}
		expect("base-leak."+r.name, want, baseLeaked[r.name])
	}
	fid := "ok"
	if len(fidelity) > 0 {
		fid = "fail"
	}
	expect("fidelity", "ok", fid)

	// The cell's measurements.
	missed, extra, compLeaves, resLeaves := 0, 0, 0, 0
	if out != nil {
		compLeaves, resLeaves = len(out.comp), len(out.res)
		for pos := range out.comp {
			if _, ok := out.res[pos]; !ok {
				missed++
			}
		}
		for pos := range out.res {
			if _, ok := out.comp[pos]; !ok {
				extra++
			}
		}
	}
	schemaLeaves := "-"
	if out != nil && out.schemaErr == nil {
		schemaLeaves = strconv.Itoa(len(out.schema))
	}
	transformation, _ := c.infoValue("transformation", "")
	var cellRow table
	cellRow.row("base", "case", "transformation", "occurrences", "tracers", "resolutions", "composed-leaves",
		"resolution-leaves", "resolution-missed", "resolution-extra", "schema-leaves", "paired", "value-substitutions",
		"fidelity", "message-patch", "message-validate")
	cellRow.row(baseName, caseName, transformation, strconv.Itoa(len(c.occs)), strconv.Itoa(len(c.tracers)),
		strconv.Itoa(len(realRecs)), strconv.Itoa(compLeaves), strconv.Itoa(resLeaves), strconv.Itoa(missed),
		strconv.Itoa(extra), schemaLeaves, strconv.Itoa(pairedCount), strconv.Itoa(combinedSubs), fid,
		outcomes["patch"], outcomes["validate"])

	files := map[string]string{
		"provenance.tsv":   prov.b.String(),
		"dependencies.tsv": deps.b.String(),
		"controls.tsv":     controls.b.String(),
		"expectations.tsv": expectations.b.String(),
		"leaks.tsv":        leaks.b.String(),
		"cell.tsv":         cellRow.b.String(),
		"fidelity.txt":     strings.Join(fidelity, "\n") + "\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(ana, name), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// overriddenBy names the fragment after which a tracer no longer appears: the compositions of
// fragment prefixes run/all made (a prefix is talosctl's composition too, not a merge engine).
func overriddenBy(c *cell, t Tracer, own int, emb []Embedded) string {
	if t.Kind == "bool" {
		return "unknown (a boolean is attributed by flipping, in the whole composition only)"
	}
	n := len(c.fragments)
	present := func(j int) (bool, bool) {
		dir := "trace/tag"
		if j < n {
			dir = fmt.Sprintf("trace/tag/prefix-%d", j)
		}
		text, ok := c.composed(dir)
		if !ok {
			return false, false
		}
		tr, err := parseTree([]byte(text), emb)
		if err != nil {
			return false, false
		}
		return len(findTracers(tr, []Tracer{t})[t.ID]) > 0, true
	}
	if p, ok := present(own); !ok || !p {
		return "unknown (absent from the composition of its own fragment's prefix)"
	}
	for j := own + 1; j <= n; j++ {
		p, ok := present(j)
		if !ok {
			return "unknown (a prefix did not compose)"
		}
		if !p {
			return c.fragments[j-1]
		}
	}
	return "unknown"
}

func uniq(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return strings.Split(list(s), ",")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
