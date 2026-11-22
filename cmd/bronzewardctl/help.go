package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
)

// helpText is the help of the command parse last read, from the table: a leaf's own text with the flags of the
// flag set it built, a group's leaves, or the index when no command was read or the path names none.
func (e *env) helpText() string {
	switch x := lookup(e.where); {
	case x == nil:
		return indexText()
	case x.synopsis == "":
		return groupText(x.path)
	default:
		return leafText(x, e.helped)
	}
}

// helpCmd is help: the index, `help global`, a group's leaves, or a leaf's help, the same text as
// `<leaf> --help`.
func (e *env) helpCmd(args []string) error {
	pos, err := e.parse("help", args, anyArgs, nil)
	if err != nil {
		return err
	}
	// "machine add" in one argument names the same command as machine add in two.
	words := strings.Fields(strings.Join(pos, " "))
	path := strings.Join(words, " ")
	var text string
	switch x := lookup(path); {
	case path == "":
		text = indexText()
	case path == "global":
		text = globalTopic
	case x == nil:
		return usageError{fmt.Errorf("help: unknown command \"%s\"", client.Quoted(path))}
	case x.synopsis == "":
		text = groupText(x.path)
	default:
		// The leaf runs with --help only: its flag set is read before it reads or sends anything.
		probe := &env{output: "table", err: io.Discard, out: io.Discard}
		handler := probe.handlers()[words[0]]
		if handler == nil {
			return fmt.Errorf("help %s: no command answers it", client.Quoted(path))
		}
		if herr := handler(append(slices.Clone(words[1:]), "--help")); !errors.Is(herr, flag.ErrHelp) {
			return fmt.Errorf("help %s: the command did not answer --help: %v", client.Quoted(path), herr)
		}
		text = leafText(x, probe.helped)
	}
	_, err = fmt.Fprintln(e.out, text)
	return err
}
