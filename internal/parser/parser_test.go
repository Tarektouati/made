package parser

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
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

func TestParseFenceLanguage(t *testing.T) {
	tests := []struct {
		name     string
		open     string
		close    string
		wantLang string
	}{
		{"plain", "```bash", "```", "bash"},
		{"space attrs", "```bash title=\"x\"", "```", "bash"},
		{"space braces", "```bash {1,3}", "```", "bash"},
		{"brace attrs", "```bash{.x}", "```", "bash"},
		{"comma opts", "```bash,title", "```", "bash"},
		{"tilde fence brace", "~~~python{.x}", "~~~", "python"},
		{"case preserved", "```Bash,x", "```", "Bash"},
		{"no language", "```", "```", ""},
		{"pandoc class only", "```{.python}", "```", "{.python}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("### t\n" + tt.open + "\necho hi\n" + tt.close + "\n")
			tasks, err := Parse(src)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			if got := tasks[0].Lang; got != tt.wantLang {
				t.Errorf("Lang = %q, want %q", got, tt.wantLang)
			}
		})
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

func TestParseEmptyFirstBlockIsScript(t *testing.T) {
	src := []byte("### empty\nAbout it.\n```bash\n```\nNot a description.\n```bash\necho real\n```\n")
	tasks, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Task{{Name: "empty", Description: "About it.", Lang: "bash", Script: "", Line: 1}}
	if !reflect.DeepEqual(tasks, want) {
		t.Errorf("tasks = %+v, want %+v", tasks, want)
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
			name:       "lone CR endings",
			src:        "### cr\r```bash\recho cr\r```\r",
			wantName:   "cr",
			wantLang:   "bash",
			wantScript: "echo cr\n",
		},
		{
			name:       "lone CR endings after a BOM",
			src:        "\xef\xbb\xbf### say-hello\rPrints a friendly greeting.\r```bash\recho a\r\recho b\r```",
			wantName:   "say-hello",
			wantDesc:   "Prints a friendly greeting.",
			wantLang:   "bash",
			wantScript: "echo a\n\necho b\n",
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

func TestParseHeadingTextDecoding(t *testing.T) {
	tests := []struct {
		name     string
		heading  string
		desc     string
		wantName string
		wantDesc string
	}{
		{"issue example", `entity &amp; stuff \* esc`, "d", "entity & stuff * esc", "d"},
		{"numeric entities", "a &#38; b &#x26; c", "d", "a & b & c", "d"},
		{"escaped ampersand keeps entity", `\&amp;`, "d", "&amp;", "d"},
		{"encoded backslash is literal", `&#92;*`, "d", `\*`, "d"},
		{"invalid entities kept", "&bogus; & &amp", "d", "&bogus; & &amp", "d"},
		{"backslash before letter kept", `a\b`, "d", `a\b`, "d"},
		{"tab collapsed", "a\tb", "d", "a b", "d"},
		{"space run collapsed", "a   b", "d", "a b", "d"},
		{"description decoded", "t", "x &lt;y&gt;\tz", "t", "x <y> z"},
		{"control char entity", "a&#27;b", "d", "ab", "d"},
		{"c0 control entity", "a&#1;b", "d", "ab", "d"},
		{"c1 control entity", "a&#150;b", "d", "ab", "d"},
		{"control entity in description", "t", "x&#27;y", "t", "xy"},
		{"rtl override dropped", "bid\u202ei", "d", "bidi", "d"},
		{"rtl override entity dropped", "bid&#x202E;i", "d", "bidi", "d"},
		{"bidi isolates dropped", "a\u2066b\u2069c", "d", "abc", "d"},
		{"zero width space dropped", "a\u200bb", "d", "ab", "d"},
		{"tag character dropped", "a\U000e0041b", "d", "ab", "d"},
		{"format char in description dropped", "t", "x\u202ey", "t", "xy"},
		{"lrm and word joiner dropped", "a\u200eb\u2060c", "d", "abc", "d"},
		{"zwj emoji kept", "🧑\u200d💻 dev setup", "d", "🧑\u200d💻 dev setup", "d"},
		{"zwj flag kept", "🏳\ufe0f\u200d🌈", "d", "🏳\ufe0f\u200d🌈", "d"},
		{"zwnj word kept", "می\u200cخواهم", "d", "می\u200cخواهم", "d"},
		{"soft hyphen kept", "co\u00adop", "d", "co\u00adop", "d"},
		{"scotland flag kept", "🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", "d", "🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", "d"},
		{"zwj in description kept", "t", "by 🧑\u200d💻", "t", "by 🧑\u200d💻"},
		{"line separator collapsed", "a\u2028b", "d", "a b", "d"},
		{"paragraph separator collapsed", "a&#x2029;b", "d", "a b", "d"},
		{"tab entity collapsed", "a&#9;b", "d", "a b", "d"},
		{"leading zero is decimal", "&#0123;", "d", "{", "d"},
		{"leading zero tab", "a&#09;b", "d", "a b", "d"},
		{"zero is replacement char", "a&#0;b", "d", "a\uFFFDb", "d"},
		{"out-of-range decimal is replacement char", "a&#9999999;b", "d", "a\uFFFDb", "d"},
		{"out-of-range hex is replacement char", "a&#x110000;b", "d", "a\uFFFDb", "d"},
		{"max code point hex", "a&#x10FFFF;b", "d", "a\U0010FFFFb", "d"},
		{"max code point decimal", "a&#1114111;b", "d", "a\U0010FFFFb", "d"},
		{"first out-of-range decimal is replacement char", "a&#1114112;b", "d", "a\uFFFDb", "d"},
		{"surrogate is replacement char", "a&#xD800;b", "d", "a\uFFFDb", "d"},
		{"hex entity", "&#X7B;", "d", "{", "d"},
		{"too many digits kept", "&#12345678;", "d", "&#12345678;", "d"},
		{"non-digit numeric kept", "&#1a;", "d", "&#1a;", "d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("### " + tt.heading + "\n" + tt.desc + "\n```bash\necho\n```\n")
			tasks, err := Parse(src)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			if tasks[0].Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tasks[0].Name, tt.wantName)
			}
			if tasks[0].Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", tasks[0].Description, tt.wantDesc)
			}
		})
	}
}

