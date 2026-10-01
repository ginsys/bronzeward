package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fileToken = "s.file-token-value-BWSYNTH"

func writeToken(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenFileModes(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o620, 0o602, 0o610} {
		if _, err := ReadTokenFile(writeToken(t, fileToken+"\n", mode)); err == nil {
			t.Errorf("mode %o accepted", mode)
		}
	}
	for _, content := range []string{"", "\n", "a b", "a\nb\n", "a\tb", "tok\x00"} {
		if _, err := ReadTokenFile(writeToken(t, content, 0o600)); err == nil {
			t.Errorf("content %q accepted", content)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		tok, err := ReadTokenFile(writeToken(t, fileToken+"\n", mode))
		if err != nil {
			t.Fatalf("mode %o: %v", mode, err)
		}
		if tok.value() != fileToken {
			t.Fatal("the trailing newline was not trimmed, or the token was altered")
		}
	}
	tok, err := ReadTokenFile(writeToken(t, fileToken, 0o600))
	if err != nil || tok.value() != fileToken {
		t.Fatalf("a token without a newline: %v", err)
	}

	// A symlink, even to a 0600 file, and a directory are refused.
	dir := t.TempDir()
	target := writeToken(t, fileToken, 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(link); err == nil {
		t.Error("a symlink was followed")
	}
	if _, err := ReadTokenFile(dir); err == nil {
		t.Error("a directory was accepted")
	}
	if _, err := ReadTokenFile(filepath.Join(dir, "absent")); err == nil {
		t.Error("an absent file was accepted")
	}

	// No rendering of a Token shows it, at any depth: a struct holding one in an unexported
	// field is printed by reflection, which no method of Token can intercept.
	holder := struct {
		t   Token
		Tok Token
	}{tok, tok}
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		for _, v := range []any{tok, &tok, holder, &holder} {
			if s := fmt.Sprintf(f, v); strings.Contains(s, "file-token") || strings.Contains(s, "66696c65") {
				t.Errorf("%s of %T shows the token: %s", f, v, s)
			}
		}
	}
	if tok.String() != "[openbao token]" {
		t.Errorf("String() = %q", tok.String())
	}
	// Errors name the path, never the content.
	_, err = ReadTokenFile(writeToken(t, fileToken+" x", 0o600))
	if err == nil || strings.Contains(err.Error(), "file-token") {
		t.Errorf("error %v", err)
	}
}

// A FIFO with no writer is refused as not a regular file, instead of blocking the open until a
// writer connects.
func TestTokenFileFIFORefusedWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadTokenFile(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("a FIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		// Connect a writer so the blocked open returns and the temporary directory can be removed.
		if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
		<-done
		t.Fatal("ReadTokenFile blocked opening a FIFO with no writer")
	}
}
