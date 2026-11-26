// Package display formats task names, paths and diagnostics for the terminal.
package display

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"unicode"

	"github.com/Tarektouati/made/internal/parser"
	"golang.org/x/text/width"
)

// Sanitize removes C0/C1 control characters, DEL, line/paragraph separators
// and invisible formatting characters (bidi controls, zero-width space, tag
// characters outside the England, Scotland and Wales flag sequences; see
// parser.ReplaceInvisible) from task names, descriptions and lint messages printed to
// the terminal. Task lookup still uses the parsed values.
func Sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
	return parser.ReplaceInvisible(s, func(rune) string { return "" })
}

// Escape writes the characters Sanitize removes as Go escapes, the way
// %q does (`\x1b`, `\a`, `\u200b`), but leaves the rest of s, backslashes included,
// unquoted. It is for paths and error text, which must stay traceable to the real file.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Zl, unicode.Zp) {
			b.WriteString(escapeRune(r))
		} else {
			b.WriteRune(r)
		}
	}
	return parser.ReplaceInvisible(b.String(), escapeRune)
}

// Path is Escape for a path, in double quotes when it is empty or
// starts or ends with a space, which would otherwise not show in the message.
func Path(p string) string {
	s := Escape(p)
	if p == "" || strings.TrimSpace(p) != p {
		return `"` + s + `"`
	}
	return s
}

// ShellQuotePath is Escape for a path used as an argument in a suggested
// command: in single quotes, each embedded single quote closed, escaped with a
// backslash and reopened, unless the escaped path has only characters a POSIX shell
// takes literally.
func ShellQuotePath(p string) string {
	s := Escape(p)
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellLiteral(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellLiteral reports whether a POSIX shell takes r literally in a word. Non-ASCII
// runes are left to Escape, which has already escaped the invisible ones.
func shellLiteral(r rune) bool {
	switch {
	case r >= 0x80, 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return strings.ContainsRune("_-./:@%+=,", r)
}

// PathOnlyOnce returns the reason inside err when it is a *fs.PathError for path, so
// a message that already names path does not repeat it, otherwise err.
func PathOnlyOnce(path string, err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Path == path {
		return pathErr.Err
	}
	return err
}

// escapeRune returns r as a Go escape sequence, without quotes.
func escapeRune(r rune) string {
	q := strconv.QuoteRune(r)
	return q[1 : len(q)-1]
}

// Error is Escape for an error's text.
func Error(err error) string {
	return Escape(err.Error())
}

// Width approximates the number of terminal cells s occupies: East Asian wide
// and fullwidth runes take two, combining marks, format characters (such as ZWJ) and
// variation selectors take none, except that the emoji selector U+FE0F widens a
// preceding one-cell rune to two. An emoji sequence takes two cells, as in terminals
// that cluster graphemes: a skin-tone modifier after an emoji, and an emoji joined to
// the previous one by ZWJ, add nothing beyond widening a one-cell base to two. ZWJ
// between other runes (Indic conjuncts) does not join them.
func Width(s string) int {
	n, last := 0, 0
	pict, joined := false, false
	for _, r := range s {
		if joined {
			joined = false
			if isPictographic(r) {
				if last == 1 {
					n++
				}
				last = 2
				continue
			}
		}
		switch k := width.LookupRune(r).Kind(); {
		case r == 0x200d:
			joined = pict
		case r >= 0x1f3fb && r <= 0x1f3ff && pict:
			if last == 1 {
				n++
				last = 2
			}
		case r == 0xfe0f:
			if last == 1 {
				n++
				last = 2
			}
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf), r >= 0xfe00 && r <= 0xfe0e:
		case k == width.EastAsianWide, k == width.EastAsianFullwidth:
			n += 2
			last = 2
			pict = isPictographic(r)
		default:
			n++
			last = 1
			pict = isPictographic(r)
		}
	}
	return n
}

// isPictographic reports whether r is in a block that holds the emoji used in ZWJ
// sequences and as skin-tone bases: Miscellaneous Technical, Miscellaneous Symbols,
// Dingbats, Miscellaneous Symbols and Arrows, and U+1F000-1FAFF.
func isPictographic(r rune) bool {
	return (r >= 0x2300 && r <= 0x23ff) || (r >= 0x2600 && r <= 0x27bf) ||
		(r >= 0x2b00 && r <= 0x2bff) || (r >= 0x1f000 && r <= 0x1faff)
}
