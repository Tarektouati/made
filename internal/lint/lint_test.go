package lint

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/runner"
)

// finding is the part of a Diagnostic a test pins down; messages are checked loosely.
type finding struct {
	Line     int
	Severity Severity
	Rule     string
}

func findings(diags []Diagnostic) []finding {
	var out []finding
	for _, d := range diags {
		out = append(out, finding{d.Line, d.Severity, d.Rule})
	}
	return out
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []finding // nil: no findings
	}{
		{name: "clean", src: "# T\n\n### build\nBuilds.\n```bash\ngo build\n```\n\n### test\n```\ngo test\n```\n"},
		{name: "duplicate", src: "### a\n```bash\nx\n```\n### a\n```bash\ny\n```\n", want: []finding{{5, Error, "duplicate-task"}}},
		{name: "same name, no fence, not duplicate", src: "### a\n```bash\nx\n```\n### a\nprose\n", want: []finding{{5, Warning, "no-code-block"}}},
		{name: "unsupported language", src: "### a\n```yaml\nk: v\n```\n", want: []finding{{2, Error, "unsupported-language"}}},
		{name: "language case and default", src: "### a\n```BASH\nx\n```\n### b\n```\nx\n```\n"},
		{name: "empty name", src: "### <br>\n```bash\nx\n```\n", want: []finding{{1, Error, "empty-name"}}},
		{name: "empty names never duplicate", src: "### <br>\n```bash\nx\n```\n### <i></i>\n```bash\nx\n```\n", want: []finding{{1, Error, "empty-name"}, {5, Error, "empty-name"}}},
		{name: "code span name", src: "### `x`\n```bash\nx\n```\n"},
		{name: "emphasis, strong and link names", src: "### *a*\n```bash\nx\n```\n### **b**\n```bash\nx\n```\n### [c](u)\n```bash\nx\n```\n"},
		{name: "image name altered", src: "### ![logo](x.png) build\n```bash\nx\n```\n", want: []finding{{1, Warning, "name-altered"}}},
		{name: "empty script", src: "### a\n```bash\n```\n", want: []finding{{2, Error, "empty-script"}}},
		{name: "blank script", src: "### a\n```bash\n  \n\n```\n", want: []finding{{2, Error, "empty-script"}}},
		{name: "empty script before code", src: "### a\n```bash\n```\n\n```bash\nx\n```\n", want: []finding{{2, Error, "empty-script"}, {5, Warning, "extra-code-block"}}},
		{name: "nul byte", src: "### a\n```bash\necho\nx\x00\n```\n", want: []finding{{4, Error, "nul-byte"}}},
		{name: "nesting", src: "### a\n" + strings.Repeat("> ", 257) + "\n", want: []finding{{2, Error, "nesting-too-deep"}}},
		{name: "name altered", src: "### <b>build</b> all\n```bash\nx\n```\n", want: []finding{{1, Warning, "name-altered"}}},
		{name: "escapes are not markup", src: "### a\\_b &amp; c\n```bash\nx\n```\n"},
		{name: "reserved name", src: "### init\n```bash\nx\n```\n### completion\n```bash\nx\n```\n", want: []finding{{1, Error, "unreachable-name"}, {5, Error, "unreachable-name"}}},
		{name: "dash name", src: "### -x\n```bash\nx\n```\n", want: []finding{{1, Warning, "unreachable-name"}}},
		{name: "no code block", src: "### a\nprose\n## next\n```bash\nx\n```\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less code span heading", src: "### `--verbose`\nSome prose.\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less reserved heading", src: "### init\nprose\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less markup heading", src: "### *Note*\nprose\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "extra code block", src: "### a\n```bash\nx\n```\n\n```bash\ny\n```\n", want: []finding{{6, Warning, "extra-code-block"}}},
		{name: "docs fence replaces unsupported", src: "### a\n```markdown\n# hi\n```\n### b\n```Text\nhi\n```\n", want: []finding{{2, Warning, "docs-fence"}, {6, Warning, "docs-fence"}}},
		{name: "sorted by line", src: "### a\n```bash\n```\n```yaml\n```\n### a\n```bash\nx\n```\n", want: []finding{{2, Error, "empty-script"}, {4, Warning, "extra-code-block"}, {6, Error, "duplicate-task"}}},
		{name: "script too large", src: "### a\n```bash\n" + strings.Repeat("x", runner.ArgLimit+1) + "\n```\n", want: []finding{{2, Error, "script-too-large"}}},
		{name: "script under limit", src: "### a\n```bash\n" + strings.Repeat("x", runner.ArgLimit/2) + "\n```\n"},
		{name: "large unsupported script", src: "### a\n```yaml\n" + strings.Repeat("x", runner.ArgLimit+1) + "\n```\n", want: []finding{{2, Error, "unsupported-language"}}},
		{name: "crlf and bom", src: "\xef\xbb\xbf### a\r\n```yaml\r\nk\r\n```\r\n", want: []finding{{2, Error, "unsupported-language"}}},
		{name: "lone cr", src: "### a\r```yaml\rk\r```\r### a\r```bash\rx\r```\r", want: []finding{{2, Error, "unsupported-language"}, {5, Error, "duplicate-task"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findings(Check([]byte(tt.src), Options{}))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestCheckHost(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	src := "### rb\n```ruby\nputs 1\n```\n### rb2\n```ruby\nputs 2\n```\n### py\n```python\nprint(1)\n```\n" +
		"### y\n```yaml\nk\n```\n### e\n```ruby\n```\n### d\n```text\nhi\n```\n"
	wantHost := []finding{
		{2, Error, "interpreter-not-found"},
		{6, Error, "interpreter-not-found"},
		{10, Error, "interpreter-not-found"},
		{14, Error, "unsupported-language"},
		{18, Error, "empty-script"},
		{21, Warning, "docs-fence"},
	}
	if got := findings(Check([]byte(src), Options{Host: true})); !slices.Equal(got, wantHost) {
		t.Errorf("Check(Host) = %+v\nwant %+v", got, wantHost)
	}
	wantNoHost := []finding{{14, Error, "unsupported-language"}, {18, Error, "empty-script"}, {21, Warning, "docs-fence"}}
	if got := findings(Check([]byte(src), Options{})); !slices.Equal(got, wantNoHost) {
		t.Errorf("Check() = %+v\nwant %+v", got, wantNoHost)
	}

	diags := Check([]byte(src), Options{Host: true})
	for i, w := range []string{`"ruby" was not found on PATH (required by task "rb", language "ruby")`, `task "rb2"`, `neither "python3" nor "python" was found on PATH`} {
		if !strings.Contains(diags[i].Msg, w) {
			t.Errorf("diag %d message %q, want it to contain %q", i, diags[i].Msg, w)
		}
	}
}

func TestCheckMessages(t *testing.T) {
	diags := Check([]byte("### a\n```bash\nx\n```\n### a\n```yaml\nk\n```\n### <i>c</i> d\n```bash\nx\n```\n"), Options{})
	want := []string{
		`"a" already defined at line 1`,
		`task "a" uses "yaml"; supported: bash, `,
		"heading \"<i>c</i> d\" is read as task \"c d\"",
	}
	if len(diags) != len(want) {
		t.Fatalf("Check() = %+v, want %d findings", diags, len(want))
	}
	for i, w := range want {
		if !strings.Contains(diags[i].Msg, w) {
			t.Errorf("diag %d message %q, want it to contain %q", i, diags[i].Msg, w)
		}
	}
}

// TestCheckScriptTooLargeMessage checks that the message gives the length the check
// measured, including the pad a script starting with "-" gets, in the OS's unit.
func TestCheckScriptTooLargeMessage(t *testing.T) {
	script := "-" + strings.Repeat("x", runner.ArgLimit-3) + "\n" // padded: exactly ArgLimit
	diags := Check([]byte("### a\n```bash\n"+script+"```\n"), Options{})
	if got := findings(diags); !slices.Equal(got, []finding{{2, Error, "script-too-large"}}) {
		t.Fatalf("Check() = %+v, want one script-too-large", got)
	}
	size := strconv.Itoa(runner.ArgLimit) + " " + runner.ArgUnit
	for _, w := range []string{"script is " + size + ";", "argument of about " + size + " or more"} {
		if !strings.Contains(diags[0].Msg, w) {
			t.Errorf("message %q, want it to contain %q", diags[0].Msg, w)
		}
	}
}
