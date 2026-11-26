package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Tarektouati/made/internal/selfupdate"
	"golang.org/x/term"
)

// app holds the dependencies and limits for the CLI. Tests configure their own
// instance; the release's linker-set version is passed in only by main.
type app struct {
	version           string
	parseTimeout      time.Duration
	selfUpdateBaseURL string
	detectInstall     func(string, bool) selfupdate.Install
	updateCacheDir    func() (string, error)
	updateTimeout     time.Duration
	isTerminal        func(io.Writer) bool
}

func newApp(version string) *app {
	return &app{
		version:       version,
		parseTimeout:  1500 * time.Millisecond,
		detectInstall: selfupdate.Current,
		updateCacheDir: func() (string, error) {
			dir, err := os.UserCacheDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(dir, "made"), nil
		},
		updateTimeout: time.Second,
		isTerminal: func(w io.Writer) bool {
			f, ok := w.(*os.File)
			return ok && term.IsTerminal(int(f.Fd()))
		},
	}
}

type options struct {
	file                                                       string
	list, showVersion, noChdir, listNames, check, strict, host bool
}

// run holds the program logic and returns the process exit code so deferred cleanup
// and a single os.Exit site stay together. args excludes the program name. made's own
// output goes to stdout and stderr; a task's process still inherits the real
// os.Stdin/os.Stdout/os.Stderr.
func (a *app) run(args []string, stdout, stderr io.Writer) int {
	var opts options
	fs := flag.NewFlagSet("made", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // parse errors are reported by flagError
	fs.StringVar(&opts.file, "file", "", "Markdown file to read tasks from (default: nearest Madefile.md, searched up to the repository root, or $HOME or the filesystem root outside a repository)")
	fs.StringVar(&opts.file, "f", "", "shorthand for --file")
	fs.BoolVar(&opts.noChdir, "no-chdir", false, "run the task in the current directory instead of the task file's directory")
	fs.BoolVar(&opts.check, "check", false, "check the task file for problems instead of running a task")
	fs.BoolVar(&opts.strict, "strict", false, "with --check, fail on warnings too")
	fs.BoolVar(&opts.host, "host", false, "with --check, also check that each task's interpreter is on PATH")
	fs.BoolVar(&opts.list, "list", false, "list available tasks")
	fs.BoolVar(&opts.list, "l", false, "shorthand for --list")
	fs.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	fs.BoolVar(&opts.showVersion, "V", false, "shorthand for --version")
	// list-names is for the completion scripts and stays out of the usage text.
	fs.BoolVar(&opts.listNames, "list-names", false, "print task names and descriptions, tab-separated, one per line")
	// The usage is printed below so that help can go to stdout.
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		// Keep the exit codes flag.ExitOnError used: 0 for -h/-help, 2 for a bad flag.
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, fs)
			return 0
		}
		fmt.Fprintf(stderr, "made: %s (see made --help)\n", flagError(fs, args, err))
		return 2
	}

	if opts.strict && !opts.check {
		fmt.Fprintln(stderr, "made: --strict requires --check")
		return 2
	}
	if opts.host && !opts.check {
		fmt.Fprintln(stderr, "made: --host requires --check")
		return 2
	}
	if opts.check {
		if fs.NArg() > 0 {
			fmt.Fprintln(stderr, "made: --check takes no task name")
			return 2
		}
		if f := firstSetFlag(fs, "f", "file", "no-chdir", "check", "strict", "host"); f != "" {
			fmt.Fprintf(stderr, "made: --check does not take %s\n", f)
			return 2
		}
	}

	if opts.showVersion {
		fmt.Fprintln(stdout, "made", resolveVersion(a.version))
		return 0
	}

	// An explicit empty -f (often an unset variable) must not fall back to discovery.
	fileSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "f" || f.Name == "file" {
			fileSet = true
		}
	})
	if fileSet && opts.file == "" {
		fmt.Fprintln(stderr, "made: -f requires a file path")
		return 2
	}

	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "init":
			return a.initCommand(fs, opts.file, stdout, stderr)
		case "completion":
			return completionCommand(fs, opts.file, stdout, stderr)
		case "self-update":
			return a.selfUpdateCommand(fs, opts.file, stdout, stderr)
		}
	}
	return a.runFile(fs.Args(), opts, stdout, stderr)
}
