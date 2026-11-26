package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Tarektouati/made/internal/discover"
	"github.com/Tarektouati/made/internal/display"
	"github.com/Tarektouati/made/internal/lint"
	"github.com/Tarektouati/made/internal/parser"
	"github.com/Tarektouati/made/internal/runner"
	"github.com/Tarektouati/made/internal/task"
)

func (a *app) runFile(args []string, opts options, stdout, stderr io.Writer) int {
	file := opts.file
	res, err := discover.ResolveFile(file)
	for _, s := range res.Skipped {
		fmt.Fprintf(stderr, "made: skipping %s: %s\n", display.Escape(s.Path), display.Error(s.Err))
	}
	if err != nil {
		fmt.Fprintf(stderr, "made: %s\n", display.Error(err))
		if ue := (*discover.UntrustedError)(nil); errors.As(err, &ue) {
			fmt.Fprintf(stderr, "made: hint: if you trust it, make it yours and not writable by others (chmod go-w), or run it deliberately with made -f %s\n", display.ShellQuotePath(ue.Path))
		}
		return 1
	}
	if len(res.Others) > 0 && !opts.listNames {
		fmt.Fprintf(stderr, "made: using %s (also found %s)\n", display.Escape(res.Path), strings.Join(res.Others, ", "))
	}
	file = res.Path

	if opts.check {
		return a.runCheck(stdout, stderr, file, res.Explicit, res.Trusted, opts.strict, lint.Options{Host: opts.host})
	}

	// Tab completion runs --list-names with the typed -f value, so a pipe there must
	// fail at once rather than block or read the terminal.
	source, regular, err := discover.ReadMadefile(file, res.Explicit && !opts.listNames, res.Trusted)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", display.Path(file), display.Error(display.PathOnlyOnce(file, err)))
		return 1
	}

	type parsed struct {
		tasks []task.Task
		err   error
	}
	out, err := withinParseTimeout(a.parseTimeout, func() parsed {
		tasks, perr := parser.Parse(source)
		return parsed{tasks, perr}
	})
	if err == nil {
		err = out.err
	}
	tasks := out.tasks
	if nest := (*parser.NestingError)(nil); errors.As(err, &nest) {
		fmt.Fprintf(stderr, "made: cannot parse %s: line %d: %s\n", display.Escape(file), nest.Line, display.Error(err))
		fmt.Fprintf(stderr, "made: hint: flatten the lists or blockquotes on that line or indent it less (lines in code blocks count too); run made --check -f %s\n", display.ShellQuotePath(file))
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot parse %s: %s\n", display.Escape(file), display.Error(err))
		return 1
	}

	if opts.listNames {
		printNames(stdout, tasks)
		return 0
	}

	// Every command that must stay quiet (completion, scripts, init, --check, -V,
	// self-update) has returned by now. The notice comes from the cache and is printed
	// after the task, so it is never mixed with the task's output or waited for.
	notice := a.startUpdateCheck(stdout, stderr)
	defer func() {
		if line := notice.Notice(); line != "" {
			fmt.Fprintln(stderr, line)
		}
	}()

	runRequested := !opts.list && len(args) > 0
	// Before runnableTasks, so its warnings cite lines of a file already named.
	if runRequested && res.FromParent && len(res.Others) == 0 { // the duplicate note already named it
		fmt.Fprintf(stderr, "made: using %s\n", display.Escape(file))
	}
	tasks = runnableTasks(stderr, tasks)

	if !runRequested {
		printTasks(stdout, stderr, file, tasks)
		return 0
	}

	name := args[0]
	task, ok := find(tasks, name)
	if !ok {
		fmt.Fprintf(stderr, "made: unknown task %q\n\n", name)
		printTasks(stdout, stderr, file, tasks)
		return 1
	}

	return runTask(task, file, regular, opts.noChdir, args[1:], stderr)
}

func runTask(task task.Task, file string, regular, noChdir bool, taskArgs []string, stderr io.Writer) int {
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
			fmt.Fprintf(stderr, "made: %s\n", display.Error(err))
			return 1
		}
		dir = filepath.Dir(abs)
	}

	// Everything after the task name goes to the script. Flag parsing already stopped
	// at the task name, so a leading "--" is optional; drop one if present.
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
		fmt.Fprintf(stderr, "made: %s\n", display.Error(runErr))
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
