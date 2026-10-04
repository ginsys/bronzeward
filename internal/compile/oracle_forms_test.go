package compile

import (
	"slices"
	"testing"
)

// The SP oracle as the sensitivity gate ports it: every form of a value it must find, and the
// texts it must not count.
func TestOracleForms(t *testing.T) {
	str := secret{id: "s@1", class: "ref", kind: "str", value: "BWSYNTH-oracle-string-value"}
	multi := secret{id: "m@1", class: "ref", kind: "str", value: "BWSYNTH-first-line\nsecond-line-of-it"}
	quoted := secret{id: "q@1", class: "ref", kind: "str", value: `BWSYNTH-say "hi" <now>`}
	raw := secret{id: "b@1", class: "ref", kind: "bytes", value: "BWSYNTH-bytes-value", placed: "QldTWU5USC1ieXRlcy12YWx1ZQ=="}
	port := secret{id: "p@1", class: "ref", kind: "int", value: "5000", key: "port"}
	base := secret{id: "key", class: "base", kind: "str", value: "c2VjcmV0LWJ1bmRsZS1rZXk="}
	for _, tc := range []struct {
		name, text, public string
		secrets            []secret
		want               []finding
	}{
		{"exact", "x: BWSYNTH-oracle-string-value\n", "", []secret{str}, []finding{{"s@1", "exact"}}},
		{"placed", "ca: QldTWU5USC1ieXRlcy12YWx1ZQ==\n", "", []secret{raw}, []finding{{"b@1", "placed"}}},
		{"yaml-escaped", `x: "BWSYNTH-first-line\nsecond-line-of-it"`, "", []secret{multi}, []finding{{"m@1", "yaml-escaped"}}},
		{"json-escaped", `{"x":"BWSYNTH-say \"hi\" \u003cnow\u003e"}`, "", []secret{quoted}, []finding{{"q@1", "json-escaped"}}},
		{"base64", "x: QldTWU5USC1vcmFjbGUtc3RyaW5nLXZhbHVl\n", "", []secret{str}, []finding{{"s@1", "base64"}}},
		{"line", "error near second-line-of-it", "", []secret{multi}, []finding{{"m@1", "line"}}},
		{"prefix", `cannot decode "BWSYNTH..." as int`, "", []secret{str}, []finding{{"s@1", "prefix"}}},
		{"fragment", "...racle-string-va...", "", []secret{str}, []finding{{"s@1", "fragment"}}},
		{"fragment in public text", "BWSYNTH-oracl", "BWSYNTH-oracle", []secret{str}, nil},
		{"keyed", "  port: 5000\n", "", []secret{port}, []finding{{"p@1", "keyed"}}},
		{"keyed bounded", "  port: 50001\n", "", []secret{port}, nil},
		{"base exact only", "key: c2VjcmV0LWJ1bmRsZS1rZXk=\n", "", []secret{base}, []finding{{"key", "exact"}}},
		{"base fragment not searched", "c2VjcmV0LWJ1bmRsZS1r", "", []secret{base}, nil},
		{"clean", "x: <redacted:value>\n", "", []secret{str, multi, quoted, raw, port, base}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := oracleScan(tc.text, tc.secrets, tc.public)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("oracleScan = %v, want %v", got, tc.want)
			}
		})
	}
}
