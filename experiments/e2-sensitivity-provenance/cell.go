package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// A cell is one case on one base, as run/all leaves it:
//
//	derived/                   bwprov derive's output
//	base.yaml                  the normalized base
//	real/<form>/               form: literal (the reference-free form), tag, marked, binding
//	    frag/<fragment>        the fragment each resolved on its own (literal: as generated)
//	    res/<fragment>.tsv     bwref's resolution records: ref, document, path
//	    resolve.rc             bwref's exit status (absent: 0)
//	    out.yaml               talosctl's composition, when it composed
//	    patch.err, patch.rc    talosctl machineconfig patch: its messages and exit status
//	    validate.txt, .rc      talosctl validate --strict (literal and tag only, when composed)
//	trace/tag/                 the same for the trace pass (tag only), plus
//	    prefix-<j>/out.yaml    the composition of fragments 1..j, for j below the fragment count
//	flip-<id>/tag/             the trace pass with boolean tracer <id> flipped
//
// Every talosctl run of real/tag and trace/tag happens from its own directory, with the same
// relative paths, so that their messages can differ only by value.
type cell struct {
	dir       string
	caseDef   issue3Case // bwrefCase's fields; yaml.v3 decodes a value into yaml.Node, not *yaml.Node
	tracers   []Tracer
	secrets   []Secret
	occs      []occRow
	info      []info
	fragments []string
}

type occRow struct {
	index                         int
	ref, version, modifier        string
	fragment, sha, doc, path, res string
}

type info struct{ key, a, b string }

type step struct {
	ran bool
	rc  int
	msg string
}

func loadCell(dir string) (*cell, error) {
	c := &cell{dir: dir}
	if err := decodeStrict(filepath.Join(dir, "derived", "real", "case", "case.yaml"), &c.caseDef); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "derived", "tracers.json"), &c.tracers); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "derived", "secrets.json"), &c.secrets); err != nil {
		return nil, err
	}
	rows, err := readTSV(filepath.Join(dir, "derived", "occurrences.tsv"))
	if err != nil {
		return nil, err
	}
	for _, r := range rows[1:] {
		if len(r) != 9 {
			return nil, fmt.Errorf("occurrences.tsv: malformed row %v", r)
		}
		i, err := strconv.Atoi(r[0])
		if err != nil {
			return nil, err
		}
		c.occs = append(c.occs, occRow{i, r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8]})
	}
	rows, err = readTSV(filepath.Join(dir, "derived", "info.tsv"))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		in := info{key: r[0]}
		if len(r) > 1 {
			in.a = r[1]
		}
		if len(r) > 2 {
			in.b = r[2]
		}
		c.info = append(c.info, in)
		if in.key == "fragment" {
			c.fragments = append(c.fragments, in.a)
		}
	}
	return c, nil
}

func (c *cell) infoValue(key, a string) (string, bool) {
	for _, in := range c.info {
		if in.key == key && in.a == a {
			return in.b, true
		}
		if in.key == key && a == "" {
			return in.a, true
		}
	}
	return "", false
}

func (c *cell) path(parts ...string) string {
	return filepath.Join(append([]string{c.dir}, parts...)...)
}

func (c *cell) read(parts ...string) (string, bool) {
	b, err := os.ReadFile(c.path(parts...))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// run reads one talosctl step: its message file and exit status file.
func (c *cell) run(dir, msgFile, rcFile string) step {
	rcText, ok := c.read(dir, rcFile)
	if !ok {
		return step{}
	}
	rc, err := strconv.Atoi(strings.TrimSpace(rcText))
	if err != nil {
		rc = -1
	}
	msg, _ := c.read(dir, msgFile)
	return step{ran: true, rc: rc, msg: msg}
}

// composed is the configuration a pass produced, if it produced one.
func (c *cell) composed(dir string) (string, bool) {
	s := c.run(dir, "patch.err", "patch.rc")
	if !s.ran || s.rc != 0 {
		return "", false
	}
	return c.read(dir, "out.yaml")
}

// canonical is the fragments as the author wrote them, every reference a `!ref <name>`: text that
// holds no referenced value, except what the author also wrote literally.
func (c *cell) canonical() string {
	var b strings.Builder
	for _, f := range c.fragments {
		t, _ := c.read("derived", "real", "case", f)
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return b.String()
}

func (c *cell) tracer(id int) Tracer {
	for _, t := range c.tracers {
		if t.ID == id {
			return t
		}
	}
	return Tracer{ID: id, Ref: "?"}
}

func (c *cell) version(ref string) string {
	for _, o := range c.occs {
		if o.ref == ref {
			return o.version
		}
	}
	return "1"
}

func tracerToken(t Tracer) string {
	s := "<redacted:" + t.Ref + "@" + t.Version
	if t.Leaf != "" {
		s += "#" + t.Leaf
	}
	return s + ">"
}

// A record is one bwref resolution: where a reference was placed in a fragment.
type record struct {
	fragment, ref, doc, path string
}

func (c *cell) records(dir string) ([]record, error) {
	var out []record
	for _, f := range c.fragments {
		rows, err := readTSV(c.path(dir, "res", f+".tsv"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, r := range rows {
			if len(r) != 3 {
				return nil, fmt.Errorf("%s/res/%s.tsv: malformed row %v", dir, f, r)
			}
			out = append(out, record{f, r[0], r[1], r[2]})
		}
	}
	return out, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func readTSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][]string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		if s.Text() == "" {
			continue
		}
		out = append(out, strings.Split(s.Text(), "\t"))
	}
	return out, s.Err()
}

// readBaseSecrets reads `<key>\t<value>` lines: the base's secrets-bundle values.
func readBaseSecrets(path string) ([]Secret, error) {
	rows, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var out []Secret
	for i, r := range rows {
		if len(r) != 2 || r[1] == "" {
			return nil, fmt.Errorf("%s: malformed line %d", path, i+1)
		}
		out = append(out, Secret{ID: fmt.Sprintf("base:%s%d", strings.TrimSuffix(r[0], ":"), i), Class: "base", Kind: "str", Value: r[1]})
	}
	return out, nil
}

func list(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	t := append([]string(nil), s...)
	sort.Strings(t)
	var u []string
	for i, x := range t {
		if i == 0 || x != t[i-1] {
			u = append(u, x)
		}
	}
	return strings.Join(u, ",")
}
