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
	os.Exit(run())
}

// run holds the program logic and returns the process exit code so deferred cleanup
// and a single os.Exit site stay together.
func run() int {
	var file string
	var list bool
	var showVersion bool
	var noChdir bool
	flag.StringVar(&file, "file", "", "Markdown file to read tasks from (default: nearest README.md in the current or a parent directory)")
	flag.StringVar(&file, "f", "", "shorthand for -file")
	flag.BoolVar(&noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	flag.BoolVar(&list, "list", false, "list available tasks")
	flag.BoolVar(&list, "l", false, "list available tasks (shorthand)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "V", false, "print version and exit (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: made [-f file] [--no-chdir] [task [--] [args...]]\n\n")
		fmt.Fprintf(os.Stderr, "Run a task defined as an H3 heading + code block in a Markdown file.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Println("made", resolveVersion())
		return 0
	}

	fromSearch := file == ""
	file, err := resolveFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "made: %v\n", err)
		return 1
	}

	source, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "made: cannot read %s: %v\n", file, err)
		return 1
	}

	tasks, err := parser.Parse(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "made: cannot parse %s: %v\n", file, err)
		return 1
	}

	if list || flag.NArg() == 0 {
		printTasks(file, tasks)
		return 0
	}

	name := flag.Arg(0)
	task, ok := find(tasks, name)
	if !ok {
		fmt.Fprintf(os.Stderr, "made: unknown task %q\n\n", name)
		printTasks(file, tasks)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := ""
	if !noChdir {
		abs, err := filepath.Abs(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "made: %v\n", err)
			return 1
		}
		dir = filepath.Dir(abs)
	}

	if file != defaultFile && fromSearch {
		fmt.Fprintf(os.Stderr, "made: using %s\n", file)
	}

	// Everything after the task name goes to the script. Flag parsing already stopped
	// at the task name, so a leading "--" is optional; drop one if present.
	taskArgs := flag.Args()[1:]
	if len(taskArgs) > 0 && taskArgs[0] == "--" {
		taskArgs = taskArgs[1:]
	}

	if err := runner.Run(ctx, task, dir, taskArgs); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "made: %v\n", err)
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

func printTasks(file string, tasks []parser.Task) {
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "made: no tasks found in %s\n", file)
		return
	}
	fmt.Printf("Tasks in %s:\n", file)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, t := range tasks {
		desc := strings.ReplaceAll(t.Description, "\n", " ")
		fmt.Fprintf(w, "  %s\t%s\n", t.Name, desc)
	}
	_ = w.Flush()
}
