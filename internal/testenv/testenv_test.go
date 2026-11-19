package testenv

import "testing"

func TestRequireCmd(t *testing.T) {
	const missing = "made-testenv-no-such-command"
	for _, tc := range []struct {
		name, cmd, require  string
		wantSkip, wantFatal bool
	}{
		{name: "present", cmd: "go", require: "go"},
		{name: "missing, not required", cmd: missing, wantSkip: true},
		{name: "missing, required", cmd: missing, require: "bash " + missing + " zsh", wantFatal: true},
		{name: "missing, other required", cmd: missing, require: "bash zsh", wantSkip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(RequireEnv, tc.require)
			f := &fakeTB{}
			RequireCmd(f, tc.cmd)
			if got := f.skipped != ""; got != tc.wantSkip {
				t.Errorf("skipped = %v (%q), want %v", got, f.skipped, tc.wantSkip)
			}
			if got := f.fatal != ""; got != tc.wantFatal {
				t.Errorf("fatal = %v (%q), want %v", got, f.fatal, tc.wantFatal)
			}
		})
	}
}
