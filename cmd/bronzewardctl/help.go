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
		fs, perr := probeFlags(path)
		if perr != nil {
			return fmt.Errorf("help %s: %v", client.Quoted(path), perr)
		}
		text = leafText(x, fs)
	}
	_, err = fmt.Fprintln(e.out, text)
	return err
}

// probeFlags is the flag set a leaf builds, read by running the leaf with --help only: it is read before the
// leaf reads or sends anything.
func probeFlags(path string) (*flag.FlagSet, error) {
	words := strings.Fields(path)
	probe := &env{output: "table", err: io.Discard, out: io.Discard}
	handler := probe.handlers()[words[0]]
	if handler == nil {
		return nil, errors.New("no command answers it")
	}
	if err := handler(append(slices.Clone(words[1:]), "--help")); !errors.Is(err, flag.ErrHelp) {
		return nil, fmt.Errorf("the command did not answer --help: %v", err)
	}
	return probe.helped, nil
}
