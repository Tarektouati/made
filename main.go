// Command made is a Markdown-driven task runner.
//
// Tasks are H3 headings in a Markdown file (the nearest Madefile.md by default), each
// followed by a fenced code block. `made <task> [args...]` runs that block with args
// as its positional parameters; `made` with no task lists them. `made init` writes a
// starter Madefile.md in the current directory, `made completion <shell>` prints a
// bash, zsh or fish completion script, and `made self-update` replaces made with the
// latest release.
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/Tarektouati/made/internal/completion"
	"github.com/Tarektouati/made/internal/lint"
	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
	"github.com/Tarektouati/made/internal/selfupdate"
	"github.com/Tarektouati/made/internal/task"
	"github.com/Tarektouati/made/internal/updatecheck"
	"golang.org/x/term"
	"golang.org/x/text/width"
)

// madefileNames are the task file names discovered without -f, highest priority first.
// They are matched exactly against directory entries, so case-insensitive filesystems
// behave like case-sensitive ones.
var madefileNames = []string{"Madefile.md", "madefile.md", "MADEFILE.md"}

// maxMadefileBytes is the largest task file made will read (discovery or -f).
const maxMadefileBytes = 10 << 20 // 10 MiB

// parseTimeout bounds how long made waits for the Markdown parser. goldmark's cost
// grows faster than linearly on some input (unclosed links, mixed emphasis), so a
// small hostile Madefile could otherwise stall made -l or tab completion. Real
// Madefiles parse in milliseconds. It is below the completion scripts' 2s timeout,
// so made reports the problem itself. Tests shorten it.
var parseTimeout = 1500 * time.Millisecond

// parseTimeoutError reports that parsing took longer than parseTimeout.
type parseTimeoutError struct{ limit time.Duration }

func (e parseTimeoutError) Error() string {
	return fmt.Sprintf("parsing took longer than %v", e.limit)
}

// withinParseTimeout runs parse and returns its result, or a parseTimeoutError if it
// does not finish within parseTimeout. goldmark cannot be cancelled, so a parse that
// times out keeps running in its goroutine until made exits.
func withinParseTimeout[T any](parse func() T) (T, error) {
	done := make(chan T, 1)
	go func() { done <- parse() }()
	timer := time.NewTimer(parseTimeout)
	defer timer.Stop()
	select {
	case v := <-done:
		return v, nil
	case <-timer.C:
		var zero T
		return zero, parseTimeoutError{parseTimeout}
	}
}

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

// selfUpdateBaseURL replaces github.com for made self-update; tests point it at a
// local server.
var selfUpdateBaseURL = ""

// detectInstall describes the running binary for made self-update; tests replace it.
var detectInstall = selfupdate.Current

// updateCacheDir returns the directory of the new-version notice's cache; tests
// replace it.
var updateCacheDir = func() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "made"), nil
}

// updateTimeout bounds the background release check; tests lengthen it.
var updateTimeout = time.Second

