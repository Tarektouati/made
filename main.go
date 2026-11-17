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
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"unicode"

	"github.com/Tarektouati/made/internal/completion"
	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
	"golang.org/x/text/width"
)

// madefileNames are the task file names discovered without -f, highest priority first.
// They are matched exactly against directory entries, so case-insensitive filesystems
// behave like case-sensitive ones.
var madefileNames = []string{"Madefile.md", "madefile.md", "MADEFILE.md"}

// maxMadefileBytes is the largest task file made will read (discovery or -f).
const maxMadefileBytes = 10 << 20 // 10 MiB

// madefileTemplate is the body `made init` writes: a short header and one example
// task, valid as is. initTemplate prepends the version note.
const madefileTemplate = "# Tasks\n\n" +
	"Tasks for this project. Run `made` to list them,\n" +
	"or `made <task>` to run one.\n\n" +
	"### hello\n" +
	"Prints a friendly greeting.\n" +
	"```bash\n" +
	"echo hello\n" +
	"```\n"

// version is set at release time via -ldflags "-X main.version=<version>", with or
// without the leading "v".
var version = ""

// resolveVersion reports the build version: the ldflags-injected value if present,
// otherwise the module version from the build info (e.g. a `go install …@v1.2.3`),
// falling back to "dev" for local builds.
func resolveVersion() string {
	if version != "" {
		return normalizeVersion(version)
	}
	if info, ok := debug.ReadBuildInfo(); ok &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		return normalizeVersion(info.Main.Version)
	}
	return "dev"
}

