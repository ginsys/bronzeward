package provider

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Decode is NewValue's inverse: what resolution places is what ingestion stored.
func TestValueDecode(t *testing.T) {
	for _, c := range []struct {
		k    Kind
		v    any
		want any
	}{
		{KindString, "s3cret", "s3cret"},
		{KindString, "", ""},
		{KindInteger, 42, json.Number("42")},
		{KindInteger, uint64(math.MaxUint64), json.Number("18446744073709551615")},
		{KindBoolean, true, true},
		{KindMapping, map[string]any{"a": "x", "b": 1, "c": false}, map[string]any{"a": "x", "b": json.Number("1"), "c": false}},
		{KindMapping, map[string]any{}, map[string]any{}},
	} {
		got, err := mustValue(t, c.k, c.v).Decode()
		if err != nil {
			t.Errorf("Decode of %s %T: %v", c.k, c.v, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Decode of %s %T = %#v, want %#v", c.k, c.v, got, c.want)
		}
	}
}

// A Value not made by NewValue, or one whose content no longer holds to its kind, decodes to an
// error that quotes nothing.
func TestValueDecodeRefusesUnchecked(t *testing.T) {
	raw := func(k Kind, s string) Value { return Value{&value{kind: k, json: json.RawMessage(s)}} }
	for name, v := range map[string]Value{
		"zero value":    {},
		"kind mismatch": raw(KindInteger, `"PROVIDER-PLAINTEXT"`),
		"trailing data": raw(KindString, `"PROVIDER-PLAINTEXT" "b"`),
	} {
		if got, err := v.Decode(); err == nil {
			t.Errorf("%s: decoded to %T", name, got)
		} else if strings.Contains(err.Error(), "PROVIDER-PLAINTEXT") {
			t.Errorf("%s: the error quotes the value: %v", name, err)
		}
	}
}
