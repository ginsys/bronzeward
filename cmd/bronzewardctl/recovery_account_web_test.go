package main

import (
	"strconv"
	"strings"
	"testing"
)

// The console's table of settle causes (web/src/settle-causes.ts) is a copy of the command's list. A refusal carries its cause
// only in its detail text, so the web finds a cause by that text; this reads the web table against the command's list and the
// server's own words, so a renamed cause fails here and the console never silently loses a next step
// (ginsys/bronzeward#365).

const (
	webCausesPath      = "../../web/src/settle-causes.ts"
	apiSettlePath      = "../../internal/api/settle.go"
	apiAccountingsPath = "../../internal/api/recovery_accountings.go"
)

func TestWebSettleCausesMatchTheCommandAndTheServer(t *testing.T) {
	if len(settleCauses) != 9 {
		t.Fatalf("the command lists %d settle causes, the web table was written for 9; read both and move them together", len(settleCauses))
	}
	web := readSource(t, webCausesPath)
	server := readSource(t, apiSettlePath) + "\n" + readSource(t, apiAccountingsPath)
	for _, c := range settleCauses {
		// The web file writes each cause as a double-quoted string.
		if !strings.Contains(web, strconv.Quote(c.cause)) {
			t.Errorf("%s has no row for the cause %q", webCausesPath, c.cause)
		}
		if !strings.Contains(server, c.cause) {
			t.Errorf("neither %s nor %s holds the cause %q: the server renamed it", apiSettlePath, apiAccountingsPath, c.cause)
		}
	}
	if n := strings.Count(web, "cause: \""); n != len(settleCauses) {
		t.Errorf("%s holds %d causes, the command lists %d", webCausesPath, n, len(settleCauses))
	}
}