func TestParseInlineMarkupText(t *testing.T) {
	tests := []struct {
		name     string
		heading  string
		desc     string
		wantName string
		wantDesc string
	}{
		{"code span", "`build`", "d", "build", "d"},
		{"code span with text", "run `go test` now", "d", "run go test now", "d"},
		{"issue 21 example", "Build `all` now", "Run the *fast* build.", "Build all now", "Run the fast build."},
		{"code span only", "`onlycode`", "d", "onlycode", "d"},
		{"code span keeps escapes and entities", "`a\\*b &amp;`", "d", "a\\*b &amp;", "d"},
		{"emphasis", "*build* all", "d", "build all", "d"},
		{"underscore emphasis", "_build_", "d", "build", "d"},
		{"strong", "**deploy** prod", "d", "deploy prod", "d"},
		{"link", "[docs](https://example.com) site", "d", "docs site", "d"},
		{"autolink", "see <https://example.com>", "d", "see https://example.com", "d"},
		{"image alt text", "![logo](x.png) img", "d", "logo img", "d"},
		{"raw html dropped", "a <b>bold</b> c", "d", "a bold c", "d"},
		{"emphasis decodes entities", "*a &amp; b*", "d", "a & b", "d"},
		{"strong inside emphasis", "*a **b** c*", "d", "a b c", "d"},
		{"code span inside strong", "**`x`** y", "d", "x y", "d"},
		{"emphasis inside link", "[*go* `vet`](u)", "d", "go vet", "d"},
		{"description code span", "t", "Runs `go test` on **all** [pkgs](u).", "t", "Runs go test on all pkgs."},
		{"description soft break inside emphasis", "t", "*a\nb*", "t", "a b"},
		{"description hard break inside strong", "t", "**a\\\nb**", "t", "a b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("### " + tt.heading + "\n" + tt.desc + "\n\n```bash\necho\n```\n")
			tasks, err := Parse(src)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			if tasks[0].Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tasks[0].Name, tt.wantName)
			}
			if tasks[0].Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", tasks[0].Description, tt.wantDesc)
			}
		})
	}
}

