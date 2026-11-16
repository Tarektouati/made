package parser

import (
	"strings"
	"testing"
)

func repeatToken(token string, n int) []byte {
	return []byte(strings.Repeat(token, n) + "\n")
}

func TestParseBasic(t *testing.T) {
	src := []byte("### say-hello\nPrints a friendly greeting.\n```bash\necho hello\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	got := tasks[0]
	if got.Name != "say-hello" {
		t.Errorf("Name = %q, want %q", got.Name, "say-hello")
	}
	if got.Description != "Prints a friendly greeting." {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Lang != "bash" {
		t.Errorf("Lang = %q, want %q", got.Lang, "bash")
	}
	if got.Script != "echo hello\n" {
		t.Errorf("Script = %q, want %q", got.Script, "echo hello\n")
	}
}

func TestParseMultipleTasks(t *testing.T) {
	src := []byte("# Title\n\n## Section\n\n### one\n```bash\necho 1\n```\n\n### two\nsecond task\n```sh\necho 2\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].Name != "one" || tasks[1].Name != "two" {
		t.Errorf("names = %q, %q", tasks[0].Name, tasks[1].Name)
	}
	if tasks[0].Description != "" {
		t.Errorf("task one description = %q, want empty", tasks[0].Description)
	}
	if tasks[1].Description != "second task" {
		t.Errorf("task two description = %q", tasks[1].Description)
	}
}

func TestParseOnlyH3AreTasks(t *testing.T) {
	// H1 and H2 headings with code blocks must not become tasks.
	src := []byte("# h1\n```bash\necho h1\n```\n\n## h2\n```bash\necho h2\n```\n\n### h3\n```bash\necho h3\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	if tasks[0].Name != "h3" {
		t.Errorf("Name = %q, want %q", tasks[0].Name, "h3")
	}
}

func TestParseHeadingWithoutCodeBlockSkipped(t *testing.T) {
	// "notes" has no code block and a non-H3 heading interrupts it, so it is dropped.
	src := []byte("### notes\njust some prose, no code.\n\n## divider\n\n### real\n```bash\necho ok\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "real" {
		t.Fatalf("tasks = %+v, want only 'real'", tasks)
	}
}

func TestParseFirstCodeBlockWins(t *testing.T) {
	src := []byte("### t\n```bash\necho first\n```\n```bash\necho second\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	if tasks[0].Script != "echo first\n" {
		t.Errorf("Script = %q, want first block", tasks[0].Script)
	}
}

func TestParseCRLF(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantName   string
		wantDesc   string
		wantLang   string
		wantScript string
	}{
		{
			name:       "multi-line block",
			src:        "### crlf\r\n```bash\r\nif true; then\r\n  echo yes\r\nfi\r\n```\r\n",
			wantName:   "crlf",
			wantLang:   "bash",
			wantScript: "if true; then\n  echo yes\nfi\n",
		},
		{
			name:       "single line",
			src:        "### hi\r\n```bash\r\necho hi\r\n```\r\n",
			wantName:   "hi",
			wantLang:   "bash",
			wantScript: "echo hi\n",
		},
		{
			name:       "heading description and lang",
			src:        "### say-hello\r\nPrints a friendly greeting.\r\n```bash\r\necho hello\r\n```\r\n",
			wantName:   "say-hello",
			wantDesc:   "Prints a friendly greeting.",
			wantLang:   "bash",
			wantScript: "echo hello\n",
		},
		{
			name:       "blank line inside block",
			src:        "### t\r\n```bash\r\necho a\r\n\r\necho b\r\n```\r\n",
			wantName:   "t",
			wantLang:   "bash",
			wantScript: "echo a\n\necho b\n",
		},
		{
			name:       "mixed endings",
			src:        "### t\n```bash\r\necho a\necho b\r\n```\n",
			wantName:   "t",
			wantLang:   "bash",
			wantScript: "echo a\necho b\n",
		},
		{
			name:       "tilde fence",
			src:        "### t\r\n~~~bash\r\necho a\r\n~~~\r\n",
			wantName:   "t",
			wantLang:   "bash",
			wantScript: "echo a\n",
		},
		{
			name:       "mid-line CR kept",
			src:        "### t\r\n```bash\r\nprintf 'a\rb'\r\n```\r\n",
			wantName:   "t",
			wantLang:   "bash",
			wantScript: "printf 'a\rb'\n",
		},
		{
			name:       "LF only unchanged",
			src:        "### say-hello\nPrints a friendly greeting.\n```bash\necho hello\n```\n",
			wantName:   "say-hello",
			wantDesc:   "Prints a friendly greeting.",
			wantLang:   "bash",
			wantScript: "echo hello\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			got := tasks[0]
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantDesc)
			}
			if got.Lang != tt.wantLang {
				t.Errorf("Lang = %q, want %q", got.Lang, tt.wantLang)
			}
			if got.Script != tt.wantScript {
				t.Errorf("Script = %q, want %q", got.Script, tt.wantScript)
			}
		})
	}
}

