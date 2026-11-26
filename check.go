package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/Tarektouati/made/internal/discover"
	"github.com/Tarektouati/made/internal/display"
	"github.com/Tarektouati/made/internal/lint"
)

// runCheck lints the task file at path for made --check, prints the findings to
// stdout, and returns the exit code: 1 if there is an error (or, with strict, a
// warning), otherwise 0. A file that cannot be read is reported on stderr, exit 1.
// explicit and trusted are as for discover.ReadMadefile; opts selects the optional checks.
func (a *app) runCheck(stdout, stderr io.Writer, path string, explicit bool, trusted fs.FileInfo, strict bool, opts lint.Options) int {
	var diags []lint.Diagnostic
	source, _, err := discover.ReadMadefile(path, explicit, trusted)
	switch {
	case errors.Is(err, discover.ErrTooLarge):
		diags = []lint.Diagnostic{{Severity: lint.Error, Rule: "file-too-large",
			Msg: fmt.Sprintf("file exceeds the maximum size of %d bytes", discover.MaxMadefileBytes)}}
	case err != nil:
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", display.Path(path), display.Error(display.PathOnlyOnce(path, err)))
		return 1
	default:
		diags, err = withinParseTimeout(a.parseTimeout, func() []lint.Diagnostic { return lint.Check(source, opts) })
		if err != nil {
			diags = []lint.Diagnostic{{Severity: lint.Error, Rule: "parse-timeout", Msg: err.Error()}}
		}
	}
	errs, warns := printDiagnostics(stdout, path, diags)
	if errs > 0 || (strict && warns > 0) {
		return 1
	}
	return 0
}

// printDiagnostics writes one "path:line: severity: rule: message" line per finding
// (without ":line" for a whole-file finding) and a summary, and returns the error and
// warning counts. It prints nothing when diags is empty.
func printDiagnostics(w io.Writer, path string, diags []lint.Diagnostic) (errs, warns int) {
	if len(diags) == 0 {
		return 0, 0
	}
	path = display.Escape(path)
	for _, d := range diags {
		loc := path
		if d.Line > 0 {
			loc = fmt.Sprintf("%s:%d", path, d.Line)
		}
		fmt.Fprintf(w, "%s: %s: %s: %s\n", loc, d.Severity, d.Rule, display.Sanitize(d.Msg))
		if d.Severity == lint.Warning {
			warns++
		} else {
			errs++
		}
	}
	fmt.Fprintf(w, "%s (%s, %s)\n", plural(len(diags), "problem"), plural(errs, "error"), plural(warns, "warning"))
	return errs, warns
}

// plural formats n with noun, adding "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