// isTerminal reports whether w is a terminal; tests replace it.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// startUpdateCheck starts the new-version check (see package updatecheck), or returns
// nil when the user opted out, in CI, when stdout or stderr is not a terminal, and for
// builds that are not a release.
func startUpdateCheck(stdout, stderr io.Writer) *updatecheck.Check {
	if os.Getenv("MADE_NO_UPDATE_CHECK") != "" || os.Getenv("NO_UPDATE_NOTIFIER") != "" {
		return nil
	}
	if _, ci := os.LookupEnv("CI"); ci {
		return nil
	}
	if !isTerminal(stdout) || !isTerminal(stderr) {
		return nil
	}
	v := resolveVersion()
	if !selfupdate.IsRelease(v) {
		return nil
	}
	dir, err := updateCacheDir()
	if err != nil {
		return nil
	}
	return updatecheck.Start(updatecheck.Config{
		Current:  v,
		Method:   selfupdate.Detect(detectInstall(v, version != "")),
		Client:   selfupdate.Client{BaseURL: selfUpdateBaseURL},
		CacheDir: dir,
		Timeout:  updateTimeout,
	})
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
	var check bool
	var strict bool
	var host bool
	fs := flag.NewFlagSet("made", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // parse errors are reported by flagError
	fs.StringVar(&file, "file", "", "Markdown file to read tasks from (default: nearest Madefile.md, searched up to the repository root, or the filesystem root outside a repository)")
	fs.StringVar(&file, "f", "", "shorthand for --file")
	fs.BoolVar(&noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	fs.BoolVar(&check, "check", false, "check the task file for problems instead of running a task")
	fs.BoolVar(&strict, "strict", false, "with --check, fail on warnings too")
	fs.BoolVar(&host, "host", false, "with --check, also check that each task's interpreter is on PATH")
	fs.BoolVar(&list, "list", false, "list available tasks")
	fs.BoolVar(&list, "l", false, "shorthand for --list")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "V", false, "shorthand for --version")
	// list-names is for the completion scripts and stays out of the usage text.
	fs.BoolVar(&listNames, "list-names", false, "print task names and descriptions, tab-separated, one per line")
	printUsage := func(w io.Writer) {
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
	// The usage is printed below so that help can go to stdout.
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		// Keep the exit codes flag.ExitOnError used: 0 for -h/-help, 2 for a bad flag.
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "made: %s (see made --help)\n", flagError(fs, args, err))
		return 2
	}

	if strict && !check {
		fmt.Fprintln(stderr, "made: --strict requires --check")
		return 2
	}
	if host && !check {
		fmt.Fprintln(stderr, "made: --host requires --check")
		return 2
	}
	if check {
		if fs.NArg() > 0 {
			fmt.Fprintln(stderr, "made: --check takes no task name")
			return 2
		}
		if f := firstSetFlag(fs, "f", "file", "no-chdir", "check", "strict", "host"); f != "" {
			fmt.Fprintf(stderr, "made: --check does not take %s\n", f)
			return 2
		}
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
		ifs.SetOutput(io.Discard)
		ifs.BoolVar(&force, "force", false, "overwrite an existing Madefile in the current directory")
		printInitUsage := func(w io.Writer) {
			fmt.Fprintf(w, "Usage: made init [--force]\n\n")
			fmt.Fprintf(w, "Create a starter %s in the current directory.\n\n", madefileNames[0])
			printOptions(w, ifs, initOptions)
		}
		ifs.Usage = func() {}
		initArgs := fs.Args()[1:]
		if err := ifs.Parse(initArgs); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printInitUsage(stdout)
				return 0
			}
			fmt.Fprintf(stderr, "made: %s (see made init --help)\n", flagError(ifs, initArgs, err))
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

	// self-update is reserved like init: it replaces the made binary and needs no task file.
	if fs.NArg() > 0 && fs.Arg(0) == "self-update" {
		if file != "" {
			fmt.Fprintln(stderr, "made: self-update does not take -f")
			return 2
		}
		if f := firstSetFlag(fs, "f", "file"); f != "" {
			fmt.Fprintf(stderr, "made: self-update does not take %s\n", f)
			return 2
		}
		return selfUpdate(fs.Args()[1:], stdout, stderr)
	}

	res, err := resolveFile(file)
	for _, s := range res.skipped {
		fmt.Fprintf(stderr, "made: skipping %s: %s\n", escapeDisplay(s.path), escapeErr(s.err))
	}
	if err != nil {
		fmt.Fprintf(stderr, "made: %s\n", escapeErr(err))
		return 1
	}
	if len(res.others) > 0 && !listNames {
		fmt.Fprintf(stderr, "made: using %s (also found %s)\n", escapeDisplay(res.path), strings.Join(res.others, ", "))
	}
	file = res.path

	if check {
		return runCheck(stdout, stderr, file, res.explicit, strict, lint.Options{Host: host})
	}

	// Tab completion runs --list-names with the typed -f value, so a pipe there must
	// fail at once rather than block or read the terminal.
	source, regular, err := readMadefile(file, res.explicit && !listNames)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", displayPath(file), escapeErr(pathOnlyOnce(file, err)))
		return 1
	}

	type parsed struct {
		tasks []task.Task
		err   error
	}
	out, err := withinParseTimeout(func() parsed {
		tasks, perr := parser.Parse(source)
		return parsed{tasks, perr}
	})
	if err == nil {
		err = out.err
	}
	tasks := out.tasks
	if nest := (*parser.NestingError)(nil); errors.As(err, &nest) {
		fmt.Fprintf(stderr, "made: cannot parse %s: line %d: %s\n", escapeDisplay(file), nest.Line, escapeErr(err))
		fmt.Fprintf(stderr, "made: hint: flatten the lists or blockquotes on that line or indent it less (lines in code blocks count too); run made --check -f %s\n", shellQuotePath(file))
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot parse %s: %s\n", escapeDisplay(file), escapeErr(err))
		return 1
	}

	if listNames {
		printNames(stdout, tasks)
		return 0
	}

	// Every command that must stay quiet (completion, scripts, init, --check, -V,
	// self-update) has returned by now. The notice comes from the cache and is printed
	// after the task, so it is never mixed with the task's output or waited for.
	notice := startUpdateCheck(stdout, stderr)
	defer func() {
		if line := notice.Notice(); line != "" {
			fmt.Fprintln(stderr, line)
		}
	}()

	runTask := !list && fs.NArg() > 0
	// Before runnableTasks, so its warnings cite lines of a file already named.
	if runTask && res.fromParent && len(res.others) == 0 { // the duplicate note already named it
		fmt.Fprintf(stderr, "made: using %s\n", escapeDisplay(file))
	}
	tasks = runnableTasks(stderr, tasks)

	if !runTask {
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

	sigs := []os.Signal{syscall.SIGTERM}
	// Subscribing un-ignores a signal, also for the task, so leave one alone when it is
	// already ignored: SIGHUP under nohup (otherwise logging out would tear down the
	// task), SIGINT and SIGQUIT in a background job of a non-interactive shell.
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			sigs = append(sigs, sig)
		}
	}
	// The Go runtime replaces an inherited SIG_IGN for SIGQUIT before main runs, so it
	// cannot be seen; shells ignore it together with SIGINT, so follow SIGINT.
	if signal.Ignored(os.Interrupt) {
		signal.Ignore(syscall.SIGQUIT)
	} else {
		sigs = append(sigs, syscall.SIGQUIT)
	}
	ctx, force, stop, received := notifySignal(sigs...)
	defer stop()

	// A pipe or device has no directory of its own (it would be /dev or /dev/fd), so
	// its tasks run in the current directory.
	dir := ""
	if !noChdir && regular {
		abs, err := filepath.Abs(file)
		if err != nil {
			fmt.Fprintf(stderr, "made: %s\n", escapeErr(err))
			return 1
		}
		dir = filepath.Dir(abs)
	}

	// Everything after the task name goes to the script. Flag parsing already stopped
	// at the task name, so a leading "--" is optional; drop one if present.
	taskArgs := fs.Args()[1:]
	if len(taskArgs) > 0 && taskArgs[0] == "--" {
		taskArgs = taskArgs[1:]
	}

	runErr := runner.Run(ctx, force, task, dir, taskArgs)
	// A task that handled Ctrl-C or Ctrl-\ itself and exited reports its own status, as
	// when run from a shell. Otherwise made was interrupted like a shell would be: 128+n
	// for the signal it received, whatever status the torn-down task ended with.
	var handled *runner.SignalHandledError
	if errors.As(runErr, &handled) {
		runErr = handled.Err
	} else if sig, ok := received().(syscall.Signal); ok {
		return 128 + int(sig)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return taskExitCode(exitErr)
		}
		fmt.Fprintf(stderr, "made: %s\n", escapeErr(runErr))
		code := 1
		var interpErr *runner.InterpreterError
		var argErr *runner.ArgLimitError
		if errors.As(runErr, &interpErr) {
			code = interpErr.ExitCode()
		} else if errors.As(runErr, &argErr) {
			code = argErr.ExitCode()
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
		fmt.Fprintf(stderr, "made: cannot determine current directory: %s\n", escapeErr(err))
		return 1
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", escapeDisplay(cwd), escapeErr(err))
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
			if kind, ok := notRegular(name); ok {
				if kind != "" {
					kind = " (" + kind + ")"
				}
				fmt.Fprintf(stderr, "made: cannot create %s: it exists and is not a regular file%s; remove it first\n", name, kind)
				return 1
			}
			fmt.Fprintf(stderr, "made: cannot create %s: %s\n", name, escapeErr(err))
			return 1
		}
		fmt.Fprintf(stdout, "made: created %s\n", name)
		return 0
	}

	target := present[0]
	if !force {
		fmt.Fprintf(stderr, "made: %s already exists in %s (use made init --force to overwrite it)\n", target, escapeDisplay(cwd))
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
		fmt.Fprintf(stderr, "made: cannot write %s: %s\n", target, escapeErr(err))
		return 1
	}
	fmt.Fprintf(stdout, "made: overwrote %s\n", target)
	return 0
}

