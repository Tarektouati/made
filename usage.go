package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/Tarektouati/made/internal/completion"
	"github.com/Tarektouati/made/internal/display"
	"github.com/Tarektouati/made/internal/task"
)

func printUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, "Usage: made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]\n")
	fmt.Fprintf(w, "       made [-f file] --check [--strict] [--host]\n")
	fmt.Fprintf(w, "       made init [--force]\n")
	fmt.Fprintf(w, "       made completion <%s>\n", strings.Join(completion.Shells, "|"))
	fmt.Fprintf(w, "       made self-update [vX.Y.Z] [--dry-run]\n\n")
	fmt.Fprintf(w, "Run a task defined as an H3 heading + code block in a Markdown file.\n")
	fmt.Fprintf(w, "`made init` creates a starter Madefile.md in the current directory;\n")
	fmt.Fprintf(w, "--force overwrites an existing one.\n")
	fmt.Fprintf(w, "`made completion <shell>` prints a shell completion script.\n")
	fmt.Fprintf(w, "`made self-update` replaces made with the latest release (or vX.Y.Z).\n")
	fmt.Fprintf(w, "`made --check` reports problems in the task file without running anything.\n\n")
	printOptions(w, fs, mainOptions)
}

func printTasks(stdout, stderr io.Writer, file string, tasks []task.Task) {
	if len(tasks) == 0 {
		fmt.Fprintf(stderr, "made: no tasks found in %s\n", display.Escape(file))
		return
	}
	fmt.Fprintf(stdout, "Tasks in %s:\n", display.Escape(file))
	names := make([]string, len(tasks))
	widths := make([]int, len(tasks))
	maxW := 0
	for i, t := range tasks {
		names[i] = display.Sanitize(t.Name)
		widths[i] = display.Width(names[i])
		maxW = max(maxW, widths[i])
	}
	for i, t := range tasks {
		desc := display.Sanitize(strings.ReplaceAll(t.Description, "\n", " "))
		fmt.Fprintf(stdout, "  %s%s%s\n", names[i], strings.Repeat(" ", maxW-widths[i]+2), desc)
	}
}

// printNames writes one task per line as "name<TAB>description" for the completion
// scripts, with no header. The description is flattened to one line. Tasks whose
// name is empty once sanitized are left out: they cannot be completed, and bash's
// read would take the description of a "\tdesc" line as the name.
func printNames(stdout io.Writer, tasks []task.Task) {
	for _, t := range tasks {
		name := display.Sanitize(t.Name)
		if name == "" {
			continue
		}
		desc := display.Sanitize(strings.ReplaceAll(t.Description, "\n", " "))
		fmt.Fprintf(stdout, "%s\t%s\n", name, desc)
	}
}

// usageOption is one line of the options list in the usage text: an optional short
// name, the long name, and an optional argument placeholder.
type usageOption struct{ short, long, arg string }

// mainOptions are the flags shown by made --help, in display order. Flags left out
// (list-names) stay hidden.
var mainOptions = []usageOption{
	{short: "f", long: "file", arg: "file"},
	{long: "no-chdir"},
	{long: "check"},
	{long: "strict"},
	{long: "host"},
	{short: "l", long: "list"},
	{short: "V", long: "version"},
	{short: "h", long: "help"},
}

// initOptions are the flags shown by made init --help.
var initOptions = []usageOption{
	{long: "force"},
	{short: "h", long: "help"},
}

// selfUpdateOptions are the flags shown by made self-update --help.
var selfUpdateOptions = []usageOption{
	{long: "dry-run"},
	{short: "h", long: "help"},
}

// flagError describes err, the error fs.Parse(args) returned, naming the flag as it
// was typed (flag's own errors drop a dash from --name). Parse leaves fs.Args() just
// past the failing argument, except for bad syntax such as ---x, which it rejects
// before consuming it.
func flagError(fs *flag.FlagSet, args []string, err error) string {
	rest := fs.Args()
	if strings.HasPrefix(err.Error(), "bad flag syntax") && len(rest) > 0 {
		return "unknown flag " + display.Escape(rest[0])
	}
	var arg string
	if i := len(args) - len(rest) - 1; i >= 0 {
		arg = args[i]
	}
	typed, _, hasValue := strings.Cut(arg, "=")
	f := fs.Lookup(strings.TrimPrefix(strings.TrimPrefix(typed, "-"), "-"))
	typed = display.Escape(typed)
	switch {
	case f == nil:
		return "unknown flag " + typed
	case !hasValue && !isBoolFlag(f):
		return "flag " + typed + " needs an argument"
	default:
		return "invalid value for flag " + typed
	}
}

// isBoolFlag reports whether f takes no argument, like -l.
func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// printOptions writes opts as an aligned "-s, --long <arg>  description" list. The
// description is the usage registered on fs for the long name; help is built into
// package flag and has no registration.
func printOptions(w io.Writer, fs *flag.FlagSet, opts []usageOption) {
	fmt.Fprintln(w, "Options:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, o := range opts {
		names := "    --" + o.long
		if o.short != "" {
			names = "-" + o.short + ", --" + o.long
		}
		if o.arg != "" {
			names += " <" + o.arg + ">"
		}
		desc := "print this help and exit"
		if f := fs.Lookup(o.long); f != nil {
			desc = f.Usage
		}
		fmt.Fprintf(tw, "  %s\t%s\n", names, desc)
	}
	_ = tw.Flush()
}

// firstSetFlag returns the first flag set on fs's command line, written as "-x" or
// "--name", skipping the names in except; it returns "" if there is none.
func firstSetFlag(fs *flag.FlagSet, except ...string) string {
	var name string
	fs.Visit(func(f *flag.Flag) {
		if name != "" || slices.Contains(except, f.Name) {
			return
		}
		if len(f.Name) == 1 {
			name = "-" + f.Name
		} else {
			name = "--" + f.Name
		}
	})
	return name
}
