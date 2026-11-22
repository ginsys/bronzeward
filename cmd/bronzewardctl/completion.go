package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
)

// completionCmd prints the completion script of a shell. It reads no server and no context: the script is made
// from the command table and from the flag set each leaf builds, so it lists what help shows.
func (e *env) completionCmd(args []string) error {
	pos, err := e.parse("completion", args, 1, nil)
	if err != nil {
		return err
	}
	m, err := newCompModel()
	if err != nil {
		return err
	}
	var text string
	switch pos[0] {
	case "bash":
		text = m.bash()
	case "zsh":
		text = m.zsh()
	case "fish":
		text = m.fish()
	default:
		return usageError{fmt.Errorf("completion: give bash, zsh or fish, not \"%s\"", client.Quoted(pos[0]))}
	}
	_, err = io.WriteString(e.out, text)
	return err
}

// compModel is what the three scripts are made from.
type compModel struct {
	tops    []string                // the top-level names
	verbs   map[string][]string     // a group's leaves
	flags   map[string][]*flag.Flag // a leaf's own flags, by path, the ones help shows
	globals []*flag.Flag            // the flags every command takes
}

func newCompModel() (*compModel, error) {
	m := &compModel{tops: topLevel(), verbs: map[string][]string{}, flags: map[string][]*flag.Flag{}}
	for i := range cmdTable {
		x := &cmdTable[i]
		if x.synopsis == "" {
			m.verbs[x.path] = verbsOf(x.path)
			continue
		}
		fs, err := probeFlags(x.path)
		if err != nil {
			return nil, fmt.Errorf("completion: %s: %v", x.path, err)
		}
		if m.flags[x.path] = visibleFlags(x, fs); m.globals == nil {
			fs.VisitAll(func(f *flag.Flag) {
				if slices.Contains(globalFlags, f.Name) {
					m.globals = append(m.globals, f)
				}
			})
		}
	}
	return m, nil
}

// spell is a flag as it is offered: -o, --context.
func spell(f *flag.Flag) string {
	if len(f.Name) == 1 {
		return "-" + f.Name
	}
	return "--" + f.Name
}

func spellAll(fl []*flag.Flag) string {
	var s []string
	for _, f := range fl {
		s = append(s, spell(f))
	}
	return strings.Join(s, " ")
}

// takesValue is whether a flag reads the next word as its value, so that a shell does not read that word as the
// command or its verb.
func takesValue(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !b.IsBoolFlag()
}

// valued is the case pattern of the global flags that take a value, in both spellings the parser accepts.
func (m *compModel) valued() string {
	var p []string
	for _, f := range m.globals {
		if takesValue(f) {
			p = append(p, "-"+f.Name, "--"+f.Name)
		}
	}
	return strings.Join(p, "|")
}

// groups are the top-level names that hold leaves, in table order.
func (m *compModel) groups() []string {
	return slices.DeleteFunc(slices.Clone(m.tops), func(t string) bool { return m.verbs[t] == nil })
}

// bash and zsh read the command and its verb as the first two words that are no flag and no flag's value; the key
// is "group verb" for a group and the name for a leaf, the same as the table's path.
func (m *compModel) bash() string {
	var b strings.Builder
	w := func(s string, a ...any) { fmt.Fprintf(&b, s, a...) }
	w("# bash completion for bronzewardctl, written by `bronzewardctl completion bash`.\n_bronzewardctl() {\n")
	w("  local cur=${COMP_WORDS[COMP_CWORD]} cmd= verb= key flags i\n  COMPREPLY=()\n")
	// bash splits --context=x into three words (--context, =, x): the = and what follows it are the flag's value too.
	w("  for ((i = 1; i < COMP_CWORD; i++)); do\n    case ${COMP_WORDS[i]} in\n      %s) if [[ ${COMP_WORDS[i+1]} == = ]]; then ((i += 2)); else ((i++)); fi ;;\n      =) ((i++)) ;;\n      -*) ;;\n", m.valued())
	// A skip that ran past the word under the cursor means the cursor is a flag's value: nothing is offered.
	w("      *) if [[ -z $cmd ]]; then cmd=${COMP_WORDS[i]}; elif [[ -z $verb ]]; then verb=${COMP_WORDS[i]}; fi ;;\n    esac\n  done\n  ((i > COMP_CWORD)) && return\n")
	w("  if [[ -z $cmd && $cur != -* ]]; then\n    mapfile -t COMPREPLY < <(compgen -W %q -- \"$cur\")\n    return\n  fi\n", strings.Join(m.tops, " "))
	w("  case $cmd in\n    %s) key=\"$cmd $verb\" ;;\n    *) key=$cmd ;;\n  esac\n", strings.Join(m.groups(), "|"))
	w("  if [[ $cur == -* ]]; then\n    case $key in\n")
	for _, x := range cmdTable {
		if fl := m.flags[x.path]; len(fl) > 0 {
			w("      %q) flags=%q ;;\n", x.path, spellAll(fl))
		}
	}
	w("    esac\n    mapfile -t COMPREPLY < <(compgen -W \"$flags %s\" -- \"$cur\")\n", spellAll(m.globals))
	w("  elif [[ -z $verb ]]; then\n    case $cmd in\n")
	for _, g := range m.groups() {
		w("      %s) mapfile -t COMPREPLY < <(compgen -W %q -- \"$cur\") ;;\n", g, strings.Join(m.verbs[g], " "))
	}
	w("    esac\n  fi\n}\ncomplete -F _bronzewardctl bronzewardctl\n")
	return b.String()
}

