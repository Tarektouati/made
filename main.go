// Command made is a Markdown-driven task runner.
//
// Tasks are H3 headings in a Markdown file (README.md by default), each followed by
// a fenced code block. `made <task>` runs that block; `made` with no task lists them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
)

const defaultFile = "README.md"

func main() {
	os.Exit(run())
}

// run holds the program logic and returns the process exit code so deferred cleanup
// and a single os.Exit site stay together.
func run() int {
	var file string
	var list bool
	flag.StringVar(&file, "file", defaultFile, "Markdown file to read tasks from")
	flag.StringVar(&file, "f", defaultFile, "Markdown file to read tasks from (shorthand)")
	flag.BoolVar(&list, "list", false, "list available tasks")
	flag.BoolVar(&list, "l", false, "list available tasks (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: made [-f file] [task]\n\n")
		fmt.Fprintf(os.Stderr, "Run a task defined as an H3 heading + code block in a Markdown file.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

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

	if err := runner.Run(ctx, task); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "made: %v\n", err)
		return 1
	}
	return 0
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