// normalizeVersion gives ldflags and build-info versions one format: a leading "v"
// before a numeric version (goreleaser strips it from .Version; Go module versions keep it).
func normalizeVersion(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// initTemplate returns the file `made init` writes for made version v: the template
// headed by an HTML comment recording v, so a Madefile says which made it was written
// for. The comment is hidden when rendered and the parser skips it; the blank line
// after it ends the HTML block before "# Tasks". Dev builds have no meaningful
// version, so they get the bare template.
func initTemplate(v string) string {
	if v == "dev" {
		return madefileTemplate
	}
	return "<!-- made " + v + " -->\n\n" + madefileTemplate
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
	fs.StringVar(&file, "f", "", "shorthand for --file")
	fs.BoolVar(&noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	fs.BoolVar(&list, "list", false, "list available tasks")
	fs.BoolVar(&list, "l", false, "shorthand for --list")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "V", false, "shorthand for --version")
	// list-names is for the completion scripts and stays out of the usage text.
	fs.BoolVar(&listNames, "list-names", false, "print task names and descriptions, tab-separated, one per line")
	printUsage := func(w io.Writer) {
		fmt.Fprintf(w, "Usage: made [-f file] [--no-chdir] [-l] [-V] [task [--] [args...]]\n")
		fmt.Fprintf(w, "       made init [--force]\n")
		fmt.Fprintf(w, "       made completion <%s>\n\n", strings.Join(completion.Shells, "|"))
		fmt.Fprintf(w, "Run a task defined as an H3 heading + code block in a Markdown file.\n")
		fmt.Fprintf(w, "`made init` creates a starter Madefile.md in the current directory;\n")
		fmt.Fprintf(w, "--force overwrites an existing one.\n")
		fmt.Fprintf(w, "`made completion <shell>` prints a shell completion script.\n\n")
		printOptions(w, fs, mainOptions)
	}
	// fs still reports parse errors on stderr; the usage is printed below so that help
	// can go to stdout.
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		// Keep the exit codes flag.ExitOnError used: 0 for -h/-help, 2 for a bad flag.
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		printUsage(stderr)
		return 2
	}

	if showVersion {
		fmt.Fprintln(stdout, "made", resolveVersion())
		return 0
	}

	// An explicit empty -f (often an unset variable) must not fall back to discovery.
	fileSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "f" || f.Name == "file" {
			fileSet = true
		}
	})
	if fileSet && file == "" {
		fmt.Fprintln(stderr, "made: -f requires a file path")
		return 2
	}

	// init is reserved: it scaffolds even when a task named "init" exists, and needs no
	// task file, like --version.
	if fs.NArg() > 0 && fs.Arg(0) == "init" {
		if file != "" {
			fmt.Fprintf(stderr, "made: init does not take -f; it creates %s in the current directory\n", madefileNames[0])
			return 2
		}
		if f := firstSetFlag(fs, "f", "file"); f != "" {
			fmt.Fprintf(stderr, "made: init does not take %s\n", f)
			return 2
		}
		var force bool
		ifs := flag.NewFlagSet("made init", flag.ContinueOnError)
		ifs.SetOutput(stderr)
		ifs.BoolVar(&force, "force", false, "overwrite an existing Madefile in the current directory")
		printInitUsage := func(w io.Writer) {
			fmt.Fprintf(w, "Usage: made init [--force]\n\n")
			fmt.Fprintf(w, "Create a starter %s in the current directory.\n\n", madefileNames[0])
			printOptions(w, ifs, initOptions)
		}
		ifs.Usage = func() {}
		if err := ifs.Parse(fs.Args()[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printInitUsage(stdout)
				return 0
			}
			printInitUsage(stderr)
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
		if f := firstSetFlag(fs, "f", "file"); f != "" {
			fmt.Fprintf(stderr, "made: completion does not take %s\n", f)
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
	for _, s := range res.skipped {
		fmt.Fprintf(stderr, "made: skipping %s: %v\n", s.path, s.err)
	}
	if err != nil {
		fmt.Fprintf(stderr, "made: %v\n", err)
		return 1
	}
	if len(res.others) > 0 && !listNames {
		fmt.Fprintf(stderr, "made: using %s (also found %s)\n", res.path, strings.Join(res.others, ", "))
	}
	file = res.path

	source, err := readMadefile(file)
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

	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT}
	// Subscribing un-ignores a signal, so leave SIGHUP alone when it is already
	// ignored (nohup); otherwise logging out would tear down the task.
	if !signal.Ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}
	ctx, force, stop, received := notifySignal(sigs...)
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

	runErr := runner.Run(ctx, force, task, dir, taskArgs)
	// Interrupted like a shell would be: 128+n for the signal made received, whatever
	// status the torn-down task ended with.
	if sig, ok := received().(syscall.Signal); ok {
		return 128 + int(sig)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return taskExitCode(exitErr)
		}
		fmt.Fprintf(stderr, "made: %v\n", runErr)
		code := 1
		var interpErr *runner.InterpreterError
		if errors.As(runErr, &interpErr) {
			code = interpErr.ExitCode()
		}
		return code
	}
	return 0
}

// signalReceived is the cancellation cause of a notifySignal context.
type signalReceived struct{ sig os.Signal }

func (s signalReceived) Error() string { return s.sig.String() + " signal received" }

// Signal reports the signal to runner.Run, which tears a task down differently when the
// terminal itself sent it.
func (s signalReceived) Signal() os.Signal { return s.sig }

// notifySignal is signal.NotifyContext that also remembers which signal arrived and
// lets a second one force teardown; see watchSignals. stop also releases the signal
// handler.
func notifySignal(sigs ...os.Signal) (ctx context.Context, force <-chan struct{}, stop func(), received func() os.Signal) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, sigs...)
	ctx, force, unwatch, received := watchSignals(ch)
	return ctx, force, func() { signal.Stop(ch); unwatch() }, received
}

// watchSignals cancels ctx on the first value from ch, and received reports that
// signal (nil if none came). force is closed by a second value, so a second Ctrl-C can
// skip the task's grace period. stop releases the watching goroutine and ctx; it is
// safe to call more than once.
func watchSignals(ch <-chan os.Signal) (ctx context.Context, force <-chan struct{}, stop func(), received func() os.Signal) {
	ctx, cancel := context.WithCancelCause(context.Background())
	forceCh := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		select {
		case s := <-ch:
			cancel(signalReceived{s})
		case <-quit:
			return
		}
		select {
		case <-ch:
			close(forceCh)
		case <-quit:
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(quit)
			cancel(nil)
		})
	}
	received = func() os.Signal {
		var s signalReceived
		if errors.As(context.Cause(ctx), &s) {
			return s.sig
		}
		return nil
	}
	return ctx, forceCh, stop, received
}

