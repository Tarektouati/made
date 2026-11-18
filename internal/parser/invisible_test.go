package parser

import (
	"fmt"
	"testing"
)

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
