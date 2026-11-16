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

func TestParseEmpty(t *testing.T) {
	tasks, err := Parse([]byte("# just a doc\n\nno tasks here.\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
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
