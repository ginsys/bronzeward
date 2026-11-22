package main

import (
	"fmt"
	"strings"
)

// referenceText is docs/operator/clients/cli-reference.md: the index, the global flags and exit codes, then one
// section per command, each the text `bronzewardctl help` prints for it. A group is a level-two section and its
// leaves are level three; a leaf with no group is level two.
func referenceText() (string, error) {
	var b strings.Builder
	section := func(heading, text string) { fmt.Fprintf(&b, "%s\n\n```text\n%s\n```\n\n", heading, text) }
	b.WriteString("# bronzewardctl command reference\n\n" +
		"This page is generated from the command table of `bronzewardctl`: `mise run generate` writes it and `mise run go` fails\n" +
		"when it is stale. Edit `cmd/bronzewardctl/commandtable.go`, never this page. Each section is what `bronzewardctl help`\n" +
		"prints for that command.\n\n")
	section("## Index", indexText())
	section("## Global flags and exit codes", globalTopic)
	for _, x := range cmdTable {
		heading := "## " + x.path
		if strings.Contains(x.path, " ") {
			heading = "### " + x.path
		}
		if x.synopsis == "" {
			section(heading, groupText(x.path))
			continue
		}
		fs, err := probeFlags(x.path)
		if err != nil {
			return "", fmt.Errorf("reference: %s: %v", x.path, err)
		}
		section(heading, leafText(&x, fs))
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}
