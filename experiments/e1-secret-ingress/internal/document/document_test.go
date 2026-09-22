package document

import (
	"strings"
	"testing"
)

// sample is shaped like the parts of a Talos machine configuration this experiment touches: a
// nested mapping, a sequence of file entries, a literal block holding a certificate, and a key
// with a dash in it.
const sample = `version: v1alpha1
machine:
  type: controlplane
  token: abcdef.0123456789abcdef
  ca:
    crt: |
      -----BEGIN CERTIFICATE-----
      MIIB
      -----END CERTIFICATE-----
    key: c2VjcmV0LWtleQ==
  files:
    - path: /var/etc/thing.conf
      content: inline-content
      permissions: 0o644
cluster:
  apiServer:
    extraArgs:
      audit-log-path: /var/log/audit.log
`

func load(t *testing.T, body string) *Document {
	t.Helper()
	d, err := Load([]byte(body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return d
}

// TestPathsAddressEveryScalar pins the addressing scheme. Marks and detection rules are written
// against these strings, so a change here silently retargets every extraction.
func TestPathsAddressEveryScalar(t *testing.T) {
	d := load(t, sample)

	want := []string{
		"doc[0].cluster.apiServer.extraArgs.audit-log-path",
		"doc[0].machine.ca.crt",
		"doc[0].machine.ca.key",
		"doc[0].machine.files[0].content",
		"doc[0].machine.files[0].path",
		"doc[0].machine.files[0].permissions",
		"doc[0].machine.token",
		"doc[0].machine.type",
		"doc[0].version",
	}
	if got := strings.Join(d.Paths(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("Paths() =\n%s\n\nwant\n%s", got, strings.Join(want, "\n"))
	}
}

// TestEveryDocumentInTheStreamIsIndexed is a regression test for the worst kind of defect this
// experiment can have: one that makes a run scan clean because it never looked.
//
// The fixtures' own controlplane.yaml is a multi-document file — Talos 1.13 emits the machine
// configuration and several sibling documents in one stream — and yaml.Unmarshal into a node
// decodes the first and discards the rest, returning no error. A secret in a later document was
// therefore outside every path a mark or a rule could name, while the leak scan reported nothing.
func TestEveryDocumentInTheStreamIsIndexed(t *testing.T) {
	d := load(t, "machine:\n  token: first\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nsecret: second\n")

	want := "doc[0].machine.token,doc[1].apiVersion,doc[1].kind,doc[1].secret"
	if got := strings.Join(d.Paths(), ","); got != want {
		t.Errorf("Paths() = %q, want %q", got, want)
	}

	// The value in the second document must be reachable, and a substitution there must survive
	// re-encoding: a document that could be read and not written would be half-covered.
	if v, ok := d.Get("doc[1].secret"); !ok || v != "second" {
		t.Errorf("the second document's scalar is not addressable (%q, %v)", v, ok)
	}
	if err := d.Replace("doc[1].secret", "bw:ref:kv://x"); err != nil {
		t.Fatalf("Replace in the second document: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	body := string(out)
	if strings.Contains(body, "second") {
		t.Errorf("the second document still holds its value:\n%s", body)
	}
	if !strings.Contains(body, "first") || !strings.Contains(body, "HostnameConfig") {
		t.Errorf("re-encoding lost a document:\n%s", body)
	}
	if !strings.Contains(body, "---") {
		t.Errorf("re-encoding merged the documents into one:\n%s", body)
	}
}

// TestPathsAreSorted matters because extraction order feeds the journal: an unsorted walk would
// make two runs over the same input produce different journals and defeat cross-run comparison.
func TestPathsAreSorted(t *testing.T) {
	d := load(t, sample)
	paths := d.Paths()
	for i := 1; i < len(paths); i++ {
		if paths[i-1] > paths[i] {
			t.Fatalf("Paths() is not sorted: %q comes before %q", paths[i-1], paths[i])
		}
	}
}

// TestGet checks a literal block comes back whole and a missing path is reported rather than
// returning an empty string, which a caller would extract as if it were a secret.
func TestGet(t *testing.T) {
	d := load(t, sample)

	crt, ok := d.Get("doc[0].machine.ca.crt")
	if !ok {
		t.Fatal("doc[0].machine.ca.crt is not addressable")
	}
	if !strings.Contains(crt, "BEGIN CERTIFICATE") || !strings.Contains(crt, "MIIB") {
		t.Errorf("the literal block did not come back whole: %q", crt)
	}

	if v, ok := d.Get("doc[0].machine.ca.absent"); ok {
		t.Errorf("a missing path returned %q as if it existed", v)
	}
}

// TestReplaceSubstitutesAndDropsTheOldStyle covers the substitution itself and the formatting
// consequence: a reference left in literal block style produces a diff that reads as a formatting
// change rather than as the secret having been taken out.
func TestReplaceSubstitutesAndDropsTheOldStyle(t *testing.T) {
	d := load(t, sample)

	if err := d.Replace("doc[0].machine.ca.key", "bw:ref:kv://secret/run-1/doc[0].machine.ca.key"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if err := d.Replace("doc[0].machine.ca.crt", "bw:ref:kv://secret/run-1/doc[0].machine.ca.crt"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	body := string(out)

	for _, gone := range []string{"c2VjcmV0LWtleQ==", "BEGIN CERTIFICATE", "MIIB"} {
		if strings.Contains(body, gone) {
			t.Errorf("the re-encoded document still holds %q:\n%s", gone, body)
		}
	}
	for _, present := range []string{
		"bw:ref:kv://secret/run-1/doc[0].machine.ca.key",
		"bw:ref:kv://secret/run-1/doc[0].machine.ca.crt",
	} {
		if !strings.Contains(body, present) {
			t.Errorf("the re-encoded document does not hold %q:\n%s", present, body)
		}
	}
	// The literal block indicator must be gone with it.
	if strings.Contains(body, "crt: |") {
		t.Errorf("the certificate is still in literal block style:\n%s", body)
	}

	// Everything not replaced must survive unchanged, or a leak scan on the sanitized draft would
	// be reading a document that differs from the input for reasons unrelated to extraction.
	for _, kept := range []string{"controlplane", "/var/etc/thing.conf", "inline-content", "/var/log/audit.log"} {
		if !strings.Contains(body, kept) {
			t.Errorf("the re-encoded document lost %q:\n%s", kept, body)
		}
	}
}

// TestReplaceRejectsAnUnknownPath checks a typo in a mark file fails the run. Silently ignoring it
// would leave the secret in place while the journal recorded a substitution.
func TestReplaceRejectsAnUnknownPath(t *testing.T) {
	d := load(t, sample)
	if err := d.Replace("doc[0].machine.ca.keys", "x"); err == nil {
		t.Fatal("Replace accepted a path that does not exist")
	}
}

// TestRoundTripIsStable checks re-encoding without changes does not perturb the document, so a
// later diff attributes every difference to a substitution.
func TestRoundTripIsStable(t *testing.T) {
	d := load(t, sample)
	first, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	again, err := Load(first)
	if err != nil {
		t.Fatalf("re-loading the re-encoded document: %v", err)
	}
	second, err := again.Bytes()
	if err != nil {
		t.Fatalf("Bytes (second): %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("re-encoding is not stable:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Join(d.Paths(), ",") != strings.Join(again.Paths(), ",") {
		t.Errorf("paths changed across a round trip:\n%v\n%v", d.Paths(), again.Paths())
	}
}

// TestAmbiguousKeysAreUnaddressableAndReported is the guard that keeps a mark from addressing the
// wrong value. A key holding a dot would make `a.b` mean either of two locations, and extracting
// the wrong one is invisible in every piece of evidence this experiment collects.
//
// The subtree is excluded rather than the document refused. Refusing rejected every real Talos
// configuration — machine.nodeLabels carries Kubernetes label keys, where dots are ordinary — and
// the hazard only exists where someone tries to address the path. What remains is a coverage gap,
// so it has to be visible: a caller reporting a clean run reports this beside it.
func TestAmbiguousKeysAreUnaddressableAndReported(t *testing.T) {
	cases := map[string]struct{ body, key string }{
		"a dotted key":    {"machine:\n  ca.crt: x\n  token: t\n", "doc[0].machine: ca.crt"},
		"a bracketed key": {"machine:\n  files[0]: x\n  token: t\n", "doc[0].machine: files[0]"},
		"at the root":     {"a.b: x\ntoken: t\n", "doc[0]: a.b"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := Load([]byte(c.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := strings.Join(d.Unaddressable(), ","); got != c.key {
				t.Errorf("Unaddressable gave %q, want %q", got, c.key)
			}
			// The ambiguous path must not be reachable by any route: a mark or a rule naming it has
			// to fail rather than resolve to one of the two locations it could mean.
			for _, p := range d.Paths() {
				if strings.Contains(p, ".crt") || strings.Contains(p, "files[0]") || p == "a.b" {
					t.Errorf("Paths includes the ambiguous path %q", p)
				}
			}
			if _, ok := d.Get("doc[0].machine.ca.crt"); ok {
				t.Error("Get resolved an ambiguous path")
			}
			if err := d.Replace("doc[0].machine.ca.crt", "x"); err == nil {
				t.Error("Replace resolved an ambiguous path")
			}
		})
	}
}

// TestLoadRefusesADocumentThatIsEntirelyUnaddressable checks the degenerate case is an error rather
// than an empty index. Extraction would find nothing and the run would scan clean for a reason that
// has nothing to do with the design under test.
func TestLoadRefusesADocumentThatIsEntirelyUnaddressable(t *testing.T) {
	if _, err := Load([]byte("a.b:\n  c: x\n")); err == nil {
		t.Fatal("Load accepted a document in which nothing can be addressed")
	} else if !strings.Contains(err.Error(), "can be addressed") {
		t.Errorf("the error does not explain why: %v", err)
	}
}

// TestLoadRefusesAliases checks the other way one scalar could appear under two paths, which would
// make one extraction look like two in the journal.
func TestLoadRefusesAliases(t *testing.T) {
	body := "base: &anchor secret-value\ncopy: *anchor\n"
	if _, err := Load([]byte(body)); err == nil {
		t.Fatal("Load accepted a document using an alias")
	} else if !strings.Contains(err.Error(), "alias") {
		t.Errorf("the error does not name the alias: %v", err)
	}
}

// TestLoadRefusesNonDocuments covers the inputs that would otherwise produce an empty index, which
// an extraction run would read as "no secrets here".
func TestLoadRefusesNonDocuments(t *testing.T) {
	for name, body := range map[string]string{
		"empty":          "",
		"only a comment": "# nothing\n",
		"a bare scalar":  "just-a-string\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load([]byte(body)); err == nil {
				t.Fatal("Load accepted an input with nothing to address")
			}
		})
	}
}