func TestParseEmpty(t *testing.T) {
	tasks, err := Parse([]byte("# just a doc\n\nno tasks here.\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
}

func TestParseUTF8BOM(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	for _, tc := range []struct {
		name string
		src  string
		want []Task
	}{
		{
			name: "bom before heading",
			src:  bom + "### bom\n```\necho bom\n```\n",
			want: []Task{{Name: "bom", Script: "echo bom\n"}},
		},
		{
			name: "bom then blank line",
			src:  bom + "\n### bom\n```bash\necho bom\n```\n",
			want: []Task{{Name: "bom", Lang: "bash", Script: "echo bom\n"}},
		},
		{
			name: "bom with description and multiple tasks",
			src:  bom + "### one\nfirst task\n```bash\necho 1\n```\n\n### two\n```sh\necho 2\n```\n",
			want: []Task{
				{Name: "one", Description: "first task", Lang: "bash", Script: "echo 1\n"},
				{Name: "two", Lang: "sh", Script: "echo 2\n"},
			},
		},
		{
			name: "no bom unchanged",
			src:  "### bom\n```\necho bom\n```\n",
			want: []Task{{Name: "bom", Script: "echo bom\n"}},
		},
		{
			name: "bom only",
			src:  bom,
			want: nil,
		},
		{
			name: "mid-document feff not stripped",
			src:  "### a\n```\necho " + bom + "\n```\n",
			want: []Task{{Name: "a", Script: "echo " + bom + "\n"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != len(tc.want) {
				t.Fatalf("got %d tasks (%+v), want %d", len(tasks), tasks, len(tc.want))
			}
			for i, want := range tc.want {
				if tasks[i] != want {
					t.Errorf("task %d = %+v, want %+v", i, tasks[i], want)
				}
			}
		})
	}
}

func TestParseRejectsDeepBlockquoteNesting(t *testing.T) {
	_, err := Parse(repeatToken("> ", maxNestingDepth+1))
	if err == nil {
		t.Fatal("Parse: nil error, want nesting too deep")
	}
	if got := err.Error(); !strings.Contains(got, "nesting too deep") {
		t.Fatalf("Parse error = %q, want nesting too deep", got)
	}
}

func TestParseRejectsDeepBulletRun(t *testing.T) {
	_, err := Parse(repeatToken("- ", maxNestingDepth+1))
	if err == nil {
		t.Fatal("Parse: nil error, want nesting too deep")
	}
	if got := err.Error(); !strings.Contains(got, "nesting too deep") {
		t.Fatalf("Parse error = %q, want nesting too deep", got)
	}
}

func TestParseRejectsDeepMixedMarkerRun(t *testing.T) {
	_, err := Parse(repeatToken("> - ", maxNestingDepth+1))
	if err == nil {
		t.Fatal("Parse: nil error, want nesting too deep")
	}
	if got := err.Error(); !strings.Contains(got, "nesting too deep") {
		t.Fatalf("Parse error = %q, want nesting too deep", got)
	}
}

func TestParseRejectsDeepOrderedMarkerRun(t *testing.T) {
	_, err := Parse(repeatToken("1. ", maxNestingDepth+1))
	if err == nil {
		t.Fatal("Parse: nil error, want nesting too deep")
	}
	if got := err.Error(); !strings.Contains(got, "nesting too deep") {
		t.Fatalf("Parse error = %q, want nesting too deep", got)
	}
}

func TestParseAllowsNestingAtLimit(t *testing.T) {
	if _, err := Parse(repeatToken("> ", maxNestingDepth)); err != nil {
		t.Fatalf("Parse: %v, want nil at limit", err)
	}
	if _, err := Parse(repeatToken("- ", maxNestingDepth)); err != nil {
		t.Fatalf("Parse: %v, want nil at limit", err)
	}
}

func TestParseIgnoresMarkersInsideFences(t *testing.T) {
	src := []byte("### task\n" +
		"```bash\n" +
		"echo hello > out\n" +
		"rm -rf /\n" +
		"> not a blockquote\n" +
		"```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "task" {
		t.Fatalf("tasks = %+v, want one task named task", tasks)
	}
}

func TestParseBOMBeforeFenceIgnoresMarkers(t *testing.T) {
	src := []byte("\xef\xbb\xbf```\n" + string(repeatToken("> ", maxNestingDepth+1)) + "```\n")
	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse: %v, want deep markers inside a BOM-prefixed fence skipped", err)
	}
}

func TestParseIgnoresMarkersInsideTildeFences(t *testing.T) {
	src := []byte("### task\n" +
		"~~~bash\n" +
		"> not a blockquote\n" +
		"- not a list\n" +
		"~~~\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "task" {
		t.Fatalf("tasks = %+v, want one task named task", tasks)
	}
}

func TestParseTildeFenceCloseLength(t *testing.T) {
	deep := strings.Repeat("> ", maxNestingDepth+1) + "\n"
	insideFence := []byte("~~~\n" + deep + "~~\n")
	if _, err := Parse(insideFence); err != nil {
		t.Fatalf("Parse: %v, want deep markers skipped while fence stays open", err)
	}
	outside := []byte("~~~~\n\n~~~~\n" + deep)
	_, err := Parse(outside)
	if err == nil || !strings.Contains(err.Error(), "nesting too deep") {
		t.Fatalf("Parse outside fence = %v, want nesting too deep", err)
	}
}
