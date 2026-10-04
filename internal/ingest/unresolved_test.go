package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const secretText = "ingest-test-secret-7f3c"

// shows is whether a rendering holds s as text, as hex or as the decimal bytes fmt prints for a
// []byte it reaches by reflection.
func shows(rendered, s string) bool {
	return strings.Contains(rendered, s) || strings.Contains(rendered, fmt.Sprintf("%x", s)) ||
		strings.Contains(rendered, strings.Trim(fmt.Sprint([]byte(s)), "[]"))
}

// TestUnresolvedNeverRenders: no fmt verb, marshaller or reflection-printed holder shows the input
// (compilation.md §2.1: no string, formatting or serialization method yields the bytes).
func TestUnresolvedNeverRenders(t *testing.T) {
	u, err := Read(strings.NewReader("machine:\n  token: "+secretText+"\n"), 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	holder := struct{ in Unresolved }{u}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		for _, x := range []any{u, &u, holder} {
			if got := fmt.Sprintf(verb, x); shows(got, secretText) {
				t.Errorf("%s of %T shows the input: %s", verb, x, got)
			}
		}
	}
	if b, err := json.Marshal(u); err == nil || bytes.Contains(b, []byte(secretText)) {
		t.Errorf("json.Marshal: %s, %v", b, err)
	}
	if b, err := yaml.Marshal(u); err == nil || bytes.Contains(b, []byte(secretText)) {
		t.Errorf("yaml.Marshal: %s, %v", b, err)
	}
	if b, err := u.MarshalText(); err == nil || b != nil {
		t.Errorf("MarshalText: %s, %v", b, err)
	}
	// Every method a caller outside the package could reach: none returns bytes or a string
	// other than the placeholder.
	ty := reflect.TypeOf(u)
	for i := 0; i < ty.NumMethod(); i++ {
		m := ty.Method(i)
		if m.Name == "Size" {
			continue
		}
		out := reflect.ValueOf(u).Method(i)
		if out.Type().NumIn() != 0 {
			continue
		}
		for _, r := range out.Call(nil) {
			if s := fmt.Sprint(r.Interface()); strings.Contains(s, secretText) {
				t.Errorf("method %s yields the input", m.Name)
			}
		}
	}
}

func TestReadRefusesOverLimit(t *testing.T) {
	if _, err := Read(strings.NewReader(strings.Repeat("a", 11)), 10); err == nil {
		t.Fatal("an input over the limit was accepted")
	}
	u, err := Read(strings.NewReader(strings.Repeat("a", 10)), 10)
	if err != nil {
		t.Fatalf("an input at the limit was refused: %v", err)
	}
	if u.Size() != 10 {
		t.Fatalf("Size() = %d", u.Size())
	}
	if _, err := Read(strings.NewReader(""), 10); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("an empty input: %v", err)
	}
	// The largest limit must not overflow the one-byte probe past it.
	if u, err := Read(strings.NewReader("a: b\n"), math.MaxInt64); err != nil || u.Size() != 5 {
		t.Fatalf("an input under the largest limit: %v", err)
	}
}

func TestZeroUnresolvedRefused(t *testing.T) {
	if _, err := parse(Unresolved{}); err == nil {
		t.Fatal("the zero Unresolved parsed")
	}
}

// UnmarshalJSON takes the request body's document member: one JSON string of at most MaxDocument
// bytes, decoded exactly. Every refusal is one fixed error that names no part of the input.
func TestUnmarshalJSON(t *testing.T) {
	var u Unresolved
	in := "machine:\n  token: " + secretText + "\n"
	raw, _ := json.Marshal(in)
	if err := json.Unmarshal(raw, &u); err != nil || !bytes.Equal(u.bytes(), []byte(in)) {
		t.Fatalf("a JSON string: %v, %d bytes", err, u.Size())
	}
	at := `"` + strings.Repeat("a", MaxDocument) + `"`
	if err := json.Unmarshal([]byte(at), &u); err != nil || u.Size() != MaxDocument {
		t.Fatalf("a document at the limit: %v", err)
	}
	for name, b := range map[string]string{
		"an object":                `{"x":"` + secretText + `"}`,
		"an unterminated string":   `"` + secretText,
		"a number":                 `12` + secretText,
		"null":                     `null`,
		"an empty string":          `""`,
		"over the limit":           `"` + strings.Repeat("a", MaxDocument+1) + `"`,
		"invalid UTF-8":            "\"" + secretText + "\xff\"",
		"a lone surrogate escape":  `"` + secretText + `\udc00"`,
		"an escape over the limit": `"` + strings.Repeat(`a`, MaxDocument+1) + `"`,
		"U+0000":                   `"` + secretText + `\u0000"`,
	} {
		var u Unresolved
		err := json.Unmarshal([]byte(b), &u)
		if err == nil || strings.Contains(err.Error(), secretText) || u.Size() != 0 {
			t.Errorf("%s: %v, %d bytes kept", name, err, u.Size())
		}
	}
}
