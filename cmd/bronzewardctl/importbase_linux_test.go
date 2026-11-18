package main

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// typeAt answers a pseudo-terminal's screen: it sends q once the pager shows its key line, and
// returns what the terminal showed once the controller side closes.
func typeAt(ctl *os.File) (screen func() string, done <-chan struct{}) {
	var mu sync.Mutex
	var seen []byte
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		b := make([]byte, 4096)
		quit := false
		for {
			n, err := ctl.Read(b)
			mu.Lock()
			seen = append(seen, b[:n]...)
			s := string(seen)
			mu.Unlock()
			if err != nil {
				return
			}
			if !quit && strings.Contains(s, "space or f forward") {
				quit = true
				ctl.WriteString("q")
			}
		}
	}()
	return func() string { mu.Lock(); defer mu.Unlock(); return string(seen) }, fin
}

// TestImportBaseDocumentOnTerminal runs import-base document on a pseudo-terminal: it prints the
// protected-review notice, shows the document in the pager and ends at the pager's quit key with
// no marks prompt and nothing sent. A terminal smaller than the pager needs is refused before the
// document is read.
func TestImportBaseDocumentOnTerminal(t *testing.T) {
	f, _ := importSteps(t)
	doc := []string{"import-base", "document", "ibr_BWSYNTH"}

	_, small := onPty(t, 24, 19)
	var e bytes.Buffer
	if got := run(doc, small, &e); got != 2 || !strings.Contains(e.String(), "import-base document needs a terminal at least 20 columns wide and 2 rows high, and this one is 19 by 24; nothing was read") {
		t.Errorf("a terminal of 19 columns: exit %d, stderr %q; want 2 and the refusal", got, e.String())
	}
	if f.documents != 0 {
		t.Errorf("the document was read %d times on a terminal too small for it", f.documents)
	}

	ctl, term := onPty(t, 24, 80)
	screen, done := typeAt(ctl)
	e.Reset()
	got := run(doc, term, &e)
	term.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal stayed open after the command ended")
	}
	s := screen()
	if got != 0 || e.Len() != 0 {
		t.Errorf("exit %d, stderr %q; want 0 and nothing", got, e.String())
	}
	if !strings.Contains(s, "protected review") || !strings.Contains(s, "BWSYNTH-base-text") || !strings.Contains(s, "second: line") {
		t.Errorf("the terminal does not show the notice and the document: %q", s)
	}
	if f.documents != 1 {
		t.Errorf("the document was read %d times; want once", f.documents)
	}
	if took := f.took(); len(took) != 0 {
		t.Errorf("sent %v; want nothing", took)
	}
}

// TestImportBaseDocumentRefusalOnTerminal: the server's refusal is shown as it came, on standard
// error, and neither the notice nor any text reaches the terminal.
func TestImportBaseDocumentRefusalOnTerminal(t *testing.T) {
	f, _ := importSteps(t)
	f.refuseDocument = `{"type":"urn:bronzeward:problem:conflict","title":"Conflict","status":409,"rule":"import-not-reviewed","instance":"req_BWSYNTH"}`
	ctl, term := onPty(t, 24, 80)
	screen, done := typeAt(ctl)
	var e bytes.Buffer
	got := run([]string{"import-base", "document", "ibr_BWSYNTH"}, term, &e)
	term.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal stayed open after the command ended")
	}
	if got != 1 || !strings.Contains(e.String(), "import-not-reviewed") {
		t.Errorf("exit %d, stderr %q; want 1 and the refusal's rule", got, e.String())
	}
	if s := screen(); strings.Contains(s, "BWSYNTH-base-text") || strings.Contains(s, "protected review") {
		t.Errorf("the terminal shows the notice or text after a refusal: %q", s)
	}
	if f.documents != 1 {
		t.Errorf("the document was read %d times; want once", f.documents)
	}
}
