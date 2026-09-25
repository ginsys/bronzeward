package main

import "testing"

func TestSchemaPathsAreTheFieldsTalosRedacts(t *testing.T) {
	text := "machine:\n    token: BWSYNTH-machine-token\n    registries:\n        config:\n            r.test:\n" +
		"                auth:\n                    username: BWSYNTH-user\n                    password: BWSYNTH-pass\n" +
		"cluster:\n    network:\n        dnsDomain: BWSYNTH-domain\n"
	got, err := schemaPaths([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v1alpha1 machine/token", "v1alpha1 machine/registries/config/r.test/auth/password"} {
		if !got[want] {
			t.Errorf("%s not in %v", want, got)
		}
	}
	for _, not := range []string{"v1alpha1 machine/registries/config/r.test/auth/username", "v1alpha1 cluster/network/dnsDomain"} {
		if got[not] {
			t.Errorf("%s redacted by the schema", not)
		}
	}
}

func TestSchemaPathsRefuseWhatTalosCannotLoad(t *testing.T) {
	if _, err := schemaPaths([]byte("machine:\n    features:\n        kubePrism:\n            port: BWSYNTH-x\n")); err == nil {
		t.Fatal("a configuration Talos cannot decode gave a schema path set")
	}
}
