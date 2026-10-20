package upgrade

import "testing"

func TestBronzewardVersionControls(t *testing.T) {
	schematic, version, err := extractSchematicAndVersion("factory.example/metal-installer/abc123:v1.14.1")
	if err != nil || schematic != "abc123" || version != "1.14.1" {
		t.Fatalf("plain tag parse: %q %q %v", schematic, version, err)
	}
	if _, _, err := extractSchematicAndVersion("invalid-image"); err == nil {
		t.Fatal("malformed image accepted")
	}
	for version, want := range map[string]bool{"v1.12.9": false, "v1.13.0-alpha.3": false, "v1.13.0": true, "v1.14.1": true} {
		if got := supportsLifecycleUpgrade(version); got != want {
			t.Fatalf("version gate %s: got %t want %t", version, got, want)
		}
	}
}

func TestBronzewardDigestMustNotBecomeVersion(t *testing.T) {
	_, version, err := extractSchematicAndVersion("factory.example/metal-installer/abc123:v1.14.1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("digest input could not be inspected: %v", err)
	}
	if version != "1.14.1" {
		t.Fatalf("BW_DIGEST_IN_VERSION: observed version %q", version)
	}
}

func TestBronzewardUnknownVersionMustNotSelectAPI(t *testing.T) {
	empty, malformed := supportsLifecycleUpgrade(""), supportsLifecycleUpgrade("not-a-version")
	if empty || malformed {
		t.Fatalf("BW_UNPROVEN_API_SELECTION: empty=%t malformed=%t", empty, malformed)
	}
}
