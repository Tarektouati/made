package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/Tarektouati/made/internal/discover"
	"github.com/Tarektouati/made/internal/display"
)

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

func (a *app) initCommand(fs *flag.FlagSet, file string, stdout, stderr io.Writer) int {
	if file != "" {
		fmt.Fprintf(stderr, "made: init does not take -f; it creates %s in the current directory\n", discover.Names()[0])
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
		fmt.Fprintf(w, "Create a starter %s in the current directory.\n\n", discover.Names()[0])
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
	return a.initMadefile(stdout, stderr, force)
}

// initMadefile writes the init template (see initTemplate) to Madefile.md in the current directory and
// returns the exit code. Existing files are matched against discover.Names exactly, and
// only regular files count, like discover.FindMadefile; parent directories are not searched, so a nested Madefile can be
// created on purpose. When one already exists, it refuses (exit 1) unless force is
// set; with force it overwrites the highest-priority existing file in place, keeping
// its name, and leaves any other Madefiles there untouched.
func (a *app) initMadefile(stdout, stderr io.Writer, force bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot determine current directory: %s\n", display.Error(err))
		return 1
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "made: cannot read %s: %s\n", display.Escape(cwd), display.Error(err))
		return 1
	}
	var present []string // in discover.Names priority order
	for _, n := range discover.Names() {
		if slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == n }) && isRegularFile(n) {
			present = append(present, n)
		}
	}

	if len(present) == 0 {
		// O_EXCL never overwrites, even if a file appeared since the check or the
		// filesystem folds case.
		name := discover.Names()[0]
		if err := a.writeTemplate(name, os.O_CREATE|os.O_EXCL); err != nil {
			if kind, ok := notRegular(name); ok {
				if kind != "" {
					kind = " (" + kind + ")"
				}
				fmt.Fprintf(stderr, "made: cannot create %s: it exists and is not a regular file%s; remove it first\n", name, kind)
				return 1
			}
			fmt.Fprintf(stderr, "made: cannot create %s: %s\n", name, display.Error(err))
			return 1
		}
		fmt.Fprintf(stdout, "made: created %s\n", name)
		return 0
	}

	target := present[0]
	if !force {
		fmt.Fprintf(stderr, "made: %s already exists in %s (use made init --force to overwrite it)\n", target, display.Escape(cwd))
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
	if err := a.writeTemplate(target, os.O_TRUNC|initForceNoFollow); err != nil {
		if isSymlink(target) {
			fmt.Fprintf(stderr, "made: %s is a symlink (refusing to overwrite)\n", target)
			return 1
		}
		fmt.Fprintf(stderr, "made: cannot write %s: %s\n", target, display.Error(err))
		return 1
	}
	fmt.Fprintf(stdout, "made: overwrote %s\n", target)
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
			return "a broken symlink to " + display.Escape(dest), true
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
func (a *app) writeTemplate(name string, flags int) error {
	f, err := os.OpenFile(name, os.O_WRONLY|flags, 0o644)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, initTemplate(resolveVersion(a.version)))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// isRegularFile reports whether path exists and is a regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// isSymlink reports whether path exists and is a symlink (does not follow the link).
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
