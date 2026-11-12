package api

import (
	"debug/buildinfo"
	"os"
	"strings"
	"testing"
)

// buildTags returns the build tags the running test binary records in its build information. The
// second result is false when the information holds no build settings at all, in which case an
// empty tag list proves nothing.
func buildTags(t *testing.T) ([]string, bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, s := range info.Settings {
		if s.Key == "-tags" {
			tags = strings.Split(s.Value, ",")
		}
	}
	return tags, len(info.Settings) > 0
}
