package lint

import (
	"slices"
	"strings"
	"testing"
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
		{name: "empty name", src: "### `x`\n```bash\nx\n```\n", want: []finding{{1, Error, "empty-name"}}},
		{name: "empty names never duplicate", src: "### `x`\n```bash\nx\n```\n### `y`\n```bash\nx\n```\n", want: []finding{{1, Error, "empty-name"}, {5, Error, "empty-name"}}},
		{name: "empty script", src: "### a\n```bash\n```\n", want: []finding{{2, Error, "empty-script"}}},
		{name: "blank script", src: "### a\n```bash\n  \n\n```\n", want: []finding{{2, Error, "empty-script"}}},
		{name: "nul byte", src: "### a\n```bash\necho\nx\x00\n```\n", want: []finding{{4, Error, "nul-byte"}}},
		{name: "nesting", src: "### a\n" + strings.Repeat("> ", 257) + "\n", want: []finding{{2, Error, "nesting-too-deep"}}},
		{name: "name altered", src: "### *build* all\n```bash\nx\n```\n", want: []finding{{1, Warning, "name-altered"}}},
		{name: "escapes are not markup", src: "### a\\_b &amp; c\n```bash\nx\n```\n"},
		{name: "reserved name", src: "### init\n```bash\nx\n```\n### completion\n```bash\nx\n```\n", want: []finding{{1, Warning, "unreachable-name"}, {5, Warning, "unreachable-name"}}},
		{name: "dash name", src: "### -x\n```bash\nx\n```\n", want: []finding{{1, Warning, "unreachable-name"}}},
		{name: "no code block", src: "### a\nprose\n## next\n```bash\nx\n```\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less code span heading", src: "### `--verbose`\nSome prose.\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less reserved heading", src: "### init\nprose\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "fence-less markup heading", src: "### *Note*\nprose\n", want: []finding{{1, Warning, "no-code-block"}}},
		{name: "extra code block", src: "### a\n```bash\nx\n```\n\n```bash\ny\n```\n", want: []finding{{6, Warning, "extra-code-block"}}},
		{name: "docs fence replaces unsupported", src: "### a\n```markdown\n# hi\n```\n### b\n```Text\nhi\n```\n", want: []finding{{2, Warning, "docs-fence"}, {6, Warning, "docs-fence"}}},
		{name: "sorted by line", src: "### a\n```bash\n```\n```yaml\n```\n### a\n```bash\nx\n```\n", want: []finding{{2, Error, "empty-script"}, {4, Warning, "extra-code-block"}, {6, Error, "duplicate-task"}}},
		{name: "crlf and bom", src: "\xef\xbb\xbf### a\r\n```yaml\r\nk\r\n```\r\n", want: []finding{{2, Error, "unsupported-language"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findings(Check([]byte(tt.src)))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestCheckMessages(t *testing.T) {
	diags := Check([]byte("### a\n```bash\nx\n```\n### a\n```yaml\nk\n```\n### `c` d\n```bash\nx\n```\n"))
	want := []string{
		`"a" already defined at line 1`,
		`task "a" uses "yaml"; supported: bash, `,
		"heading \"`c` d\" is read as task \"d\"",
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
