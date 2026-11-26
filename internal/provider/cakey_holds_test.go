package provider

import (
	"encoding/base64"
	"strings"
	"testing"
)

// The offered key matches byte for byte or not at all: no re-wrapping, type, spacing or newline is
// ignored (compilation.md §2.3 step 3a). Synthetic text, no key material.
func TestCAKeyHolds(t *testing.T) {
	const stored = "-----BEGIN SYNTHETIC KEY-----\nc3ludGhldGljLWNhLWtleS1mb3ItdGVzdHM=\n-----END SYNTHETIC KEY-----\n"
	k := NewCAKey([]byte("crt"), []byte(stored))
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	if !k.Holds(enc(stored)) {
		t.Fatal("the stored text does not match itself")
	}
	for name, offered := range map[string]string{
		"re-wrapped":       enc(strings.Replace(stored, "LWNh", "\nLWNh", 1)),
		"another PEM type": enc(strings.ReplaceAll(stored, "SYNTHETIC", "OTHER")),
		"a space":          enc(strings.Replace(stored, "KEY-----\n", "KEY----- \n", 1)),
		"no newline":       enc(strings.TrimSuffix(stored, "\n")),
		"a second newline": enc(stored + "\n"),
		"one byte altered": enc(strings.Replace(stored, "c3lu", "c3lv", 1)),
		"another key":      enc("another synthetic key\n"),
		"not base64":       "%%%",
		"empty":            "",
	} {
		if k.Holds(offered) {
			t.Errorf("%s matched", name)
		}
	}
	if (CAKey{}).Holds("") {
		t.Error("an unread key matched an empty offer")
	}
}
