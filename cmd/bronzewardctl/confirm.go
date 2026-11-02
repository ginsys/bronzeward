package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
)

// notConfirmed is a write the operator did not confirm, or could not be asked to: nothing was
// sent, and the CLI exits 2.
type notConfirmed struct{ error }

// confirmTyped asks for want typed back, what naming it in the prompt: from standard input, a
// terminal or one line of a pipe, unless --token-stdin took it, then from the controlling
// terminal. A wrong or empty answer sends nothing.
func (e *env) confirmTyped(what, want string) error {
	in := io.Reader(stdin)
	if e.tokenStdin {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return notConfirmed{fmt.Errorf("--token-stdin took standard input and there is no terminal to type %s on; nothing was sent", what)}
		}
		defer tty.Close()
		in = tty
	}
	fmt.Fprintf(e.err, "Type %s to send it, or press Enter to stop: ", client.Printable(what))
	line, err := bufio.NewReader(in).ReadString('\n')
	if !isTerminal(stdin) || e.tokenStdin {
		fmt.Fprintln(e.err)
	}
	switch got := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"); {
	case got == want:
		return nil
	case got == "" && (err == nil || errors.Is(err, io.EOF)):
		return notConfirmed{errors.New("stopped: nothing was typed; nothing was sent")}
	case err != nil && !errors.Is(err, io.EOF):
		return notConfirmed{fmt.Errorf("reading %s: %w; nothing was sent", what, err)}
	}
	return notConfirmed{fmt.Errorf("the typed text is not %s; nothing was sent", what)}
}