func TestParseHTMLBlockSwallowsFence(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		tasks int
	}{
		{"no blank line", "### html\n<div>x</div>\n```bash\necho\n```\n", 0},
		{"blank line", "### html\n<div>x</div>\n\n```bash\necho\n```\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != tt.tasks {
				t.Fatalf("got %d tasks, want %d", len(tasks), tt.tasks)
			}
			if tt.tasks == 1 && tasks[0].Name != "html" {
				t.Errorf("Name = %q, want %q", tasks[0].Name, "html")
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
			want: []Task{{Name: "bom", Script: "echo bom\n", Line: 1}},
		},
		{
			name: "bom then blank line",
			src:  bom + "\n### bom\n```bash\necho bom\n```\n",
			want: []Task{{Name: "bom", Lang: "bash", Script: "echo bom\n", Line: 2}},
		},
		{
			name: "bom with description and multiple tasks",
			src:  bom + "### one\nfirst task\n```bash\necho 1\n```\n\n### two\n```sh\necho 2\n```\n",
			want: []Task{
				{Name: "one", Description: "first task", Lang: "bash", Script: "echo 1\n", Line: 1},
				{Name: "two", Lang: "sh", Script: "echo 2\n", Line: 7},
			},
		},
		{
			name: "no bom unchanged",
			src:  "### bom\n```\necho bom\n```\n",
			want: []Task{{Name: "bom", Script: "echo bom\n", Line: 1}},
		},
		{
			name: "bom only",
			src:  bom,
			want: nil,
		},
		{
			name: "mid-document feff not stripped",
			src:  "### a\n```\necho " + bom + "\n```\n",
			want: []Task{{Name: "a", Script: "echo " + bom + "\n", Line: 1}},
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
	_, err := Parse([]byte(strings.Repeat("- ", maxNestingDepth+1) + "x\n"))
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

func TestParseAllowsShallowMarkersInsideFences(t *testing.T) {
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

func TestParseAllowsShallowMarkersInsideTildeFences(t *testing.T) {
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

func TestParseDetailedPositions(t *testing.T) {
	const doc = "# Title\n\n### build\nBuilds it.\n```bash\necho a\n```\n\n```sh\necho b\n```\n\n## Section\n\n### `x` step\n\n### docs\n```text\nhi\n```\n"
	for _, tc := range []struct{ name, src string }{
		{"lf", doc},
		{"crlf", strings.ReplaceAll(doc, "\n", "\r\n")},
		{"bom", "\xef\xbb\xbf" + doc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks, err := ParseDetailed([]byte(tc.src))
			if err != nil {
				t.Fatalf("ParseDetailed: %v", err)
			}
			if len(tasks) != 3 {
				t.Fatalf("got %d headings, want 3: %+v", len(tasks), tasks)
			}
			build, step, docs := tasks[0], tasks[1], tasks[2]
			if build.Line != 3 || build.Name != "build" || build.Altered || len(build.Fences) != 2 ||
				build.Fences[0].Line != 5 || build.Fences[0].Lang != "bash" || build.Fences[0].Script != "echo a\n" ||
				build.Fences[1].Line != 9 || build.Fences[1].Lang != "sh" {
				t.Errorf("build = %+v", build)
			}
			if step.Line != 15 || step.Name != "x step" || step.Raw != "`x` step" || step.Altered || len(step.Fences) != 0 {
				t.Errorf("step = %+v", step)
			}
			if docs.Line != 17 || len(docs.Fences) != 1 || docs.Fences[0].Line != 18 || docs.Fences[0].Lang != "text" {
				t.Errorf("docs = %+v", docs)
			}
		})
	}
}

func TestParseDetailedAltered(t *testing.T) {
	tests := []struct {
		heading string
		want    bool
	}{
		{"plain", false},
		{`a\_b &amp; c`, false},
		{"`code`", false},
		{"*em* and **strong**", false},
		{"[link](u)", false},
		{"<https://example.com>", false},
		{"*a [`b`](u)*", false},
		{"<b>html</b>", true},
		{"![alt](x.png)", true},
		{"*nested <br> html*", true},
		{"[![alt](x.png)](u)", true},
	}
	for _, tt := range tests {
		t.Run(tt.heading, func(t *testing.T) {
			tasks, err := ParseDetailed([]byte("### " + tt.heading + "\n"))
			if err != nil {
				t.Fatalf("ParseDetailed: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d headings, want 1", len(tasks))
			}
			if tasks[0].Altered != tt.want {
				t.Errorf("Altered = %v, want %v (Name %q)", tasks[0].Altered, tt.want, tasks[0].Name)
			}
		})
	}
}

// TestParseDetailedMatchesParse checks ParseDetailed's first fences are Parse's tasks.
func TestParseDetailedMatchesParse(t *testing.T) {
	src := []byte("### a\n```bash\necho a\n```\n```sh\nx\n```\n### none\n# H1\n```bash\nignored\n```\n### b\n> ```bash\n> quoted\n> ```\n```python\nprint(1)\n```\n")
	want, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ParseDetailed(src)
	if err != nil {
		t.Fatal(err)
	}
	var got []Task
	for _, r := range raw {
		if len(r.Fences) > 0 {
			f := r.Fences[0]
			got = append(got, Task{Name: r.Name, Lang: f.Lang, Script: f.Script, Line: r.Line})
		}
	}
	if len(got) != len(want) {
		t.Fatalf("ParseDetailed tasks %+v, Parse tasks %+v", got, want)
	}
	for i := range want {
		want[i].Description = ""
		if got[i] != want[i] {
			t.Errorf("task %d: ParseDetailed %+v, Parse %+v", i, got[i], want[i])
		}
	}
}

func TestParseDetailedLoneCRLines(t *testing.T) {
	src := "### a\r```bash\rx\r```\r\r### b\r```bash\ry\r```\r"
	tasks, err := ParseDetailed([]byte(src))
	if err != nil {
		t.Fatalf("ParseDetailed: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks (%+v), want 2", len(tasks), tasks)
	}
	for i, want := range []struct{ line, fence int }{{1, 2}, {6, 7}} {
		if got := tasks[i]; got.Line != want.line || len(got.Fences) != 1 || got.Fences[0].Line != want.fence {
			t.Errorf("task %d = %+v, want heading line %d and one fence at line %d", i, got, want.line, want.fence)
		}
	}
}

func TestParseLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []int
	}{
		{name: "lf", src: "# T\n\n### a\nabout a\n\n```bash\nx\n```\n\n### none\n### b\n```bash\ny\n```\n", want: []int{3, 11}},
		{name: "crlf", src: "### a\r\n```bash\r\nx\r\n```\r\n\r\n### b\r\n```bash\r\ny\r\n```\r\n", want: []int{1, 6}},
		{name: "lone cr", src: "### a\r```bash\rx\r```\r\r### b\r```bash\ry\r```\r", want: []int{1, 6}},
		{name: "bom", src: "\xef\xbb\xbf\n### a\n```bash\nx\n```\n", want: []int{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var got []int
			for _, task := range tasks {
				got = append(got, task.Line)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("lines = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNestingErrorLine(t *testing.T) {
	src := append([]byte("### a\n\n"), repeatToken("> ", maxNestingDepth+1)...)
	_, err := ParseDetailed(src)
	var nest *NestingError
	if !errors.As(err, &nest) || nest.Line != 3 {
		t.Fatalf("ParseDetailed error = %v, want *NestingError at line 3", err)
	}
	if _, err := Parse(src); err == nil || err.Error() != "nesting too deep (max 256)" {
		t.Errorf("Parse error = %v, want the unchanged message", err)
	}
}

// TestParseRejectsDeepNestingAroundFences covers inputs where an earlier version of
// the scan skipped lines as fenced code that goldmark parses as Markdown.
func TestParseRejectsDeepNestingAroundFences(t *testing.T) {
	deep := strings.Repeat("> ", maxNestingDepth+1) + "\n"
	for _, tc := range []struct{ name, src string }{
		{"tab-indented fence", "\t```\n" + deep},
		{"space and tab indented fence", "  \t```\n" + deep},
		{"backtick in backtick info string", "```a`b\n" + deep},
		{"list item fence then dedent", "- a\n\n  ```\n" + deep},
		{"fence inside html block", "<div>\n```\n\n" + deep},
		{"fence inside html block in list item", "- <div>\n  ```\n\n  " + deep},
		{"fence inside html comment", "<!--\n```\n-->\n" + deep},
		{"fence inside pre block", "<pre>\n\n```\n</pre>\n" + deep},
		{"tab-indented closer does not reopen", "```\n\t```\n```\n" + deep},
		{"list item fence closed at 4 columns", "- a\n\n  ```\n    ```\n  " + deep},
		{"list item fence closed at 5 columns", "- a\n\n  ```\n     ```\n  " + deep},
		{"ordered list item fence closed at 6 columns", "1. a\n\n   ```\n      ```\n   " + deep},
		{"closer after a fence ended early is not an opener", " ```\n0\n```\n" + deep},
		{"indent before an end-of-line marker counts", "*\n  " + strings.Repeat("> ", maxNestingDepth) + "*\n"},
		{"early-closed fence", "  ```\n     ```\n```\n" + deep},
		{"fence after a container marker", "0) ```\n   ```\n   " + deep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var nest *NestingError
			if _, err := Parse([]byte(tc.src)); !errors.As(err, &nest) {
				t.Fatalf("Parse error = %v, want *NestingError", err)
			}
		})
	}
}

// TestParseBlankLinesDoNotNest checks a whitespace-only line adds no depth, however
// wide: it cannot open or continue a container.
func TestParseBlankLinesDoNotNest(t *testing.T) {
	for _, line := range []string{strings.Repeat(" ", 600), strings.Repeat("\t", 200), " \t \t" + strings.Repeat(" ", 600)} {
		src := "### t\n" + line + "\n```bash\necho ok\n```\n"
		if _, err := Parse([]byte(src)); err != nil {
			t.Errorf("Parse(%d-byte blank line): %v, want nil", len(line), err)
		}
	}
}

// TestParseIndentedCodeDoesNotNest checks deep indentation adds no depth when no
// earlier line nests deep: it can only continue list items that are already open.
func TestParseIndentedCodeDoesNotNest(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"spaces before a marker", strings.Repeat(" ", 2000) + "- x"},
		{"tabs before a marker", strings.Repeat("\t", 300) + "- x"},
		{"spaces before text", strings.Repeat(" ", 514) + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "### t\n" + tc.line + "\n```bash\necho ok\n```\n"
			if _, err := Parse([]byte(src)); err != nil {
				t.Errorf("Parse: %v, want nil", err)
			}
		})
	}
}

// TestParseTrailingWhitespaceDoesNotNest checks whitespace after the last marker on a
// line adds no depth, while indent before an end-of-line marker still does.
func TestParseTrailingWhitespaceDoesNotNest(t *testing.T) {
	if _, err := Parse([]byte("> " + strings.Repeat(" ", 600) + "\n")); err != nil {
		t.Errorf("Parse(marker then 600 spaces): %v, want nil", err)
	}
	src := strings.Repeat("- ", 150) + "x\n" + strings.Repeat(" ", 300) + strings.Repeat("> ", 110) + "-\n"
	var nest *NestingError
	if _, err := Parse([]byte(src)); !errors.As(err, &nest) {
		t.Errorf("Parse(indent then markers ending in an empty item) = %v, want *NestingError", err)
	}
}

// TestParseCountsMarkersInsideFences pins a deliberate trade-off: the scan counts
// lines inside code blocks too, so a code line nested past the limit is rejected.
func TestParseCountsMarkersInsideFences(t *testing.T) {
	deep := strings.Repeat("> ", maxNestingDepth+1) + "\n"
	for _, tc := range []struct{ name, src string }{
		{"backtick fence", "### t\n```bash\n" + deep + "```\n"},
		{"tilde fence", "~~~\n" + deep + "~~~\n"},
		{"BOM-prefixed fence", "\xef\xbb\xbf```\n" + deep + "```\n"},
		{"list item fence", "- a\n\n  ```\n  " + deep + "  ```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var nest *NestingError
			if _, err := Parse([]byte(tc.src)); !errors.As(err, &nest) {
				t.Fatalf("Parse error = %v, want *NestingError", err)
			}
		})
	}
}

// TestParseThematicBreakDoesNotNest checks a run of "-" or "*" markers that forms a
// thematic break adds no depth: goldmark tries a thematic break before a list item.
func TestParseThematicBreakDoesNotNest(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"dashes", strings.Repeat("- ", 257)},
		{"stars", strings.Repeat("* ", 257)},
		{"tab-separated dashes", strings.Repeat("-\t", 257)},
		{"after blockquotes", strings.Repeat("> ", 10) + strings.Repeat("- ", 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "### t\n" + tc.line + "\n```bash\necho ok\n```\n"
			if _, err := Parse([]byte(src)); err != nil {
				t.Errorf("Parse: %v, want nil", err)
			}
		})
	}
}

