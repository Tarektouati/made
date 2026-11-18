package parser

import (
	"strings"
	"unicode/utf8"
)

const (
	flagTagBase   = '\U0001F3F4' // WAVING BLACK FLAG, the base of emoji flag tag sequences
	tagSpecFirst  = '\U000E0020'
	tagSpecLast   = '\U000E007E'
	tagCancel     = '\U000E007F'
	tagBlockFirst = '\U000E0000'
)

// isInvisible reports whether r is a formatting character that can hide or reorder
// text: the Arabic letter mark, zero width space, LRM/RLM, bidi embeddings and
// overrides, word joiner, bidi isolates, BOM and tag characters. ZWJ, ZWNJ and the
// soft hyphen are left alone; emoji and many scripts need them.
func isInvisible(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200b, r == 0x200e, r == 0x200f, r == 0x2060, r == 0xfeff:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= tagBlockFirst && r <= tagCancel:
		return true
	}
	return false
}

// flagTagSeqLen returns the byte length of the tag characters following a flag base
// in s when they form an emoji flag tag sequence (one or more tag specs ended by the
// cancel tag, as in the Scotland flag), or 0 if they do not.
func flagTagSeqLen(s string) int {
	n, specs := 0, 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		n += size
		switch {
		case r >= tagSpecFirst && r <= tagSpecLast:
			specs++
		case r == tagCancel && specs > 0:
			return n
		default:
			return 0
		}
	}
	return 0
}

// ReplaceInvisible returns s with each invisible formatting character (see
// isInvisible) replaced by repl(r). Tag characters that complete an emoji flag tag
// sequence are kept.
func ReplaceInvisible(s string, repl func(r rune) string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == flagTagBase {
			size += flagTagSeqLen(s[i+size:])
			b.WriteString(s[i : i+size])
		} else if isInvisible(r) {
			b.WriteString(repl(r))
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
