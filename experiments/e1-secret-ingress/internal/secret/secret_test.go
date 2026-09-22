package secret

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// plaintext is the value every test below asserts never appears in output. It is not a secret and
// not a fixture canary; it exists only so that a single strings.Contains can decide each case.
const plaintext = "E1-TEST-PLAINTEXT-MUST-NOT-APPEAR"

// wantRedaction is what every rendering must produce instead.
func wantRedaction(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(plaintext))
	return redactedPrefix + hex.EncodeToString(sum[:])[:digestLen]
}

// assertRedacted fails if got holds the plaintext, and fails if it does not hold the redaction.
// Both halves matter: a rendering that emits nothing at all hides the plaintext but also hides
// that a secret was there, which would make the ordering journal unreadable.
func assertRedacted(t *testing.T, what, got string) {
	t.Helper()
	assertNoPlaintext(t, what, got)
	if !strings.Contains(got, wantRedaction(t)) {
		t.Errorf("%s did not render the redaction: got %q, want it to contain %q", what, got, wantRedaction(t))
	}
}

// assertNoPlaintext fails if the plaintext appears in any spelling fmt produces for a byte slice:
// as text, as the decimal list %v gives a []byte, or as hex. The first version looked only for the
// text, so a []byte field printed as [69 49 45 ...] passed it, and that leak was caught only
// because the same output also lacked the redaction. The Go-syntax list, 0x45, 0x31, ..., which is
// what a []byte field prints under %#v, was missing until a review pointed it out.
func assertNoPlaintext(t *testing.T, what, got string) {
	t.Helper()
	for spelling, needle := range plaintextSpellings() {
		if strings.Contains(got, needle) {
			t.Errorf("%s leaked the plaintext as %s: %s", what, spelling, got)
		}
	}
}

// plaintextSpellings is every form fmt gives the plaintext's bytes, keyed by a name for the error.
// TestEverySpellingIsRecognised holds it to the verbs the tests use.
func plaintextSpellings() map[string]string {
	b := []byte(plaintext)
	goSyntax := fmt.Sprintf("%#v", b)
	return map[string]string{
		"text":         plaintext,
		"decimal":      strings.Trim(fmt.Sprint(b), "[]"),
		"hex":          hex.EncodeToString(b),
		"upper hex":    strings.ToUpper(hex.EncodeToString(b)),
		"spaced bytes": fmt.Sprintf("% x", b),
		"Go syntax":    goSyntax[strings.Index(goSyntax, "{")+1 : len(goSyntax)-1],
	}
}

// TestEverySpellingIsRecognised calibrates assertNoPlaintext against the leak it exists to catch:
// for every verb the redaction tests use, the unprotected control's rendering must match at least
// one spelling. A verb whose leak no needle matches would pass those tests however it leaked.
func TestEverySpellingIsRecognised(t *testing.T) {
	leaky := unprotected{plaintext: []byte(plaintext)}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%d", "%q"} {
		got := fmt.Sprintf(verb, leaky)
		matched := false
		for _, needle := range plaintextSpellings() {
			if strings.Contains(got, needle) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("no spelling recognises the leak %s prints: %s", verb, got)
		}
	}
}

// unprotected is what Unresolved would be without its method set: a struct with an unexported
// byte field and nothing else. It exists to calibrate the tests below.
type unprotected struct{ plaintext []byte }

// TestTheTestsCanSeeALeak is the positive control for this file. Every other test asserts an
// absence, and an absence is only evidence once the instrument has been shown to detect the thing
// it is looking for. Unexported fields do not hide a value from fmt, which is the whole reason
// Unresolved needs an explicit method set rather than just a lowercase field name.
//
// If this test ever fails, the assertions below prove nothing and must not be believed.
func TestTheTestsCanSeeALeak(t *testing.T) {
	leaky := unprotected{plaintext: []byte(plaintext)}

	// %s on a []byte field renders the bytes as text. This is the leak the real type prevents.
	if got := fmt.Sprintf("%s", leaky); !strings.Contains(got, plaintext) {
		t.Fatalf("the control did not leak, so these tests cannot detect a leak: %q", got)
	}
	// %+v on the struct renders the field too, in numeric form; check the text form separately so
	// the control does not depend on one verb's byte-slice handling.
	if got := fmt.Sprintf("%s", leaky.plaintext); !strings.Contains(got, plaintext) {
		t.Fatalf("the control did not leak through its field: %q", got)
	}
}

