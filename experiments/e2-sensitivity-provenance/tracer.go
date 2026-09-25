package main

import (
	"fmt"
	"strings"
)

// shapeTracer gives a value a stand-in of the same shape that can be recognized after composition
// and in a message: every letter becomes x (X), every digit 0, and every other byte (spaces, quotes,
// tabs, backslashes, newlines, anything that decides how YAML or JSON quotes it and how a
// validation rule judges it) stays. Each line of five bytes or more then starts with `zq` and the
// tracer's three-digit id, so that a line or a prefix a message quotes still names its tracer.
func shapeTracer(real string, id int) string {
	head := fmt.Sprintf("zq%03d", id)
	lines := strings.Split(real, "\n")
	for i, line := range lines {
		b := []byte(line)
		for j, c := range b {
			switch {
			case c >= 'a' && c <= 'z':
				b[j] = 'x'
			case c >= 'A' && c <= 'Z':
				b[j] = 'X'
			case c >= '0' && c <= '9':
				b[j] = '0'
			}
		}
		if len(b) >= len(head) {
			copy(b, head)
		}
		lines[i] = string(b)
	}
	return strings.Join(lines, "\n")
}

// intTracer is an integer reference's stand-in: 61000 plus its tracer id, a valid port.
func intTracer(id int) string {
	return fmt.Sprintf("%d", 61000+id)
}
