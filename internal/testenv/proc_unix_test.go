//go:build unix

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestReadPids(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          []int
		wantFatal     bool
	}{
		{name: "one per line", content: "12\n345\n", want: []int{12, 345}},
		{name: "space separated", content: "12 345", want: []int{12, 345}},
		{name: "empty", content: "", wantFatal: true},
		{name: "blank", content: "\n \n", wantFatal: true},
		{name: "not a pid", content: "12\nx\n", wantFatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pids")
			// #nosec G306 -- Non-sensitive fixture inside a private temporary directory.
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			f := &fakeTB{}
			got := ReadPids(f, path)
			if (f.fatal != "") != tc.wantFatal {
				t.Errorf("fatal = %q, want fatal %v", f.fatal, tc.wantFatal)
			}
			if !tc.wantFatal && !slices.Equal(got, tc.want) {
				t.Errorf("ReadPids = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		f := &fakeTB{}
		ReadPids(f, filepath.Join(t.TempDir(), "none"))
		if f.fatal == "" {
			t.Error("ReadPids on a missing file did not fail the test")
		}
	})
}

func TestAlive(t *testing.T) {
	if !Alive(t, os.Getpid()) {
		t.Error("Alive(own pid) = false, want true")
	}
	cmd := exec.CommandContext(t.Context(), "true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(t, cmd.Process.Pid) {
		t.Errorf("Alive(%d) = true for a reaped process, want false", cmd.Process.Pid)
	}
}
