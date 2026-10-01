package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const secretText = "ingest-test-secret-7f3c"

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
			if got := fmt.Sprintf(verb, x); strings.Contains(got, secretText) || strings.Contains(got, fmt.Sprintf("%x", secretText)) {
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
}

func TestZeroUnresolvedRefused(t *testing.T) {
	if _, err := parse(Unresolved{}); err == nil {
		t.Fatal("the zero Unresolved parsed")
	}
}
