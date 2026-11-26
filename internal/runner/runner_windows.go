//go:build windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
)

// errorFilenameExcedRange is ERROR_FILENAME_EXCED_RANGE, which CreateProcess returns for
// a command line longer than 32,767 characters.
const errorFilenameExcedRange = syscall.Errno(206)

// ArgLimit is the approximate size, in UTF-16 code units, above which a script can no
// longer be passed as an argument: CreateProcess caps the whole command line at 32,767
// characters.
const ArgLimit = 32767

// ArgUnit names the unit of ArgLimit and argLen, for messages.
const ArgUnit = "UTF-16 characters"

// argLen returns the size of s as the OS counts it against ArgLimit: in UTF-16 code
// units, so a character outside the Basic Multilingual Plane counts twice. Invalid
// UTF-8 counts one unit per byte, as it becomes U+FFFD.
func argLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// configureProcessGroup is a no-op on Windows, which lacks POSIX process groups. Tearing
// down the full process tree (e.g. via a job object) is out of scope; teardown is
// best-effort against the interpreter process only.
func configureProcessGroup(cmd *exec.Cmd) bool {
	return false
}

func signalTerm(cmd *exec.Cmd, td *teardown) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// signalTermSurvivors does nothing: only the interpreter, which has exited, is tracked.
func signalTermSurvivors(cmd *exec.Cmd, td *teardown) {}

// trackDescendants does nothing: descendants are not tracked on Windows.
func trackDescendants(cmd *exec.Cmd, td *teardown) {}

// trackSurvivors does nothing: descendants are not tracked on Windows.
func trackSurvivors(cmd *exec.Cmd, td *teardown) {}

// fromTerminal always reports false: there is no terminal foreground group on Windows.
func fromTerminal(td *teardown) bool { return false }

func signalKill(cmd *exec.Cmd, td *teardown) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// survivors always reports false: without process groups only the interpreter, which
// has already exited, is tracked.
func survivors(cmd *exec.Cmd, td *teardown) bool {
	return false
}

// argListTooLong reports whether err is Windows rejecting the command line as too long.
func argListTooLong(err error) bool { return errors.Is(err, errorFilenameExcedRange) }

// hostLimits reports that CreateProcess caps the whole command line, which the
// environment is not part of.
func hostLimits() osLimits {
	return osLimits{os: "Windows", unit: ArgUnit, perArg: ArgLimit, cmdLine: true}
}

// envLen returns 0: the environment does not count against the command-line limit.
func envLen([]string) int { return 0 }

// lookExecutable uses the child's PATHEXT without changing process-wide variables.
// The ordinary inherited case stays with os/exec. Explicit candidates with an
// extension are also checked through os/exec, so the prepared path is the path
// CommandContext will use, even when the child's PATHEXT differs from made's.
func lookExecutable(path string, env []string) (string, error) {
	ext := environmentValue(env, "PATHEXT")
	if ext == os.Getenv("PATHEXT") {
		return exec.LookPath(path)
	}
	if ext == "" {
		ext = ".com;.exe;.bat;.cmd"
	}
	for e := range strings.SplitSeq(strings.ToLower(ext), ";") {
		if e == "" {
			continue
		}
		if e[0] != '.' {
			e = "." + e
		}
		candidate := path + e
		if p, err := exec.LookPath(candidate); err == nil && p == candidate {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}
