package parser

import (
	"strings"
	"testing"
)

func TestParseTaskGroups(t *testing.T) {
	source := strings.Join([]string{
		"### loose", "```sh", "true", "```",
		"## **Build** &amp; test", "### one", "```sh", "true", "```",
		"#### detail", "prose", "### two", "```sh", "true", "```",
		"## **Build** &amp; test", "### three", "```sh", "true", "```",
		"# Reset", "### tail", "```sh", "true", "```",
		"## <br>", "### blank-group", "```sh", "true", "```",
	}, "\n")
	tasks, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, group     string
		line, groupLine int
	}{
		{"loose", "", 1, 0},
		{"one", "Build & test", 6, 5},
		{"two", "Build & test", 12, 5},
		{"three", "Build & test", 17, 16},
		{"tail", "", 22, 0},
		{"blank-group", "", 27, 26},
	}
	if len(tasks) != len(want) {
		t.Fatalf("tasks = %+v, want %d", tasks, len(want))
	}
	for i, w := range want {
		got := tasks[i]
		if got.Name != w.name || got.Group != w.group || got.Line != w.line || got.GroupLine != w.groupLine {
			t.Errorf("task %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestParseGroupsUseOnlyTopLevelHeadings(t *testing.T) {
	source := "## Real\n```markdown\n## In a fence\n```\n> ## In a blockquote\n\n- ## In a list\n\n### task\n```sh\ntrue\n```\n"
	tasks, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Name != "task" || tasks[0].Group != "Real" || tasks[0].GroupLine != 1 {
		t.Errorf("tasks = %+v, want task in Real", tasks)
	}
}

func TestParseGroupHeadingTextAndLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		source := "\xef\xbb\xbf" + strings.Join([]string{"# Title", "", "Build `tools` &amp; test\u202e\u200b", "---", "### run", "```sh", "true", "```", ""}, ending)
		tasks, err := Parse([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 1 || tasks[0].Group != "Build tools & test" || tasks[0].GroupLine != 3 || tasks[0].Line != 5 {
			t.Errorf("line ending %q: tasks = %+v", ending, tasks)
		}
	}
}
