package client

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// TokenInput is where an invocation takes its automation token from (client-contracts.md §9.4
// item 1): a file named by --token-file, or standard input with --token-stdin. Never an argument
// value or an environment value.
type TokenInput struct {
	File  string
	Stdin bool
}

// automationToken reads the token for x: --token-stdin, else the first of --token-file, the
// context's automation.tokenFile and BRONZEWARD_TOKEN_FILE (a path) that is set.
func automationToken(x *Context, in TokenInput) (string, error) {
	if x.Credential != "automation" {
		return "", fmt.Errorf("credential %q: this bronzewardctl cannot sign in yet; use an automation context", x.Credential)
	}
	var b []byte
	var err error
	src := "standard input"
	if in.Stdin {
		b, err = io.ReadAll(os.Stdin)
	} else {
		src = in.File
		if src == "" && x.Automation != nil {
			src = x.Automation.TokenFile
		}
		if src == "" {
			src = os.Getenv("BRONZEWARD_TOKEN_FILE")
		}
		if src == "" {
			return "", errors.New("no automation token: give --token-file, --token-stdin, the context's automation.tokenFile or BRONZEWARD_TOKEN_FILE")
		}
		b, err = readOwnerOnly(src)
	}
	if err != nil {
		return "", fmt.Errorf("token from %s: %w", src, err)
	}
	t := strings.TrimSpace(string(b))
	if !strings.HasPrefix(t, "bwt_") || strings.ContainsAny(t, " \t\r\n") {
		return "", fmt.Errorf("token from %s: not an automation token (bwt_…) on one line", src)
	}
	return t, nil
}

// readOwnerOnly reads a regular file no one but its owner may read (§9.4 item 2). It opens
// without blocking, so a FIFO is refused instead of waited on.
func readOwnerOnly(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("mode %04o lets others read it; make it owner-only (chmod 600)", fi.Mode().Perm())
	}
	return io.ReadAll(f)
}
