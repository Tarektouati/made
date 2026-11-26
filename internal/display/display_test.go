package display

import (
	"testing"
)

const scotlandFlag = "🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"

func TestShellQuotePath(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"Madefile.md", "Madefile.md"},
		{"docs/tasks-v2.md", "docs/tasks-v2.md"},
		{"my tasks.md", "'my tasks.md'"},
		{"it's.md", `'it'\''s.md'`},
		{"a;b$c.md", "'a;b$c.md'"},
		{"x\x1b.md", `'x\x1b.md'`},
		{"", "''"},
	} {
		if got := ShellQuotePath(tc.path); got != tc.want {
			t.Errorf("shellQuotePath(%q) = %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestSanitizeDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: "hello"},
		{name: "csi clear", in: "a\x1b[2Jb", want: "a[2Jb"},
		{name: "osc title", in: "\x1b]0;evil\x07title", want: "]0;eviltitle"},
		{name: "tab", in: "a\tb", want: "ab"},
		{name: "newline", in: "a\nb", want: "ab"},
		{name: "del", in: "a\x7fb", want: "ab"},
		{name: "c1 csi", in: "a\u009b2Jb", want: "a2Jb"},
		{name: "unicode", in: "保留", want: "保留"},
		{name: "rtl override", in: "bid\u202ei", want: "bidi"},
		{name: "bidi isolates", in: "a\u2066b\u2067c\u2068d\u2069e", want: "abcde"},
		{name: "zero width space", in: "a\u200bb", want: "ab"},
		{name: "bom", in: "\ufeffa", want: "a"},
		{name: "tag character", in: "a\U000e0041b", want: "ab"},
		{name: "line separator", in: "a\u2028b", want: "ab"},
		{name: "paragraph separator", in: "a\u2029b", want: "ab"},
		{name: "combining mark kept", in: "e\u0301", want: "e\u0301"},
		{name: "variation selector kept", in: "❤\ufe0f", want: "❤\ufe0f"},
		{name: "arabic letter mark", in: "a\u061cb", want: "ab"},
		{name: "lrm and rlm", in: "a\u200eb\u200fc", want: "abc"},
		{name: "embeddings and overrides", in: "a\u202ab\u202bc\u202cd\u202de", want: "abcde"},
		{name: "word joiner", in: "a\u2060b", want: "ab"},
		{name: "zwj emoji kept", in: "🧑\u200d💻 dev", want: "🧑\u200d💻 dev"},
		{name: "zwj flag kept", in: "🏳\ufe0f\u200d🌈", want: "🏳\ufe0f\u200d🌈"},
		{name: "zwnj word kept", in: "می\u200cخواهم", want: "می\u200cخواهم"},
		{name: "soft hyphen kept", in: "co\u00adop", want: "co\u00adop"},
		{name: "scotland flag kept", in: scotlandFlag, want: scotlandFlag},
		{name: "hidden tag text after flag", in: "a🏴\U000e0068\U000e0065\U000e006c\U000e006c\U000e006f\U000e007fb", want: "a🏴b"},
		{name: "unterminated tag sequence", in: "🏴\U000e0067\U000e0062x", want: "🏴x"},
		{name: "cancel tag alone", in: "a\U000e007fb", want: "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("sanitizeDisplay(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEscapeDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "/tmp/dir/Madefile.md", want: "/tmp/dir/Madefile.md"},
		{name: "backslash kept", in: `C:\work\Madefile.md`, want: `C:\work\Madefile.md`},
		{name: "osc title", in: "x\x1b]0;PWNED\x07dir", want: `x\x1b]0;PWNED\adir`},
		{name: "tab and newline", in: "a\tb\nc", want: `a\tb\nc`},
		{name: "del", in: "a\x7fb", want: `a\x7fb`},
		{name: "c1 csi", in: "a\u009bb", want: `a\u009bb`},
		{name: "zero width space", in: "a\u200bb", want: `a\u200bb`},
		{name: "rtl override", in: "bid\u202ei", want: `bid\u202ei`},
		{name: "line separator", in: "a\u2028b", want: `a\u2028b`},
		{name: "stray tag", in: "a\U000e0041b", want: `a\U000e0041b`},
		{name: "zwj emoji kept", in: "🧑\u200d💻", want: "🧑\u200d💻"},
		{name: "scotland flag kept", in: scotlandFlag, want: scotlandFlag},
		{name: "hidden tag text after flag", in: "a🏴\U000e0068\U000e0065\U000e006c\U000e006c\U000e006f\U000e007fb", want: `a🏴\U000e0068\U000e0065\U000e006c\U000e006c\U000e006f\U000e007fb`},
		{name: "unicode kept", in: "保留", want: "保留"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Escape(tt.in); got != tt.want {
				t.Errorf("escapeDisplay(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDisplayWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "empty", in: "", want: 0},
		{name: "ascii", in: "build", want: 5},
		{name: "cjk", in: "日本語", want: 6},
		{name: "emoji", in: "emoji🚀", want: 7},
		{name: "fullwidth", in: "ＡＢ", want: 4},
		{name: "halfwidth katakana", in: "ｱ", want: 1},
		{name: "combining accent", in: "e\u0301", want: 1},
		{name: "precomposed accent", in: "é", want: 1},
		{name: "emoji selector widens narrow symbol", in: "❤\ufe0f", want: 2},
		{name: "emoji selector on wide rune", in: "🚀\ufe0f", want: 2},
		{name: "text selector", in: "❤\ufe0e", want: 1},
		{name: "lone emoji selector", in: "\ufe0f", want: 0},
		{name: "zwj", in: "\u200d", want: 0},
		{name: "zwj family", in: "👨\u200d👩\u200d👧", want: 2},
		{name: "zwj family before text", in: "👨\u200d👩\u200d👧family", want: 8},
		{name: "skin tone", in: "👍🏽", want: 2},
		{name: "skin tone on narrow base", in: "✌🏽", want: 2},
		{name: "skin tone on narrow index finger", in: "☝🏽", want: 2},
		{name: "skin tone on narrow raised hand", in: "🖐🏽", want: 2},
		{name: "skin tone on narrow detective", in: "🕵🏽", want: 2},
		{name: "skin tone in zwj sequence", in: "👩🏻\u200d💻", want: 2},
		{name: "zwj gender sign with selector", in: "🏃\u200d♂\ufe0f", want: 2},
		{name: "zwj with selectors on both sides", in: "👁\ufe0f\u200d🗨\ufe0f", want: 2},
		{name: "zwj rainbow flag", in: "🏳\ufe0f\u200d🌈", want: 2},
		{name: "zwj black cat", in: "🐈\u200d⬛", want: 2},
		{name: "zwj on narrow base", in: "❤\u200d🔥", want: 2},
		{name: "lone skin tone", in: "🏽", want: 2},
		{name: "skin tone after letter", in: "a🏽", want: 3},
		{name: "zwj between letters", in: "a\u200db", want: 2},
		{name: "zwj in devanagari conjunct", in: "क्\u200dष", want: 2},
		{name: "regional indicator flag", in: "🇫🇷", want: 2},
		{name: "tag sequence flag", in: scotlandFlag, want: 2},
		{name: "trailing zwj", in: "👨\u200d", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Width(tt.in); got != tt.want {
				t.Errorf("displayWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
