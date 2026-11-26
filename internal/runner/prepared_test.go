package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/task"
)

func TestPreparedRunUsesSnapshot(t *testing.T) {
	requireCmd(t, "bash")
	const variable = "MADE_PREPARED_VALUE"
	t.Setenv(variable, "inherited")
	dir := t.TempDir()
	args := []string{"", "literal $(false)\n--flag"}
	script := "printf '%s\\n' \"$#\" \"$1\" \"$2\" \"$" + variable + "\" > result\n"
	p, err := Prepare(task.Task{Name: "snapshot", Lang: "bash", Script: script}, dir, args, []string{variable + "=file"})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p.Path) || p.Script != script || p.Dir != dir {
		t.Errorf("prepared metadata = %q, %q, %q", p.Path, p.Script, p.Dir)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "result")); !os.IsNotExist(statErr) {
		t.Fatalf("Prepare ran the script: %v", statErr)
	}
	// Neither caller-owned slices nor a later process-environment change may alter
	// the command and environment that were prepared for review.
	args[1] = "changed argument"
	t.Setenv(variable, "changed environment")
	t.Setenv("PATH", t.TempDir())
	if err = p.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "2\n\nliteral $(false)\n--flag\ninherited\n"; string(got) != want {
		t.Errorf("prepared task output = %q, want %q", got, want)
	}
}

func TestPrepareUsesDefaultPATHOnlyWhenUnset(t *testing.T) {
	requireCmd(t, "bash")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(bash)
	t.Setenv("PATH", "")
	if err = os.Unsetenv("PATH"); err != nil {
		t.Fatal(err)
	}
	task := task.Task{Name: "path", Script: "[ \"$PATH\" = \"$1\" ]\n"}
	p, err := Prepare(task, "", []string{path}, []string{"PATH=" + path})
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != bash {
		t.Errorf("default PATH resolved %q, want %q", p.Path, bash)
	}
	if err := p.Run(context.Background(), nil); err != nil {
		t.Fatalf("child PATH differs from lookup PATH: %v", err)
	}
	for _, inherited := range []string{"", ".", "relative-bin"} {
		t.Setenv("PATH", inherited)
		_, err := Prepare(task, "", nil, []string{"PATH=" + path})
		var ie *InterpreterError
		if !errors.As(err, &ie) || ie.ExitCode() != 127 {
			t.Errorf("inherited PATH %q was overridden: %v", inherited, err)
		}
	}
}

func TestMergeEnvironmentPrecedence(t *testing.T) {
	base := []string{"A=process", "EMPTY=", "PATH=process-path", "=C:=C:\\work"}
	defaults := []string{"A=file", "EMPTY=file", "PATH=file-path", "NEW=first", "NEW=last"}
	want := append(slices.Clone(base), "NEW=last")
	if got := mergeEnvironment(base, defaults); !slices.Equal(got, want) {
		t.Errorf("merge = %q, want %q", got, want)
	}
	if got := mergeEnvironment([]string{"Path=process"}, []string{"PATH=file"}); runtime.GOOS == "windows" {
		if !slices.Equal(got, []string{"Path=process"}) {
			t.Errorf("Windows keys must ignore case: %q", got)
		}
	} else if !slices.Equal(got, []string{"Path=process", "PATH=file"}) {
		t.Errorf("Unix keys must preserve case: %q", got)
	}
}

func TestPrepareKeepsScriptPaddingAndErrors(t *testing.T) {
	requireCmd(t, "bash")
	p, err := Prepare(task.Task{Name: "pad", Script: "-n\n"}, "", []string{"-x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-e", "-o", "pipefail", "-c", " -n\n", "made", "-x"}; !slices.Equal(p.Args, want) || p.Script != "-n\n" {
		t.Errorf("prepared arguments = %q, script %q; want %q and unpadded script", p.Args, p.Script, want)
	}
	for _, tc := range []struct {
		task task.Task
		want string
	}{
		{task.Task{Name: "empty", Script: " \n"}, "empty code block"},
		{task.Task{Name: "nul", Script: "ok\n\x00"}, "NUL byte (line 2"},
		{task.Task{Name: "unknown", Lang: "cobol", Script: "x"}, "unsupported language"},
	} {
		if _, err := Prepare(tc.task, "", nil, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Prepare %q = %v, want %q", tc.task.Name, err, tc.want)
		}
	}
}