// selfUpdate runs made self-update with args, the words after "self-update", and
// returns the exit code: 0 when made was updated or already current, or for a dry
// run; 2 for a usage error; otherwise one of the selfupdate.Exit codes. The version
// and --dry-run may come in either order.
func selfUpdate(args []string, stdout, stderr io.Writer) int {
	var dryRun bool
	sfs := flag.NewFlagSet("made self-update", flag.ContinueOnError)
	sfs.SetOutput(io.Discard)
	sfs.BoolVar(&dryRun, "dry-run", false, "print what would happen without changing anything")
	sfs.Usage = func() {}
	var versions []string
	for rest := args; ; {
		if err := sfs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(stdout, "Usage: made self-update [vX.Y.Z] [--dry-run]\n\n")
				fmt.Fprintf(stdout, "Replace made with the latest release, or with release vX.Y.Z (which may be older).\n")
				fmt.Fprintf(stdout, "Installs from Homebrew or go install are left to those tools.\n\n")
				printOptions(stdout, sfs, selfUpdateOptions)
				return 0
			}
			fmt.Fprintf(stderr, "made: %s (see made self-update --help)\n", flagError(sfs, rest, err))
			return 2
		}
		if sfs.NArg() == 0 {
			break
		}
		// Parse stops at the first word that is not a flag, or after "--", which ends
		// the flags for good.
		if i := len(rest) - sfs.NArg() - 1; i >= 0 && rest[i] == "--" {
			versions = append(versions, sfs.Args()...)
			break
		}
		versions = append(versions, sfs.Arg(0))
		rest = sfs.Args()[1:]
	}
	if len(versions) > 1 {
		fmt.Fprintln(stderr, "made: self-update takes at most one version")
		return 2
	}
	target := ""
	if len(versions) == 1 && versions[0] != "latest" {
		// A pre-release would be installed as a development build, which made
		// self-update then refuses to replace.
		var err error
		if target, err = selfupdate.NormalizeTag(versions[0]); err != nil || !selfupdate.IsRelease(target) {
			fmt.Fprintf(stderr, "made: invalid version %q: give a release such as v1.2.3\n", versions[0])
			return 2
		}
	}

	in := detectInstall(resolveVersion(), version != "")
	if m := selfupdate.Detect(in); m != selfupdate.Manual {
		fmt.Fprintf(stderr, "made: %s\n", escapeDisplay(selfupdate.Refusal(m, in)))
		return selfupdate.ExitNotSelfUpdatable
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	u := selfupdate.Updater{
		Client:  selfupdate.Client{BaseURL: selfUpdateBaseURL},
		Exe:     in.Exe,
		Current: in.Version,
		Target:  target,
		GOOS:    in.GOOS,
		GOARCH:  in.GOARCH,
		DryRun:  dryRun,
		Stdout:  stdout,
	}
	if err := u.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "made: %s\n", escapeErr(err))
		code := selfupdate.ExitFailure
		var ue *selfupdate.Error
		if errors.As(err, &ue) {
			code = ue.Code
			if ue.Hint != "" {
				fmt.Fprintf(stderr, "made: hint: %s\n", ue.Hint)
			}
		}
		return code
	}
	return 0
}

