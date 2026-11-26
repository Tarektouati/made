package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tarektouati/made/internal/display"
	"github.com/Tarektouati/made/internal/selfupdate"
)

func (a *app) selfUpdateCommand(fs *flag.FlagSet, file string, stdout, stderr io.Writer) int {
	if file != "" {
		fmt.Fprintln(stderr, "made: self-update does not take -f")
		return 2
	}
	if f := firstSetFlag(fs, "f", "file"); f != "" {
		fmt.Fprintf(stderr, "made: self-update does not take %s\n", f)
		return 2
	}
	return a.selfUpdate(fs.Args()[1:], stdout, stderr)
}

// selfUpdate runs made self-update with args, the words after "self-update", and
// returns the exit code: 0 when made was updated or already current, or for a dry
// run; 2 for a usage error; otherwise one of the selfupdate.Exit codes. The version
// and --dry-run may come in either order.
func (a *app) selfUpdate(args []string, stdout, stderr io.Writer) int {
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

	in := a.detectInstall(resolveVersion(a.version), a.version != "")
	if m := selfupdate.Detect(in); m != selfupdate.Manual {
		fmt.Fprintf(stderr, "made: %s\n", display.Escape(selfupdate.Refusal(m, in)))
		return selfupdate.ExitNotSelfUpdatable
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	u := selfupdate.Updater{
		Client:  selfupdate.Client{BaseURL: a.selfUpdateBaseURL},
		Exe:     in.Exe,
		Current: in.Version,
		Target:  target,
		GOOS:    in.GOOS,
		GOARCH:  in.GOARCH,
		DryRun:  dryRun,
		Stdout:  stdout,
	}
	if err := u.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "made: %s\n", display.Error(err))
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
