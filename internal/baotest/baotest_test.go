package baotest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecide(t *testing.T) {
	for _, c := range []struct {
		addr, require string
		skip, fail    bool
	}{
		{"http://127.0.0.1:58201", "", false, false},
		{"http://127.0.0.1:58201", "1", false, false},
		{"", "", true, false},
		{"", "0", true, false},
		{"", "1", false, true},
	} {
		if skip, fail := decide(c.addr, c.require); skip != c.skip || fail != c.fail {
			t.Errorf("decide(%q, %q) = %t, %t", c.addr, c.require, skip, fail)
		}
	}
}

// Every policy New writes is a committed file, the one bin/up writes too.
func TestPolicyFilesExist(t *testing.T) {
	dir := policyDir(t)
	for _, name := range Policies {
		if _, err := os.Stat(filepath.Join(dir, name+".hcl")); err != nil {
			t.Error(err)
		}
	}
}
