package parser

import (
	"bytes"
	"fmt"
)

// maxNestingDepth limits blockquote/list marker depth before goldmark runs.
const maxNestingDepth = 256

// NestingError reports container nesting (blockquotes, lists) deeper than the parser
// accepts. Line is the 1-based line where the limit is first exceeded.
type NestingError struct{ Line int }

func (e *NestingError) Error() string {
	return fmt.Sprintf("nesting too deep (max %d)", maxNestingDepth)
}

func validateNesting(source []byte) error {
	return validateNestingLimit(source, maxNestingDepth)
}

// validateNestingLimit rejects source with a line whose container nesting may go
// over limit (see lineNestingDepth). It counts every line, including lines inside
// fenced code blocks and HTML blocks, which goldmark does not parse as Markdown.
// Telling those apart needs goldmark's full block structure, and any line the scan
// skipped while goldmark parsed it would let deep nesting through. The cost is that
// a code line with more than limit markers, or indented far enough, is rejected.
func validateNestingLimit(source []byte, limit int) error {
	for n := 1; len(source) > 0; n++ {
		var line []byte
		line, source = nextLine(source)
		if lineNestingDepth(line) > limit {
			return &NestingError{Line: n}
		}
	}
	return nil
}

func nextLine(source []byte) (line, rest []byte) {
	i := bytes.IndexByte(source, '\n')
	if i < 0 {
		return trimCR(source), nil
	}
	return trimCR(source[:i]), source[i+1:]
}

func trimCR(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		return b[:len(b)-1]
	}
	return b
}

// lineNestingDepth returns an upper bound on the container depth goldmark can reach
// on line: one per container marker in the line's prefix (blockquote, bullet, and
// ordered list markers, mixed sequences like "> - > -" too), plus half the whitespace
// columns before a marker or text. A line opens new containers only with markers,
// and they count after any indent: inside a list item, "    > > >" is a run of
// blockquotes. A line continues a list item only when it is indented to the item's
// content, and each nested item adds at least two columns ("- "). A blockquote
// continues only with its own ">" marker. A thematic break ("- - -", "* * *") counts
// as text: at every block start goldmark tries it before a list item, and it opens
// no container.
func lineNestingDepth(line []byte) int {
	// pending is whitespace not yet followed by a marker or text. Trailing
	// whitespace cannot continue a list item, so a blank line adds no depth.
	markers, ws, pending, col := 0, 0, 0, 0
	tbMark, tbFrom, tbTo := thematicBreakRange(line)
	i := 0
	for i < len(line) {
		switch c := line[i]; {
		case c == tbMark && i >= tbFrom && i <= tbTo:
			return markers + (ws+pending)/2
		case c == ' ':
			pending++
			col++
			i++
		case c == '\t':
			w := 4 - col%4
			pending += w
			col += w
			i++
		case c == '>':
			markers++
			ws, pending = ws+pending, 0
			i, col = skipMarkerSpace(line, i+1, col+1)
		case isBulletMarker(c) && (i+1 == len(line) || isSpaceOrTab(line[i+1])):
			markers++
			ws, pending = ws+pending, 0
			i, col = skipMarkerSpace(line, i+1, col+1)
		default:
			j := i
			for j < len(line) && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j > i && j < len(line) && (line[j] == '.' || line[j] == ')') &&
				(j+1 == len(line) || isSpaceOrTab(line[j+1])) {
				markers++
				ws, pending = ws+pending, 0
				i, col = skipMarkerSpace(line, j+1, col+j+1-i)
				continue
			}
			return markers + (ws+pending)/2
		}
	}
	return markers + ws/2
}

// thematicBreakRange finds the positions i from which line[i:] is a thematic break
// when line[i] is mark: three or more of the same "-" or "*", with only spaces or
// tabs between and after them. They are tbFrom..tbTo; tbTo is -1 when there are none.
// Computing the range once keeps the scan linear on long lines of markers.
func thematicBreakRange(line []byte) (mark byte, tbFrom, tbTo int) {
	tbTo = -1
	n := 0
	for i := len(line) - 1; i >= 0; i-- {
		c := line[i]
		if isSpaceOrTab(c) {
			continue
		}
		if mark == 0 {
			if c != '-' && c != '*' {
				return 0, 0, -1
			}
			mark = c
		}
		if c != mark {
			break
		}
		if n++; n == 3 {
			tbTo = i
		}
		tbFrom = i
	}
	return mark, tbFrom, tbTo
}

// skipMarkerSpace steps over the one space or tab that belongs to the container
// marker ending at i, so it is not counted as indent.
func skipMarkerSpace(line []byte, i, col int) (int, int) {
	if i < len(line) && isSpaceOrTab(line[i]) {
		if line[i] == '\t' {
			return i + 1, col + 4 - col%4
		}
		return i + 1, col + 1
	}
	return i, col
}

func isSpaceOrTab(b byte) bool { return b == ' ' || b == '\t' }

func isBulletMarker(b byte) bool {
	return b == '-' || b == '+' || b == '*'
}
