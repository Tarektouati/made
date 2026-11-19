// Package lint statically checks a task file for mistakes the parser tolerates
// silently or that only fail when a task runs. It backs made --check.
package lint

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
)

// Severity ranks a Diagnostic. Errors fail made --check; warnings fail it only with
// --strict.
type Severity int

const (
	// Error marks a problem that breaks a task or hides one.
	Error Severity = iota
	// Warning marks something that is probably not what the author meant.
	Warning
)

// String returns "error" or "warning", as printed by made --check.
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is one problem found in a task file. Line is 1-based; 0 means the
// problem concerns the whole file.
type Diagnostic struct {
	Line     int
	Severity Severity
	Rule     string
	Msg      string
}

// docsLangs are fence languages that mark an example rather than a script. A task
// using one is documentation that happens to sit under an H3 heading.
var docsLangs = []string{"markdown", "md", "text", "txt", "plaintext", "console", "output"}

// reservedNames are task names made handles itself, so the task never runs.
var reservedNames = []string{"init", "completion"}

// Options selects the optional checks of Check.
type Options struct {
	// Host adds the checks that depend on this machine rather than on the file and the
	// OS: interpreter-not-found, from looking each task's interpreter up on PATH.
	Host bool
}

// Check reports the problems in a task file's source, sorted by line.
func Check(src []byte, opts Options) []Diagnostic {
	tasks, err := parser.ParseDetailed(src)
	if err != nil {
		var nest *parser.NestingError
		if errors.As(err, &nest) {
			return []Diagnostic{{nest.Line, Error, "nesting-too-deep", err.Error()}}
		}
		return []Diagnostic{{0, Error, "parse", err.Error()}}
	}

	var diags []Diagnostic
	add := func(line int, sev Severity, rule, format string, args ...any) {
		diags = append(diags, Diagnostic{line, sev, rule, fmt.Sprintf(format, args...)})
	}
	defined := map[string]int{}    // task name -> line of its first definition
	resolved := map[string]error{} // normalized language -> runner.Resolve's result

	for _, t := range tasks {
		// A heading without a code block is not a task, so its name never matters.
		if len(t.Fences) == 0 {
			add(t.Line, Warning, "no-code-block", "heading %q has no code block, so it is not a task", t.Raw)
			continue
		}
		switch {
		case t.Name == "":
			add(t.Line, Error, "empty-name", "heading %q gives an empty task name", t.Raw)
		case t.Altered:
			add(t.Line, Warning, "name-altered", "heading %q is read as task %q; HTML tags and images are not part of names", t.Raw, t.Name)
		}
		if slices.Contains(reservedNames, t.Name) {
			add(t.Line, Warning, "unreachable-name", "task %q cannot be run: made %s is reserved", t.Name, t.Name)
		} else if strings.HasPrefix(t.Name, "-") {
			add(t.Line, Warning, "unreachable-name", "task %q starts with \"-\": run it as made -- %s (plain made %s and completion won't work)", t.Name, t.Name, t.Name)
		}

		if t.Name != "" {
			if first, ok := defined[t.Name]; ok {
				add(t.Line, Error, "duplicate-task", "%q already defined at line %d", t.Name, first)
			} else {
				defined[t.Name] = t.Line
			}
		}

		f := t.Fences[0]
		lang := runner.NormalizeLang(f.Lang)
		supported := runner.Supported(f.Lang)
		switch {
		case slices.Contains(docsLangs, lang):
			add(f.Line, Warning, "docs-fence", "task %q uses a %q code block, which looks like documentation; it cannot be run", t.Name, f.Lang)
		case !supported:
			add(f.Line, Error, "unsupported-language", "task %q uses %q; supported: %s", t.Name, f.Lang, strings.Join(runner.Languages(), ", "))
		}
		empty := strings.TrimSpace(f.Script) == ""
		if opts.Host && supported && !empty {
			if err := resolve(resolved, t.Name, f.Lang); err != nil {
				add(f.Line, Error, "interpreter-not-found", "%s", err)
			}
		}
		if empty {
			add(f.Line, Error, "empty-script", "task %q has an empty code block", t.Name)
		} else if i := strings.IndexByte(f.Script, 0); i >= 0 {
			add(f.Line+1+strings.Count(f.Script[:i], "\n"), Error, "nul-byte", "task %q script contains a NUL byte", t.Name)
		}
		if runner.ScriptTooLarge(f.Lang, f.Script) {
			add(f.Line, Error, "script-too-large", "task %q script is %d %s; the OS rejects a command-line argument of about %d %s or more, so it cannot run; move the bulk of it into a file the task runs",
				t.Name, runner.ScriptLen(f.Lang, f.Script), runner.ArgUnit, runner.ArgLimit, runner.ArgUnit)
		}
		for _, extra := range t.Fences[1:] {
			add(extra.Line, Warning, "extra-code-block", "ignored: task %q runs only its first code block (line %d)", t.Name, f.Line)
		}
	}

	slices.SortStableFunc(diags, func(a, b Diagnostic) int { return cmp.Compare(a.Line, b.Line) })
	return diags
}

// resolve returns runner.Resolve's error for task name with fence language lang, or
// nil. The interpreter is looked up once per language; cache holds the results.
func resolve(cache map[string]error, name, lang string) error {
	key := runner.NormalizeLang(lang)
	err, ok := cache[key]
	if !ok {
		err = runner.Resolve(parser.Task{Name: name, Lang: lang})
		cache[key] = err
	}
	var ie *runner.InterpreterError
	if errors.As(err, &ie) {
		named := *ie
		named.Task, named.Lang = name, lang
		return &named
	}
	return err
}
