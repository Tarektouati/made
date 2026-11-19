package parser

import (
	"fmt"
	"strings"
	"testing"
)

// tagged returns the flag tag sequence for subdivision code: the black flag, code in
// tag characters, and the cancel tag.
func tagged(code string) string {
	var b strings.Builder
	b.WriteRune(flagTagBase)
	for _, c := range code {
		b.WriteRune(0xe0000 + c)
	}
	b.WriteRune(tagCancel)
	return b.String()
}

// tagMarks returns code as ReplaceInvisible marks its tag characters, then the
// cancel tag's mark.
func tagMarks(code string) string {
	var b strings.Builder
	for _, c := range code {
		fmt.Fprintf(&b, "<%x>", 0xe0000+c)
	}
	return b.String() + "<e007f>"
}

func TestReplaceInvisible(t *testing.T) {
	const scotland = "🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "build", "build"},
		{"arabic letter mark", "a\u061cb", "a<61c>b"},
		{"zero width space", "a\u200bb", "a<200b>b"},
		{"lrm rlm", "\u200e\u200f", "<200e><200f>"},
		{"embeddings and overrides", "\u202a\u202b\u202c\u202d\u202e", "<202a><202b><202c><202d><202e>"},
		{"word joiner", "a\u2060b", "a<2060>b"},
		{"isolates", "\u2066\u2067\u2068\u2069", "<2066><2067><2068><2069>"},
		{"bom", "\ufeffa", "<feff>a"},
		{"stray tag", "a\U000e0041b", "a<e0041>b"},
		{"tag block start", "\U000e0000", "<e0000>"},
		{"zwj kept", "🧑\u200d💻", "🧑\u200d💻"},
		{"zwnj kept", "می\u200cخواهم", "می\u200cخواهم"},
		{"soft hyphen kept", "co\u00adop", "co\u00adop"},
		{"flag tag sequence kept", scotland, scotland},
		{"flag sequence then stray tag", scotland + "\U000e0041", scotland + "<e0041>"},
		{"unterminated flag sequence", "🏴\U000e0067\U000e0062x", "🏴<e0067><e0062>x"},
		{"tags after other base", "a\U000e0067\U000e007f", "a<e0067><e007f>"},
		{"bare black flag", "🏴", "🏴"},
		{"england flag kept", tagged("gbeng"), tagged("gbeng")},
		{"wales flag kept", tagged("gbwls"), tagged("gbwls")},
		{"flags in text kept", "go " + tagged("gbsct") + tagged("gbwls") + " now", "go " + tagged("gbsct") + tagged("gbwls") + " now"},
		{"hidden text after flag", "a" + tagged("hello") + "b", "a🏴" + tagMarks("hello") + "b"},
		{"extra specs before cancel", tagged("gbsctx"), "🏴" + tagMarks("gbsctx")},
		{"prefix of a flag", tagged("gbsc"), "🏴" + tagMarks("gbsc")},
		{"uppercase flag letters", tagged("GBSCT"), "🏴" + tagMarks("GBSCT")},
		{"unlisted subdivision", tagged("usca"), "🏴" + tagMarks("usca")},
		{"lone cancel tag", "a\U000e007fb", "a<e007f>b"},
		{"flag code without black flag", "a" + tagged("gbsct")[len("🏴"):] + "b", "a" + tagMarks("gbsct") + "b"},
	}
	mark := func(r rune) string { return fmt.Sprintf("<%x>", r) }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReplaceInvisible(tt.in, mark); got != tt.want {
				t.Errorf("ReplaceInvisible(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseKeepsOnlyRealFlagTagSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"england", tagged("gbeng"), tagged("gbeng")},
		{"scotland", tagged("gbsct"), tagged("gbsct")},
		{"wales", tagged("gbwls"), tagged("gbwls")},
		{"hidden text", "a" + tagged("hello") + "b", "a🏴b"},
		{"extra specs before cancel", tagged("gbsctx"), "🏴"},
		{"lone cancel tag", "a\U000e007fb", "ab"},
		{"tags without black flag", "a" + tagged("gbsct")[len("🏴"):] + "b", "ab"},
		{"zwj sequences kept", "🧑\u200d💻 🏳\ufe0f\u200d🌈", "🧑\u200d💻 🏳\ufe0f\u200d🌈"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "### t " + tt.in + "\nby " + tt.in + "\n```bash\necho\n```\n"
			tasks, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}
			if want := "t " + tt.want; tasks[0].Name != want {
				t.Errorf("Name = %q, want %q", tasks[0].Name, want)
			}
			if want := "by " + tt.want; tasks[0].Description != want {
				t.Errorf("Description = %q, want %q", tasks[0].Description, want)
			}
		})
	}
}