// initMadefile writes the init template (see initTemplate) to Madefile.md in the current directory and
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
	if isSymlink(target) {
		fmt.Fprintf(stderr, "made: %s is a symlink (refusing to overwrite)\n", target)
		return 1
	}
	// No O_CREATE: overwrite the file that is there, never create one under a
	// different case. O_NOFOLLOW (on unix) refuses the path if it becomes
	// a symlink between the check above and open.
	if err := writeTemplate(target, os.O_TRUNC|initForceNoFollow); err != nil {
		if isSymlink(target) {
			fmt.Fprintf(stderr, "made: %s is a symlink (refusing to overwrite)\n", target)
			return 1
		}
		fmt.Fprintf(stderr, "made: cannot write %s: %v\n", target, err)
		return 1
	}
	fmt.Fprintf(stdout, "made: overwrote %s\n", target)
	return 0
}

// writeTemplate opens name for writing with the extra open flags and writes
// the init template for this build's version to it. New files get mode 0644.
func writeTemplate(name string, flags int) error {
	f, err := os.OpenFile(name, os.O_WRONLY|flags, 0o644)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, initTemplate(resolveVersion()))
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
	skipped    []skippedMadefile
}

// skippedMadefile is a Madefile name present in a directory that could not be used.
type skippedMadefile struct {
	path string
	err  error
}

// resolveFile returns the task file to read. A non-empty file (given with -f) is used
// as is. Otherwise the nearest Madefile in the current directory or one of its parents
// is used (see findMadefile); if there is none, resolveFile returns an error. The walk
// starts from the physical current directory, with symlinks resolved. Unusable
// candidates are reported in skipped, also alongside the not-found error.
func resolveFile(file string) (resolved, error) {
	if file != "" {
		return resolved{path: file}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return resolved{}, fmt.Errorf("cannot determine current directory: %w", err)
	}
	start := cwd
	if physical, err := filepath.EvalSymlinks(cwd); err == nil {
		start = physical
	}
	dir, name, others, skipped, ok := findMadefile(start)
	if !ok {
		return resolved{skipped: skipped}, fmt.Errorf("no %s found in %s or its parents (use -f to choose a file, or made init to create one)", madefileNames[0], start)
	}
	res := resolved{path: name, others: others, skipped: skipped} // keep the short name when it's right here
	if dir != start {
		res.path = filepath.Join(dir, name)
		res.fromParent = true
	}
	return res, nil
}

// findMadefile walks from start upward and returns the first directory holding a
// regular file named exactly like one of madefileNames, with the highest-priority
// name found and any other matching names in that directory. The walk stops after
// the first directory containing a .git entry (a directory, or a file for worktrees
// and submodules), otherwise at the filesystem root. Names that are present but
// unusable (broken or looping symlinks, non-regular files, permission errors), and
// directories that exist but cannot be listed, are returned in skipped, nearest
// first, and the walk goes on.
func findMadefile(start string) (dir, name string, others []string, skipped []skippedMadefile, ok bool) {
	for dir = start; ; {
		entries, err := os.ReadDir(dir)
		boundary := false
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			skipped = append(skipped, skippedMadefile{dir, pathErrReason(err)})
		}
		if err == nil {
			present := map[string]bool{}
			for _, e := range entries {
				present[e.Name()] = true
			}
			boundary = present[".git"]
			for _, n := range madefileNames {
				if !present[n] {
					continue
				}
				if err := checkMadefile(filepath.Join(dir, n)); err != nil {
					skipped = append(skipped, skippedMadefile{filepath.Join(dir, n), err})
					continue
				}
				if name == "" {
					name = n
				} else {
					others = append(others, n)
				}
			}
			if name != "" {
				return dir, name, others, skipped, true
			}
		}
		parent := filepath.Dir(dir)
		if boundary || parent == dir {
			return "", "", nil, skipped, false
		}
		dir = parent
	}
}

