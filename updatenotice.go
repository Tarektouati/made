package main

import (
	"io"
	"os"

	"github.com/Tarektouati/made/internal/selfupdate"
	"github.com/Tarektouati/made/internal/updatecheck"
)

// startUpdateCheck starts the new-version check (see package updatecheck), or returns
// nil when the user opted out, in CI, when stdout or stderr is not a terminal, and for
// builds that are not a release.
func (a *app) startUpdateCheck(stdout, stderr io.Writer) *updatecheck.Check {
	if os.Getenv("MADE_NO_UPDATE_CHECK") != "" || os.Getenv("NO_UPDATE_NOTIFIER") != "" {
		return nil
	}
	if _, ci := os.LookupEnv("CI"); ci {
		return nil
	}
	if !a.isTerminal(stdout) || !a.isTerminal(stderr) {
		return nil
	}
	v := resolveVersion(a.version)
	if !selfupdate.IsRelease(v) {
		return nil
	}
	dir, err := a.updateCacheDir()
	if err != nil {
		return nil
	}
	return updatecheck.Start(updatecheck.Config{
		Current:  v,
		Method:   selfupdate.Detect(a.detectInstall(v, a.version != "")),
		Client:   selfupdate.Client{BaseURL: a.selfUpdateBaseURL},
		CacheDir: dir,
		Timeout:  a.updateTimeout,
	})
}