func (m *compModel) zsh() string {
	var b strings.Builder
	w := func(s string, a ...any) { fmt.Fprintf(&b, s, a...) }
	w("#compdef bronzewardctl\n# zsh completion for bronzewardctl, written by `bronzewardctl completion zsh`.\n_bronzewardctl() {\n")
	w("  local cmd verb key w i\n  local -a flags\n")
	w("  for ((i = 2; i < CURRENT; i++)); do\n    w=$words[i]\n    case $w in\n      %s) ((i++)) ;;\n      -*) ;;\n", m.valued())
	w("      *) if [[ -z $cmd ]]; then cmd=$w; elif [[ -z $verb ]]; then verb=$w; fi ;;\n    esac\n  done\n  ((i > CURRENT)) && return\n")
	w("  if [[ -z $cmd && $PREFIX != -* ]]; then\n    compadd -- %s\n    return\n  fi\n", strings.Join(m.tops, " "))
	w("  case $cmd in\n    %s) key=\"$cmd $verb\" ;;\n    *) key=$cmd ;;\n  esac\n", strings.Join(m.groups(), "|"))
	w("  if [[ $PREFIX == -* ]]; then\n    case $key in\n")
	for _, x := range cmdTable {
		if fl := m.flags[x.path]; len(fl) > 0 {
			w("      %q) flags=(%s) ;;\n", x.path, spellAll(fl))
		}
	}
	w("    esac\n    compadd -- $flags %s\n  elif [[ -z $verb ]]; then\n    case $cmd in\n", spellAll(m.globals))
	for _, g := range m.groups() {
		w("      %s) compadd -- %s ;;\n", g, strings.Join(m.verbs[g], " "))
	}
	// Autoloaded from $fpath the file is the function's body, so it calls the function it defined; sourced, it registers it.
	w("    esac\n  fi\n}\nif [[ $funcstack[1] == _bronzewardctl ]]; then\n  _bronzewardctl \"$@\"\nelse\n  compdef _bronzewardctl bronzewardctl\nfi\n")
	return b.String()
}

// fish takes a command's description from the table's purpose line. The group and the verb are the words seen.
func (m *compModel) fish() string {
	var b strings.Builder
	w := func(s string, a ...any) { fmt.Fprintf(&b, s, a...) }
	opt := func(f *flag.Flag) string {
		s := "-l " + f.Name
		if len(f.Name) == 1 {
			s = "-s " + f.Name
		}
		if takesValue(f) {
			s += " -r"
		}
		return s
	}
	w("# fish completion for bronzewardctl, written by `bronzewardctl completion fish`.\ncomplete -c bronzewardctl -f\n")
	// The command and its verb are the first two words before the cursor that are no flag and no global flag's value;
	// a word later on, an argument, never counts (__fish_seen_subcommand_from would match it anywhere).
	w("function __bronzewardctl_words\n  set -l skip 0\n  for w in (commandline -opc)[2..-1]\n    if test $skip = 1\n      set skip 0\n      continue\n    end\n")
	w("    switch $w\n      case %s\n        set skip 1\n      case '-*'\n      case '*'\n        echo $w\n    end\n  end\nend\n", strings.ReplaceAll(m.valued(), "|", " "))
	w("function __bronzewardctl_at # <command> [<verb>]: the first word is the command and, when a verb is given, the second is it\n")
	w("  set -l w (__bronzewardctl_words)\n  test \"$w[1]\" = $argv[1]; and begin; test (count $argv) -lt 2; or test \"$w[2]\" = $argv[2]; end\nend\n")
	w("function __bronzewardctl_verbs # <group>: the group is the only word so far\n  set -l w (__bronzewardctl_words)\n  test \"$w[1]\" = $argv[1]; and test (count $w) -eq 1\nend\n")
	for _, f := range m.globals {
		w("complete -c bronzewardctl %s\n", opt(f))
	}
	for _, t := range m.tops {
		w("complete -c bronzewardctl -n 'test (count (__bronzewardctl_words)) -eq 0' -a %s -d %q\n", t, lookup(t).line)
	}
	for _, g := range m.groups() {
		w("complete -c bronzewardctl -n '__bronzewardctl_verbs %s' -a %q\n", g, strings.Join(m.verbs[g], " "))
	}
	for _, x := range cmdTable {
		for _, f := range m.flags[x.path] {
			w("complete -c bronzewardctl -n '__bronzewardctl_at %s' %s\n", x.path, opt(f))
		}
	}
	return b.String()
}