// TestParseRejectsDeepNestingBeforeThematicBreak checks markers before a thematic
// break, or runs that are not one, still count.
func TestParseRejectsDeepNestingBeforeThematicBreak(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"blockquotes then a break", strings.Repeat("> ", maxNestingDepth+1) + "- - -"},
		{"mixed markers", strings.Repeat("- * ", 200) + "x"},
		{"stars then text", strings.Repeat("* ", maxNestingDepth+1) + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var nest *NestingError
			if _, err := Parse([]byte(tc.line + "\n")); !errors.As(err, &nest) {
				t.Fatalf("Parse error = %v, want *NestingError", err)
			}
		})
	}
}

// TestParseRejectsIndentedNesting covers nesting the scan only sees by counting markers
// and indent together: markers after four or more columns of indent (inside a list
// item), and list items continued through indentation.
func TestParseRejectsIndentedNesting(t *testing.T) {
	deep := strings.Repeat("> ", maxNestingDepth+1) + "\n"
	stair := strings.Repeat("- ", 150) + "x\n" +
		strings.Repeat(" ", 300) + strings.Repeat("- ", 150) + "x\n"
	for _, tc := range []struct{ name, src string }{
		{"blockquotes inside a list item", "- - x\n\n    " + deep},
		{"blockquotes after a tab", "- - x\n\n\t" + deep},
		{"list items continued by indent", stair},
		{"list items continued by deep indent", strings.Repeat("- ", 200) + "x\n" +
			strings.Repeat(" ", 2000) + strings.Repeat("- ", 60) + "x\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var nest *NestingError
			if _, err := Parse([]byte(tc.src)); !errors.As(err, &nest) {
				t.Fatalf("Parse error = %v, want *NestingError", err)
			}
		})
	}
}

func BenchmarkParseDetailedManyTasks(b *testing.B) {
	var sb strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&sb, "### t%d\n```bash\necho\n```\n", i)
	}
	src := []byte(sb.String())
	for b.Loop() {
		if _, err := ParseDetailed(src); err != nil {
			b.Fatal(err)
		}
	}
}
