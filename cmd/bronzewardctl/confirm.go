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
	got, err := e.answer(client.Printable("Type "+what+" to send it, or press Enter to stop: "), what)
	if err != nil {
		return err
	}
	if got == want {
		return nil
	}
	return notConfirmed{fmt.Errorf("the typed text is not %s; nothing was sent", what)}
}

// confirmYes asks one question, [y/N] after it, from the same place confirmTyped reads: only y or yes
// in any case sends. The default is no, so Enter, no answer, end of input and anything else stop with
// nothing sent. The caller passes the question ("Record accounting for machine w1 (cluster prod)?");
// there is no --yes, so a script cannot record the judgment unseen.
func (e *env) confirmYes(question string) error {
	got, err := e.answer(client.Printable(question)+" [y/N]: ", "an answer")
	if err != nil {
		return err
	}
	if strings.EqualFold(got, "y") || strings.EqualFold(got, "yes") {
		return nil
	}
	return notConfirmed{errors.New("stopped: the answer is not y or yes; nothing was sent")}
}

// answer shows prompt and returns the one line typed at it, without its line end. An empty line stops
// before the caller compares it. what names the answer in the errors.
func (e *env) answer(prompt, what string) (string, error) {
	in := io.Reader(stdin)
	if e.tokenStdin {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return "", notConfirmed{errors.New("--token-stdin took standard input and there is no terminal to answer on; nothing was sent")}
		}
		defer tty.Close()
		in = tty
	}
	if _, err := fmt.Fprint(e.err, prompt); err != nil {
		return "", notConfirmed{fmt.Errorf("showing the prompt: %w; nothing was sent", err)}
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if !isTerminal(stdin) || e.tokenStdin {
		fmt.Fprintln(e.err)
	}
	got := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch {
	case got == "" && (err == nil || errors.Is(err, io.EOF)):
		return "", notConfirmed{errors.New("stopped: nothing was typed; nothing was sent")}
	case err != nil && !errors.Is(err, io.EOF):
		return "", notConfirmed{fmt.Errorf("reading %s: %w; nothing was sent", what, err)}
	}
	return got, nil
}