// TestUnresolvedFormatVerbs covers every fmt verb a caller might reach for, including %#v, which
// does not call String and is the trap this type exists to close.
func TestUnresolvedFormatVerbs(t *testing.T) {
	u := NewUnresolved([]byte(plaintext))

	for _, verb := range []string{"%v", "%s", "%q", "%#v", "%+v", "%x", "%d", "%08s"} {
		assertRedacted(t, "fmt.Sprintf("+verb+")", fmt.Sprintf(verb, u))
	}

	// %q must stay a quoted string, or a caller building a shell command or an SQL literal from
	// it produces something that no longer parses.
	if q := fmt.Sprintf("%q", u); !strings.HasPrefix(q, `"`) || !strings.HasSuffix(q, `"`) {
		t.Errorf("%%q was not quoted: %s", q)
	}

	// A pointer must redact too: &u is what a caller passes without thinking.
	assertRedacted(t, "fmt.Sprintf(%v) on a pointer", fmt.Sprintf("%v", &u))

	// Direct method calls, for the case where Formatter is removed in a future edit and String or
	// GoString becomes the live path again.
	assertRedacted(t, "String()", u.String())
	assertRedacted(t, "GoString()", u.GoString())
	assertRedacted(t, "Redacted()", u.Redacted())
}

// TestUnresolvedInsideContainers covers the realistic shape: the secret is a field or an element,
// and the caller formats the container without thinking about what is inside it.
func TestUnresolvedInsideContainers(t *testing.T) {
	u := NewUnresolved([]byte(plaintext))

	type config struct {
		Name  string
		Token Unresolved
	}
	c := config{Name: "bootstrap", Token: u}

	for _, verb := range []string{"%v", "%+v", "%#v"} {
		assertRedacted(t, "struct under "+verb, fmt.Sprintf(verb, c))
		assertRedacted(t, "pointer to struct under "+verb, fmt.Sprintf(verb, &c))
	}

	assertRedacted(t, "slice", fmt.Sprintf("%v", []Unresolved{u}))
	assertRedacted(t, "map value", fmt.Sprintf("%v", map[string]Unresolved{"token": u}))
	assertRedacted(t, "interface element", fmt.Sprintf("%v", []any{"x", u, 3}))

	// An unexported field. fmt calls a field's Format, String or GoString only when reflection is
	// allowed to take its value as an interface, which it is not for an unexported field; it then
	// walks the field's own fields instead. The exported Token above is the case where the
	// overrides run, and the only case this test used to check.
	type private struct {
		name  string
		token Unresolved
	}
	// No redaction can appear here — fmt never calls a method on an unexported field — so the
	// assertion is only that the value is absent in every spelling.
	p := private{name: "bootstrap", token: u}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%d", "%q"} {
		assertNoPlaintext(t, "unexported field under "+verb, fmt.Sprintf(verb, p))
		assertNoPlaintext(t, "pointer to a struct with an unexported field under "+verb, fmt.Sprintf(verb, &p))
	}
}

// TestUnresolvedJSON covers encoding/json directly and as a struct field, since a sanitized draft
// and every journal record are JSON.
func TestUnresolvedJSON(t *testing.T) {
	u := NewUnresolved([]byte(plaintext))

	direct, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertRedacted(t, "json.Marshal", string(direct))

	// It must still be valid JSON, not a bare token.
	var s string
	if err := json.Unmarshal(direct, &s); err != nil {
		t.Fatalf("marshalled form is not a JSON string: %v", err)
	}

	nested, err := json.Marshal(struct {
		Name  string     `json:"name"`
		Token Unresolved `json:"token"`
	}{Name: "bootstrap", Token: u})
	if err != nil {
		t.Fatalf("json.Marshal (nested): %v", err)
	}
	assertRedacted(t, "json.Marshal (nested)", string(nested))

	text, err := u.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	assertRedacted(t, "MarshalText", string(text))
}

// tokenError is the shape an error takes when a secret is what went wrong: the value is carried
// along so the message can identify it, which is exactly when a leak happens.
type tokenError struct {
	Token Unresolved
}

func (e tokenError) Error() string { return fmt.Sprintf("rejecting token %v", e.Token) }

