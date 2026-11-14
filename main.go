// Command made is a Markdown-driven task runner.
//
// Tasks are H3 headings in a Markdown file (README.md by default), each followed by
// a fenced code block. `made <task> [args...]` runs that block with args as its
// positional parameters; `made` with no task lists them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
)

const defaultFile = "README.md"

// version is set at release time via -ldflags "-X main.version=<tag>".
var version = ""

// resolveVersion reports the build version: the ldflags-injected value if present,
// otherwise the module version from the build info (e.g. a `go install …@v1.2.3`),
// falling back to "dev" for local builds.
func resolveVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run holds the program logic and returns the process exit code so deferred cleanup
// and a single os.Exit site stay together. args excludes the program name. made's own
// output goes to stdout and stderr; a task's process still inherits the real
// os.Stdin/os.Stdout/os.Stderr.
func run(args []string, stdout, stderr io.Writer) int {
	var file string
	var list bool
	var showVersion bool
	var noChdir bool
	fs := flag.NewFlagSet("made", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&file, "file", "", "Markdown file to read tasks from (default: nearest README.md in the current or a parent directory)")
	fs.StringVar(&file, "f", "", "shorthand for -file")
	fs.BoolVar(&noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	fs.BoolVar(&list, "list", false, "list available tasks")
	fs.BoolVar(&list, "l", false, "list available tasks (shorthand)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "V", false, "print version and exit (shorthand)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: made [-f file] [--no-chdir] [task [--] [args...]]\n\n")
		fmt.Fprintf(stderr, "Run a task defined as an H3 heading + code block in a Markdown file.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		// fs has already reported the error and the usage on stderr. Keep the exit
		// codes flag.ExitOnError used: 0 for -h/-help, 2 for a bad flag.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if showVersion {
		fmt.Fprintln(stdout, "made", resolveVersion())
		return 0
	}

	fromSearch := file == ""
	file, err := resolveFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "made: %v\n", err)
		return 1
	}

	source, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %v\n", file, err)
		return 1
	}

	tasks, err := parser.Parse(source)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot parse %s: %v\n", file, err)
		return 1
	}

	if list || fs.NArg() == 0 {
		printTasks(stdout, stderr, file, tasks)
		return 0
	}

	name := fs.Arg(0)
	task, ok := find(tasks, name)
	if !ok {
		fmt.Fprintf(stderr, "made: unknown task %q\n\n", name)
		printTasks(stdout, stderr, file, tasks)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := ""
	if !noChdir {
		abs, err := filepath.Abs(file)
		if err != nil {
			fmt.Fprintf(stderr, "made: %v\n", err)
			return 1
		}
		dir = filepath.Dir(abs)
	}

	if file != defaultFile && fromSearch {
		fmt.Fprintf(stderr, "made: using %s\n", file)
	}

	// Everything after the task name goes to the script. Flag parsing already stopped
	// at the task name, so a leading "--" is optional; drop one if present.
	taskArgs := fs.Args()[1:]
	if len(taskArgs) > 0 && taskArgs[0] == "--" {
		taskArgs = taskArgs[1:]
	}

	if err := runner.Run(ctx, task, dir, taskArgs); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "made: %v\n", err)
		return 1
	}
	return 0
}

// resolveFile returns the task file to read. A non-empty file (given with -f) is used
// as is. Otherwise the nearest defaultFile in the current directory or one of its
// parents is used; if there is none, defaultFile is returned unchanged so the caller
// reports the usual "cannot read" error.
func resolveFile(file string) (string, error) {
	if file != "" {
		return file, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine current directory: %w", err)
	}
	dir, ok := findUp(cwd, isRegularFile)
	if !ok || dir == cwd {
		return defaultFile, nil // keep the short name when it's right here
	}
	return filepath.Join(dir, defaultFile), nil
}

// findUp walks from start up to the filesystem root and returns the first directory
// whose defaultFile satisfies isFile.
func findUp(start string, isFile func(path string) bool) (dir string, ok bool) {
	for dir = start; ; {
		if isFile(filepath.Join(dir, defaultFile)) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// isRegularFile reports whether path exists and is not a directory.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func find(tasks []parser.Task, name string) (parser.Task, bool) {
	for _, t := range tasks {
		if t.Name == name {
			return t, true
		}
	}
	return parser.Task{}, false
}

func printTasks(stdout, stderr io.Writer, file string, tasks []parser.Task) {
	if len(tasks) == 0 {
		fmt.Fprintf(stderr, "made: no tasks found in %s\n", file)
		return
	}
	fmt.Fprintf(stdout, "Tasks in %s:\n", file)
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, t := range tasks {
		desc := strings.ReplaceAll(t.Description, "\n", " ")
		fmt.Fprintf(w, "  %s\t%s\n", t.Name, desc)
	}
	_ = w.Flush()
}