// readMadefile reads a task file after checking it is a regular file within
// maxMadefileBytes.
func readMadefile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxMadefileBytes {
		return nil, fmt.Errorf("%s exceeds maximum size (%d bytes)", path, maxMadefileBytes)
	}
	return os.ReadFile(path)
}

// isRegularFile reports whether path exists and is a regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// errNotRegular is checkMadefile's reason for an existing path that is not a regular file.
var errNotRegular = errors.New("not a regular file")

// checkMadefile returns nil if path is a regular file (following symlinks), otherwise
// the reason it cannot be used, without the path.
func checkMadefile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if target, lerr := os.Readlink(path); lerr == nil {
				return fmt.Errorf("broken symlink to %s", target)
			}
		}
		return pathErrReason(err)
	}
	if !info.Mode().IsRegular() {
		return errNotRegular
	}
	return nil
}

// pathErrReason returns the error inside a *fs.PathError, dropping the operation
// and path, or err itself otherwise.
func pathErrReason(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// isSymlink reports whether path exists and is a symlink (does not follow the link).
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func find(tasks []parser.Task, name string) (parser.Task, bool) {
	for _, t := range tasks {
		if t.Name == name {
			return t, true
		}
	}
	return parser.Task{}, false
}

// sanitizeDisplay removes C0/C1 control characters and DEL from strings printed to the
// terminal (task names and descriptions). Task lookup still uses the parsed values.
func sanitizeDisplay(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// displayWidth approximates the number of terminal cells s occupies: East Asian wide
// and fullwidth runes take two, combining marks, format characters (such as ZWJ) and
// variation selectors take none, except that the emoji selector U+FE0F widens a
// preceding one-cell rune to two. Multi-rune emoji sequences are not clustered.
func displayWidth(s string) int {
	n, last := 0, 0
	for _, r := range s {
		switch k := width.LookupRune(r).Kind(); {
		case r == 0xfe0f:
			if last == 1 {
				n++
				last = 2
			}
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf), r >= 0xfe00 && r <= 0xfe0e:
		case k == width.EastAsianWide, k == width.EastAsianFullwidth:
			n += 2
			last = 2
		default:
			n++
			last = 1
		}
	}
	return n
}

func printTasks(stdout, stderr io.Writer, file string, tasks []parser.Task) {
	if len(tasks) == 0 {
		fmt.Fprintf(stderr, "made: no tasks found in %s\n", file)
		return
	}
	fmt.Fprintf(stdout, "Tasks in %s:\n", file)
	names := make([]string, len(tasks))
	widths := make([]int, len(tasks))
	maxW := 0
	for i, t := range tasks {
		names[i] = sanitizeDisplay(t.Name)
		widths[i] = displayWidth(names[i])
		maxW = max(maxW, widths[i])
	}
	for i, t := range tasks {
		desc := sanitizeDisplay(strings.ReplaceAll(t.Description, "\n", " "))
		fmt.Fprintf(stdout, "  %s%s%s\n", names[i], strings.Repeat(" ", maxW-widths[i]+2), desc)
	}
}

// printNames writes one task per line as "name<TAB>description" for the completion
// scripts, with no header. The description is flattened to one line.
func printNames(stdout io.Writer, tasks []parser.Task) {
	for _, t := range tasks {
		name := sanitizeDisplay(t.Name)
		desc := sanitizeDisplay(strings.ReplaceAll(t.Description, "\n", " "))
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
	{short: "l", long: "list"},
	{short: "V", long: "version"},
	{short: "h", long: "help"},
}

// initOptions are the flags shown by made init --help.
var initOptions = []usageOption{
	{long: "force"},
	{short: "h", long: "help"},
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