// notRegular reports whether path exists (without following a symlink) and does not
// lead to a regular file, with what it is when that is a directory or a symlink to
// nothing or to a directory, for display.
func notRegular(path string) (kind string, ok bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().IsRegular() {
		return "", false
	}
	if info.IsDir() {
		return "a directory", true
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", true
	}
	target, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if dest, lerr := os.Readlink(path); lerr == nil {
			return "a broken symlink to " + escapeDisplay(dest), true
		}
		return "", true
	case err != nil:
		return "", true
	case target.Mode().IsRegular():
		return "", false
	case target.IsDir():
		return "a symlink to a directory", true
	}
	return "", true
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
	explicit   bool     // given with -f rather than discovered
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
		return resolved{path: file, explicit: true}, nil
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
// unusable (broken or looping symlinks, non-regular files, names that cannot be
// looked up because a directory cannot be searched), and directories that exist
// but cannot be listed, are returned in skipped, nearest first, and the walk goes
// on. A regular file that cannot be read is still returned, so the caller fails on
// it rather than running a parent's Madefile.
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

// readMadefile reads a task file of at most maxMadefileBytes. A discovered file must
// be a regular file, checked before it is opened, because opening a FIFO blocks. An
// explicit (-f) file may be anything readable but a directory, such as a pipe or
// /dev/stdin; a FIFO with no writer then blocks, like cat. The size check is repeated
// while reading, because some files report a wrong size (0 for /proc files) or grow
// after the stat. regular reports whether the file read was a regular file.
func readMadefile(path string, explicit bool) (source []byte, regular bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if explicit && info.IsDir() {
		return nil, false, &fs.PathError{Op: "read", Path: path, Err: errIsDir}
	}
	if !explicit && !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().IsRegular() && info.Size() > maxMadefileBytes {
		return nil, false, fmt.Errorf("%s %w (%d bytes)", path, errTooLarge, maxMadefileBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if info, err = f.Stat(); err != nil {
		return nil, false, err
	}
	regular = info.Mode().IsRegular()
	if !explicit && !regular {
		return nil, false, fmt.Errorf("%s is not a regular file", path)
	}
	source, err = readLimited(f, path)
	return source, regular, err
}

// errIsDir is readMadefile's reason for an explicit path that is a directory.
var errIsDir = errors.New("is a directory")

// readLimited reads r to the end, or fails with errTooLarge once it passes
// maxMadefileBytes.
func readLimited(r io.Reader, path string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxMadefileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxMadefileBytes {
		return nil, fmt.Errorf("%s %w (%d bytes)", path, errTooLarge, maxMadefileBytes)
	}
	return b, nil
}

// errTooLarge is wrapped by readMadefile for a file over maxMadefileBytes.
var errTooLarge = errors.New("exceeds maximum size")

// runCheck lints the task file at path for made --check, prints the findings to
// stdout, and returns the exit code: 1 if there is an error (or, with strict, a
// warning), otherwise 0. A file that cannot be read is reported on stderr, exit 1.
// explicit is as for readMadefile; opts selects the optional checks.
func runCheck(stdout, stderr io.Writer, path string, explicit, strict bool, opts lint.Options) int {
	var diags []lint.Diagnostic
	source, _, err := readMadefile(path, explicit)
	switch {
	case errors.Is(err, errTooLarge):
		diags = []lint.Diagnostic{{Severity: lint.Error, Rule: "file-too-large",
			Msg: fmt.Sprintf("file exceeds the maximum size of %d bytes", maxMadefileBytes)}}
	case err != nil:
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", displayPath(path), escapeErr(pathOnlyOnce(path, err)))
		return 1
	default:
		diags, err = withinParseTimeout(func() []lint.Diagnostic { return lint.Check(source, opts) })
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
	path = escapeDisplay(path)
	for _, d := range diags {
		loc := path
		if d.Line > 0 {
			loc = fmt.Sprintf("%s:%d", path, d.Line)
		}
		fmt.Fprintf(w, "%s: %s: %s: %s\n", loc, d.Severity, d.Rule, sanitizeDisplay(d.Msg))
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

// find returns the task called name, normalized like the parser normalizes headings
// so a name typed as written in the Madefile matches.
func find(tasks []task.Task, name string) (task.Task, bool) {
	name = parser.NormalizeSpace(name)
	for _, t := range tasks {
		if t.Name == name {
			return t, true
		}
	}
	return task.Task{}, false
}

// runnableTasks drops the tasks that cannot be run by name, reporting each on
// stderr: one with an empty name, and one whose name an earlier task already has
// (find returns the first).
func runnableTasks(stderr io.Writer, tasks []task.Task) []task.Task {
	defined := map[string]int{} // task name -> line of its first definition
	kept := tasks[:0:0]
	for _, t := range tasks {
		if t.Name == "" {
			fmt.Fprintf(stderr, "made: task at line %d has an empty name; it is skipped\n", t.Line)
			continue
		}
		if first, ok := defined[t.Name]; ok {
			fmt.Fprintf(stderr, "made: task %q is defined again at line %d; only the first (line %d) runs\n", t.Name, t.Line, first)
			continue
		}
		defined[t.Name] = t.Line
		kept = append(kept, t)
	}
	return kept
}

// sanitizeDisplay removes C0/C1 control characters, DEL, line/paragraph separators
// and invisible formatting characters (bidi controls, zero-width space, tag
// characters outside the England, Scotland and Wales flag sequences; see
// parser.ReplaceInvisible) from task names, descriptions and lint messages printed to
// the terminal. Task lookup still uses the parsed values.
func sanitizeDisplay(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
	return parser.ReplaceInvisible(s, func(rune) string { return "" })
}

// escapeDisplay writes the characters sanitizeDisplay removes as Go escapes, the way
// %q does (`\x1b`, `\a`, `\u200b`), but leaves the rest of s, backslashes included,
// unquoted. It is for paths and error text, which must stay traceable to the real file.
func escapeDisplay(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Zl, unicode.Zp) {
			b.WriteString(escapeRune(r))
		} else {
			b.WriteRune(r)
		}
	}
	return parser.ReplaceInvisible(b.String(), escapeRune)
}

// displayPath is escapeDisplay for a path, in double quotes when it is empty or
// starts or ends with a space, which would otherwise not show in the message.
func displayPath(p string) string {
	s := escapeDisplay(p)
	if p == "" || strings.TrimSpace(p) != p {
		return `"` + s + `"`
	}
	return s
}

// shellQuotePath is escapeDisplay for a path used as an argument in a suggested
// command: in single quotes, each embedded single quote closed, escaped with a
// backslash and reopened, unless the escaped path has only characters a POSIX shell
// takes literally.
func shellQuotePath(p string) string {
	s := escapeDisplay(p)
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellLiteral(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellLiteral reports whether a POSIX shell takes r literally in a word. Non-ASCII
// runes are left to escapeDisplay, which has already escaped the invisible ones.
func shellLiteral(r rune) bool {
	switch {
	case r >= 0x80, 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return strings.ContainsRune("_-./:@%+=,", r)
}

// pathOnlyOnce returns the reason inside err when it is a *fs.PathError for path, so
// a message that already names path does not repeat it, otherwise err.
func pathOnlyOnce(path string, err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Path == path {
		return pathErr.Err
	}
	return err
}

// escapeRune returns r as a Go escape sequence, without quotes.
func escapeRune(r rune) string {
	q := strconv.QuoteRune(r)
	return q[1 : len(q)-1]
}

// escapeErr is escapeDisplay for an error's text.
func escapeErr(err error) string {
	return escapeDisplay(err.Error())
}

// displayWidth approximates the number of terminal cells s occupies: East Asian wide
// and fullwidth runes take two, combining marks, format characters (such as ZWJ) and
// variation selectors take none, except that the emoji selector U+FE0F widens a
// preceding one-cell rune to two. An emoji sequence takes two cells, as in terminals
// that cluster graphemes: a skin-tone modifier after an emoji, and an emoji joined to
// the previous one by ZWJ, add nothing beyond widening a one-cell base to two. ZWJ
// between other runes (Indic conjuncts) does not join them.
func displayWidth(s string) int {
	n, last := 0, 0
	pict, joined := false, false
	for _, r := range s {
		if joined {
			joined = false
			if isPictographic(r) {
				if last == 1 {
					n++
				}
				last = 2
				continue
			}
		}
		switch k := width.LookupRune(r).Kind(); {
		case r == 0x200d:
			joined = pict
		case r >= 0x1f3fb && r <= 0x1f3ff && pict:
			if last == 1 {
				n++
				last = 2
			}
		case r == 0xfe0f:
			if last == 1 {
				n++
				last = 2
			}
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf), r >= 0xfe00 && r <= 0xfe0e:
		case k == width.EastAsianWide, k == width.EastAsianFullwidth:
			n += 2
			last = 2
			pict = isPictographic(r)
		default:
			n++
			last = 1
			pict = isPictographic(r)
		}
	}
	return n
}

// isPictographic reports whether r is in a block that holds the emoji used in ZWJ
// sequences and as skin-tone bases: Miscellaneous Technical, Miscellaneous Symbols,
// Dingbats, Miscellaneous Symbols and Arrows, and U+1F000-1FAFF.
func isPictographic(r rune) bool {
	return (r >= 0x2300 && r <= 0x23ff) || (r >= 0x2600 && r <= 0x27bf) ||
		(r >= 0x2b00 && r <= 0x2bff) || (r >= 0x1f000 && r <= 0x1faff)
}

func printTasks(stdout, stderr io.Writer, file string, tasks []task.Task) {
	if len(tasks) == 0 {
		fmt.Fprintf(stderr, "made: no tasks found in %s\n", escapeDisplay(file))
		return
	}
	fmt.Fprintf(stdout, "Tasks in %s:\n", escapeDisplay(file))
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
// scripts, with no header. The description is flattened to one line. Tasks whose
// name is empty once sanitized are left out: they cannot be completed, and bash's
// read would take the description of a "\tdesc" line as the name.
func printNames(stdout io.Writer, tasks []task.Task) {
	for _, t := range tasks {
		name := sanitizeDisplay(t.Name)
		if name == "" {
			continue
		}
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
		return "unknown flag " + escapeDisplay(rest[0])
	}
	var arg string
	if i := len(args) - len(rest) - 1; i >= 0 {
		arg = args[i]
	}
	typed, _, hasValue := strings.Cut(arg, "=")
	f := fs.Lookup(strings.TrimPrefix(strings.TrimPrefix(typed, "-"), "-"))
	typed = escapeDisplay(typed)
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
