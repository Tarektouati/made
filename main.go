// Command made is a Markdown-driven task runner.
//
// Tasks are H3 headings in a Markdown file (the nearest Madefile.md by default), each
// followed by a fenced code block. `made <task> [args...]` runs that block with args
// as its positional parameters; `made` with no task lists them. `made init` writes a
// starter Madefile.md in the current directory, and `made completion <shell>` prints a
// bash, zsh or fish completion script.
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
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/Tarektouati/made/internal/completion"
	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
)

// madefileNames are the task file names discovered without -f, highest priority first.
// They are matched exactly against directory entries, so case-insensitive filesystems
// behave like case-sensitive ones.
var madefileNames = []string{"Madefile.md", "madefile.md", "MADEFILE.md"}

// madefileTemplate is what `made init` writes: a short header and one example task,
// valid as is.
const madefileTemplate = "# Tasks\n\n" +
	"Tasks for this project. Run `made` to list them,\n" +
	"or `made <task>` to run one.\n\n" +
	"### hello\n" +
	"Prints a friendly greeting.\n" +
	"```bash\n" +
	"echo hello\n" +
	"```\n"

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
	var listNames bool
	fs := flag.NewFlagSet("made", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&file, "file", "", "Markdown file to read tasks from (default: nearest Madefile.md, searched up to the repository root, or the filesystem root outside a repository)")
	fs.StringVar(&file, "f", "", "shorthand for -file")
	fs.BoolVar(&noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	fs.BoolVar(&list, "list", false, "list available tasks")
	fs.BoolVar(&list, "l", false, "list available tasks (shorthand)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "V", false, "print version and exit (shorthand)")
	// list-names is for the completion scripts and stays out of the usage text.
	fs.BoolVar(&listNames, "list-names", false, "print task names and descriptions, tab-separated, one per line")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: made [-f file] [--no-chdir] [task [--] [args...]]\n")
		fmt.Fprintf(stderr, "       made init [--force]\n")
		fmt.Fprintf(stderr, "       made completion <%s>\n\n", strings.Join(completion.Shells, "|"))
		fmt.Fprintf(stderr, "Run a task defined as an H3 heading + code block in a Markdown file.\n")
		fmt.Fprintf(stderr, "`made init` creates a starter Madefile.md in the current directory;\n")
		fmt.Fprintf(stderr, "--force overwrites an existing one.\n")
		fmt.Fprintf(stderr, "`made completion <shell>` prints a shell completion script.\n\n")
		printVisibleDefaults(fs, stderr, "list-names")
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

	// init is reserved: it scaffolds even when a task named "init" exists, and needs no
	// task file, like --version.
	if fs.NArg() > 0 && fs.Arg(0) == "init" {
		if file != "" {
			fmt.Fprintf(stderr, "made: init does not take -f; it creates %s in the current directory\n", madefileNames[0])
			return 2
		}
		var force bool
		ifs := flag.NewFlagSet("made init", flag.ContinueOnError)
		ifs.SetOutput(stderr)
		ifs.BoolVar(&force, "force", false, "overwrite an existing Madefile in the current directory")
		ifs.Usage = func() {
			fmt.Fprintf(stderr, "Usage: made init [--force]\n\n")
			fmt.Fprintf(stderr, "Create a starter %s in the current directory.\n\n", madefileNames[0])
			ifs.PrintDefaults()
		}
		if err := ifs.Parse(fs.Args()[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if ifs.NArg() > 0 {
			fmt.Fprintln(stderr, "made: init takes no arguments")
			return 2
		}
		return initMadefile(stdout, stderr, force)
	}

	// completion is reserved like init: it prints a script and needs no task file.
	if fs.NArg() > 0 && fs.Arg(0) == "completion" {
		if file != "" {
			fmt.Fprintln(stderr, "made: completion does not take -f")
			return 2
		}
		var script []byte
		ok := fs.NArg() == 2
		if ok {
			script, ok = completion.Script(fs.Arg(1))
		}
		if !ok {
			fmt.Fprintf(stderr, "made: completion takes one of: %s\n", strings.Join(completion.Shells, ", "))
			return 2
		}
		_, _ = stdout.Write(script)
		return 0
	}

	res, err := resolveFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "made: %v\n", err)
		return 1
	}
	if len(res.others) > 0 && !listNames {
		fmt.Fprintf(stderr, "made: using %s (also found %s)\n", res.path, strings.Join(res.others, ", "))
	}
	file = res.path

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

	if listNames {
		printNames(stdout, tasks)
		return 0
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

	if res.fromParent && len(res.others) == 0 { // the duplicate note already named it
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

// initMadefile writes madefileTemplate to Madefile.md in the current directory and
// returns the exit code. Existing files are matched against madefileNames exactly, and
// only regular files count, like findMadefile; parent directories are not searched, so a nested Madefile can be
// created on purpose. When one already exists, it refuses (exit 1) unless force is
// set; with force it overwrites the highest-priority existing file in place, keeping
// its name, and leaves any other Madefiles there untouched.
func initMadefile(stdout, stderr io.Writer, force bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot determine current directory: %v\n", err)
		return 1
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %v\n", cwd, err)
		return 1
	}
	var present []string // in madefileNames priority order
	for _, n := range madefileNames {
		if slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == n }) && isRegularFile(n) {
			present = append(present, n)
		}
	}

	if len(present) == 0 {
		// O_EXCL never overwrites, even if a file appeared since the check or the
		// filesystem folds case.
		name := madefileNames[0]
		if err := writeTemplate(name, os.O_CREATE|os.O_EXCL); err != nil {
			fmt.Fprintf(stderr, "made: cannot create %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "made: created %s\n", name)
		return 0
	}

	target := present[0]
	if !force {
		fmt.Fprintf(stderr, "made: %s already exists in %s (use made init --force to overwrite it)\n", target, cwd)
		return 1
	}
	if len(present) > 1 {
		fmt.Fprintf(stderr, "made: also found %s; left unchanged\n", strings.Join(present[1:], ", "))
	}
	// No O_CREATE: overwrite the file that is there, never create one under a
	// different case.
	if err := writeTemplate(target, os.O_TRUNC); err != nil {
		fmt.Fprintf(stderr, "made: cannot write %s: %v\n", target, err)
		return 1
	}
	fmt.Fprintf(stdout, "made: overwrote %s\n", target)
	return 0
}

// writeTemplate opens name for writing with the extra open flags and writes
// madefileTemplate to it. New files get mode 0644.
func writeTemplate(name string, flags int) error {
	f, err := os.OpenFile(name, os.O_WRONLY|flags, 0o644)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, madefileTemplate)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// resolved is the task file chosen by resolveFile.
type resolved struct {
	path       string   // path to read; a bare name when found in the current directory
	fromParent bool     // discovered in a parent of the current directory
	others     []string // other Madefile names found in the same directory, by priority
}

// resolveFile returns the task file to read. A non-empty file (given with -f) is used
// as is. Otherwise the nearest Madefile in the current directory or one of its parents
// is used (see findMadefile); if there is none, resolveFile returns an error.
func resolveFile(file string) (resolved, error) {
	if file != "" {
		return resolved{path: file}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return resolved{}, fmt.Errorf("cannot determine current directory: %w", err)
	}
	dir, name, others, ok := findMadefile(cwd)
	if !ok {
		return resolved{}, fmt.Errorf("no %s found in %s or its parents (use -f to choose a file, or made init to create one)", madefileNames[0], cwd)
	}
	res := resolved{path: name} // keep the short name when it's right here
	if dir != cwd {
		res.path = filepath.Join(dir, name)
		res.fromParent = true
	}
	res.others = others
	return res, nil
}

// findMadefile walks from start upward and returns the first directory holding a
// regular file named exactly like one of madefileNames, with the highest-priority
// name found and any other matching names in that directory. The walk stops after
// the first directory containing a .git entry (a directory, or a file for worktrees
// and submodules), otherwise at the filesystem root. A directory that cannot be
// read counts as having no Madefile.
func findMadefile(start string) (dir, name string, others []string, ok bool) {
	for dir = start; ; {
		entries, err := os.ReadDir(dir)
		boundary := false
		if err == nil {
			present := map[string]bool{}
			for _, e := range entries {
				present[e.Name()] = true
			}
			boundary = present[".git"]
			for _, n := range madefileNames {
				if !present[n] || !isRegularFile(filepath.Join(dir, n)) {
					continue
				}
				if name == "" {
					name = n
				} else {
					others = append(others, n)
				}
			}
			if name != "" {
				return dir, name, others, true
			}
		}
		parent := filepath.Dir(dir)
		if boundary || parent == dir {
			return "", "", nil, false
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

// printNames writes one task per line as "name<TAB>description" for the completion
// scripts, with no header. The description is flattened to one line.
func printNames(stdout io.Writer, tasks []parser.Task) {
	for _, t := range tasks {
		fmt.Fprintf(stdout, "%s\t%s\n", t.Name, strings.ReplaceAll(t.Description, "\n", " "))
	}
}

// printVisibleDefaults is fs.PrintDefaults without the flags named in hidden.
func printVisibleDefaults(fs *flag.FlagSet, w io.Writer, hidden ...string) {
	visible := flag.NewFlagSet(fs.Name(), flag.ContinueOnError)
	visible.SetOutput(w)
	fs.VisitAll(func(f *flag.Flag) {
		if slices.Contains(hidden, f.Name) {
			return
		}
		visible.Var(f.Value, f.Name, f.Usage)
		visible.Lookup(f.Name).DefValue = f.DefValue // f.Value may hold a parsed value
	})
	visible.PrintDefaults()
}