// TestUnresolvedThroughErrors covers %w wrapping. An error's message is written to logs, returned
// to callers and embedded in error reports, all of which §7.1 names as persistence surfaces.
func TestUnresolvedThroughErrors(t *testing.T) {
	u := NewUnresolved([]byte(plaintext))

	base := tokenError{Token: u}
	wrapped := fmt.Errorf("extract: %w", base)
	doubled := fmt.Errorf("import: %w", wrapped)

	assertRedacted(t, "error message", base.Error())
	assertRedacted(t, "wrapped error", wrapped.Error())
	assertRedacted(t, "twice-wrapped error", doubled.Error())
	assertRedacted(t, "wrapped error under %+v", fmt.Sprintf("%+v", doubled))

	// Errorf with the value itself rather than an error carrying it.
	assertRedacted(t, "fmt.Errorf with the value", fmt.Errorf("token %v is invalid", u).Error())
}

// TestUnresolvedThroughSlog covers both stock handlers, as an attribute and as a struct field.
func TestUnresolvedThroughSlog(t *testing.T) {
	u := NewUnresolved([]byte(plaintext))

	type config struct {
		Name  string     `json:"name"`
		Token Unresolved `json:"token"`
	}
	c := config{Name: "bootstrap", Token: u}

	handlers := map[string]func(*bytes.Buffer) slog.Handler{
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	}

	for name, newHandler := range handlers {
		for _, attr := range []struct {
			what string
			a    slog.Attr
		}{
			{"the value", slog.Any("token", u)},
			{"a pointer", slog.Any("token", &u)},
			{"a struct holding it", slog.Any("config", c)},
			{"a group holding it", slog.Group("creds", slog.Any("token", u))},
		} {
			var buf bytes.Buffer
			slog.New(newHandler(&buf)).LogAttrs(t.Context(), slog.LevelInfo, "ingesting", attr.a)
			assertRedacted(t, name+" handler with "+attr.what, buf.String())
		}
	}
}

// TestDigestAndCopy pins the two properties the journal depends on.
func TestDigestAndCopy(t *testing.T) {
	buf := []byte(plaintext)
	u := NewUnresolved(buf)

	sum := sha256.Sum256([]byte(plaintext))
	if want := hex.EncodeToString(sum[:]); u.Digest() != want {
		t.Errorf("Digest() = %q, want %q", u.Digest(), want)
	}

	// The constructor must copy: a caller that zeroes its buffer after wrapping must not change
	// the value behind a digest already written to the journal.
	for i := range buf {
		buf[i] = 0
	}
	if got := string(u.Unsafe()); got != plaintext {
		t.Errorf("NewUnresolved did not copy its input: Unsafe() = %q after the caller zeroed it", got)
	}

	// And the accessor must copy too: writing through what Unsafe returned must not change the
	// value either.
	out := u.Unsafe()
	for i := range out {
		out[i] = 0
	}
	if got := string(u.Unsafe()); got != plaintext {
		t.Errorf("writing through Unsafe() changed the value: %q", got)
	}

	// Equal plaintext must give an equal digest, or cross-run comparison in the report is
	// meaningless; different plaintext must not.
	if NewUnresolved([]byte("a")).Digest() == NewUnresolved([]byte("b")).Digest() {
		t.Error("different plaintext produced the same digest")
	}
	if NewUnresolved([]byte("a")).Digest() != NewUnresolved([]byte("a")).Digest() {
		t.Error("equal plaintext produced different digests")
	}
}

// TestZeroValueRendersUnset checks that a forgotten field is not distinguishable from a set one by
// leaking a recognisable constant. The SHA-256 of the empty string is a well-known value; emitting
// it would tell a reader "this secret is empty", which is itself information about the secret.
func TestZeroValueRendersUnset(t *testing.T) {
	var u Unresolved

	got := fmt.Sprintf("%v", u)
	if !strings.HasPrefix(got, redactedPrefix) {
		t.Errorf("the zero value did not render as a redaction: %q", got)
	}
	emptySum := sha256.Sum256(nil)
	if strings.Contains(got, hex.EncodeToString(emptySum[:])[:digestLen]) {
		t.Errorf("the zero value rendered the digest of the empty string: %q", got)
	}
}

// TestSanitizedZeroValueIsInvalid pins the one gap the type system leaves open. Any package can
// write Sanitized{}; every persistence function must be able to reject it.
func TestSanitizedZeroValueIsInvalid(t *testing.T) {
	var zero Sanitized
	if zero.Valid() {
		t.Error("the zero Sanitized reported itself valid")
	}

	s := NewSanitized([]byte("machine: {}\n"), []Reference{{Path: "a.b", URI: "kv://x", Digest: "d"}})
	if !s.Valid() {
		t.Error("NewSanitized produced a value that reports itself invalid")
	}
	if got := string(s.Document()); got != "machine: {}\n" {
		t.Errorf("Document() = %q", got)
	}
	if len(s.References()) != 1 {
		t.Fatalf("References() returned %d entries, want 1", len(s.References()))
	}
}

