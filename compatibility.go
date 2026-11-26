package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Tarektouati/made/internal/selfupdate"
)

// warnMadefileVersion reads only the first-line marker written by made init.
// It records the writer's version, not a guaranteed minimum requirement.
func warnMadefileVersion(w io.Writer, path string, source []byte, current string) {
	line := string(source)
	if end := strings.IndexAny(line, "\r\n"); end >= 0 {
		line = line[:end]
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	marked, ok := strings.CutPrefix(line, "<!-- made ")
	if !ok {
		return
	}
	recorded, ok := strings.CutSuffix(marked, " -->")
	if !ok {
		return
	}
	// NormalizeTag rejects control characters and other non-version text before
	// anything from the comment reaches a terminal. Require the canonical v prefix.
	tag, err := selfupdate.NormalizeTag(recorded)
	if err != nil || tag != recorded || !selfupdate.Newer(current, recorded) {
		return
	}
	fmt.Fprintf(w, "made: %s was written with made %s (you have %s); tasks may behave differently\n", displayPath(path), escapeDisplay(recorded), escapeDisplay(current))
}
