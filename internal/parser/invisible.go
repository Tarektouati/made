package parser

import (
	"strings"
	"unicode/utf8"
)

const (
	flagTagBase   = '\U0001F3F4' // WAVING BLACK FLAG, the base of emoji flag tag sequences
	tagCancel     = '\U000E007F'
	tagBlockFirst = '\U000E0000'
)

// flagTagSeqs are the tag characters, cancel tag included, of the only emoji flag tag
// sequences kept after a flag base: England (gbeng), Scotland (gbsct) and Wales
// (gbwls), the subdivision flags Unicode recommends. They are matched exactly in
// lowercase, the only form those flags take; any other tag run, including uppercase
// letters or extra tags before the cancel tag, can carry hidden text.
var flagTagSeqs = []string{tagSeq("gbeng"), tagSeq("gbsct"), tagSeq("gbwls")}

// tagSeq returns code written in tag characters, followed by the cancel tag.
func tagSeq(code string) string {
	var b strings.Builder
	for _, c := range code {
		b.WriteRune(tagBlockFirst + c)
	}
	b.WriteRune(tagCancel)
	return b.String()
}

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
// at the start of s when they are one of flagTagSeqs, or 0 if they are not.
func flagTagSeqLen(s string) int {
	for _, seq := range flagTagSeqs {
		if strings.HasPrefix(s, seq) {
			return len(seq)
		}
	}
	return 0
}

// ReplaceInvisible returns s with each invisible formatting character (see
// isInvisible) replaced by repl(r). Tag characters outside the England, Scotland and
// Wales flag sequences (see flagTagSeqs) are replaced like the rest.
func ReplaceInvisible(s string, repl func(r rune) string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == flagTagBase:
			size += flagTagSeqLen(s[i+size:])
			b.WriteString(s[i : i+size])
		case isInvisible(r):
			b.WriteString(repl(r))
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
