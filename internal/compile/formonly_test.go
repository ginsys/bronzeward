package compile

import "testing"

// formOnly says true only when every document, key and value is equal (operator-experience.md §2.4,
// ginsys/bronzeward#1098); each case below is one way two texts differ or one thing it cannot decide.
func TestFormOnly(t *testing.T) {
	const base = "version: v1alpha1\nmachine:\n  token: s3cret\n  network:\n    hostname: a\n  files:\n    - path: /x\n      op: create\n    - path: /y\n      op: append\n"
	for _, c := range []struct {
		name, a, b string
		want       bool
	}{
		{"identical", base, base, true},
		{"comments and blank lines", base, "# generated\n" + base + "\n# end\n", true},
		{"comments between keys", "a: 1\nb: 2\n", "a: 1 # one\n# two\nb: 2\n", true},
		{"key order", "a: 1\nb: 2\n", "b: 2\na: 1\n", true},
		{"indentation and quoting", "a:\n    b: \"x\"\n", "a:\n  b: x\n", true},
		{"flow style", "a: [1, 2]\nb: {c: d}\n", "a:\n- 1\n- 2\nb:\n  c: d\n", true},
		{"literal and quoted string", "a: |\n  one\n  two\n", "a: \"one\\ntwo\\n\"\n", true},
		{"null spellings", "a: ~\nb: null\nc:\n", "a:\nb: ~\nc: null\n", true},
		{"integer spelling", "a: 16\n", "a: 0x10\n", true},
		{"float spelling", "a: 1.5\nb: 100.0\n", "a: 1.50\nb: 1e2\n", true},
		{"two documents", "a: 1\n---\nb: 2\n", "# c\na: 1\n---\n# d\nb: 2\n", true},

		{"a changed value", "a: 1\n", "a: 2\n", false},
		{"floats beyond float64 precision", "a: 9007199254740992.0\n", "a: 9007199254740993.0\n", false},
		{"integers beyond any integer type", "a: 100000000000000000000\n", "a: 100000000000000000001\n", false},
		{"two spellings of one integer key", "16: first\n0x10: second\n", "16: first\n0x10: second\n", false},
		{"a non-string key", "1: a\n", "1: a\n", false},
		{"a changed secret behind an unchanged shape", "machine:\n  token: s3cret\n", "machine:\n  token: s3cres\n", false},
		{"a dropped key", "a: 1\nb: 2\n", "a: 1\n", false},
		{"an added key", "a: 1\n", "a: 1\nb: 2\n", false},
		{"a renamed key", "a: 1\n", "b: 1\n", false},
		{"a document added", "a: 1\n", "a: 1\n---\nb: 2\n", false},
		{"a document removed", "a: 1\n---\nb: 2\n", "a: 1\n", false},
		{"document order", "a: 1\n---\nb: 2\n", "b: 2\n---\na: 1\n", false},
		{"list order", "a: [1, 2]\n", "a: [2, 1]\n", false},
		{"a longer list", "a: [1]\n", "a: [1, 1]\n", false},
		{"a string and a number", "a: \"1\"\n", "a: 1\n", false},
		{"a string and a boolean", "a: \"true\"\n", "a: true\n", false},
		{"an integer and a float", "a: 1\n", "a: 1.0\n", false},
		{"a null and an empty string", "a: null\n", "a: \"\"\n", false},
		{"a map and a list", "a: {b: 1}\n", "a: [b]\n", false},
		{"an alias", "a: &x 1\nb: *x\n", "a: 1\nb: 1\n", false},
		{"an anchor alone", "a: &x 1\n", "a: &x 1\n", false},
		{"a merge key", "a: &x {b: 1}\nc:\n  <<: *x\n", "a: &x {b: 1}\nc:\n  <<: *x\n", false},
		{"a duplicate key", "a: 1\na: 1\n", "a: 1\na: 1\n", false},
		{"a duplicate key on one side", "a: 1\na: 1\n", "a: 1\n", false},
		{"a custom tag", "a: !secret x\n", "a: !secret x\n", false},
		{"a parse error", "a: [1\n", "a: [1\n", false},
		{"a parse error on one side", "a: 1\n", "a: [1\n", false},
		{"an empty text", "", "", true},
		{"an empty text and a document", "", "a: 1\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := formOnly([]byte(c.a), []byte(c.b)); got != c.want {
				t.Errorf("formOnly = %t, want %t", got, c.want)
			}
		})
	}
}