// TestSanitizedCopiesItsInputs checks that a caller cannot mutate a Sanitized document after
// construction. Persistence reads it later; a shared backing array would let plaintext be written
// back into a document that already passed extraction.
func TestSanitizedCopiesItsInputs(t *testing.T) {
	doc := []byte("machine: {}\n")
	refs := []Reference{{Path: "a.b", URI: "kv://x", Digest: "d"}}
	s := NewSanitized(doc, refs)

	copy(doc, []byte("LEAKED------"))
	refs[0].URI = "mutated"

	if got := string(s.Document()); got != "machine: {}\n" {
		t.Errorf("mutating the caller's slice changed the document: %q", got)
	}
	if got := s.References()[0].URI; got != "kv://x" {
		t.Errorf("mutating the caller's slice changed a reference: %q", got)
	}

	// The other direction: what the accessors hand out must not alias the value either, or the
	// constructor's copying protects only half of it.
	copy(s.Document(), []byte("LEAKED------"))
	s.References()[0].URI = "mutated"
	if got := string(s.Document()); got != "machine: {}\n" {
		t.Errorf("writing through Document() changed the document: %q", got)
	}
	if got := s.References()[0].URI; got != "kv://x" {
		t.Errorf("writing through References() changed a reference: %q", got)
	}
}

// sanitizedConstructors are the non-test packages permitted to call NewSanitized.
//
// Extraction is the constructor the design rests on. Staging is the second, and it is a trust
// assertion rather than an extraction: a resumed change is rebuilt from bytes decrypted out of
// staging, sound only because what was encrypted had already been sanitized and its digest is
// checked against the claim first. Test files may build one freely; they cannot reach persistence
// in a real run.
var sanitizedConstructors = map[string]bool{
	"internal/secret":  true,
	"internal/extract": true,
	"internal/staging": true,
}

// TestNewSanitizedHasNoUnexpectedCallers is the enforcement the constructor's doc comment used to
// claim and did not have. Go cannot restrict an exported function to one sibling package, so the
// claim that only extraction produces a Sanitized is a property of this test, not of the compiler,
// and the report says so. Lexical like the Unsafe scan beside it, and over-reporting for the same
// reason.
func TestNewSanitizedHasNoUnexpectedCallers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the module root: %v", err)
	}
	var scanned int
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		scanned++
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(body, []byte("NewSanitized(")) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if pkg := filepath.ToSlash(filepath.Dir(rel)); !sanitizedConstructors[pkg] {
			t.Errorf("%s calls NewSanitized; only %v may", rel, keys(sanitizedConstructors))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned == 0 {
		t.Fatalf("walked %s and found no .go files at all; the scan proves nothing", root)
	}
}

// unsafeCallers are the packages permitted to call Unresolved.Unsafe. Extraction must read the
// plaintext to send it to a provider, and the deliberate-failure controls must write it somewhere
// it does not belong; nothing else has a reason to.
var unsafeCallers = map[string]bool{
	"internal/secret":  true,
	"internal/extract": true,
	"internal/control": true,
}

// TestUnsafeHasNoUnexpectedCallers is the structural half of the argument the experiment rests on:
// "what may touch a secret?" must be answerable by one grep over this module. It walks the module
// source rather than trusting that a reviewer would notice a new call site.
//
// It is a lexical scan, not a type-checked one, so it over-reports (any method named Unsafe on any
// type counts). That direction is the safe one: a false positive is read by a human, a false
// negative is a leak.
func TestUnsafeHasNoUnexpectedCallers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the module root: %v", err)
	}
	// Prove the walk reached real source before trusting an empty result: a scan that found
	// nothing because it walked the wrong directory is indistinguishable from a clean one.
	var scanned int

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		scanned++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(body, []byte(".Unsafe()")) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if pkg := filepath.ToSlash(filepath.Dir(rel)); !unsafeCallers[pkg] {
			t.Errorf("%s calls Unsafe(); only %v may", rel, keys(unsafeCallers))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned == 0 {
		t.Fatalf("walked %s and found no .go files at all; the scan proves nothing", root)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
