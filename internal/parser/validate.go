package parser

import (
	"bytes"
	"fmt"
)

// maxNestingDepth limits blockquote/list marker depth before goldmark runs.
const maxNestingDepth = 256

type fenceState struct {
	char   byte
	length int
}

// NestingError reports container nesting (blockquotes, lists) deeper than the parser
// accepts. Line is the 1-based line where the limit is first exceeded.
type NestingError struct{ Line int }

func (e *NestingError) Error() string {
	return fmt.Sprintf("nesting too deep (max %d)", maxNestingDepth)
}

func validateNesting(source []byte) error {
	var maxSeen int
	var fence *fenceState
	for n := 1; len(source) > 0; n++ {
		var line []byte
		line, source = nextLine(source)
		if fence == nil {
			if fs, ok := fenceOpen(line); ok {
				fence = fs
				continue
			}
		} else {
			if fenceClose(line, fence) {
				fence = nil
			}
			continue
		}
		d := lineNestingDepth(line)
		if d > maxSeen {
			maxSeen = d
		}
		if maxSeen > maxNestingDepth {
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

func fenceOpen(line []byte) (*fenceState, bool) {
	i := skipIndentUpto3(line)
	if i >= len(line) {
		return nil, false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return nil, false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	ln := j - i
	if ln < 3 {
		return nil, false
	}
	return &fenceState{char: c, length: ln}, true
}

func fenceClose(line []byte, fence *fenceState) bool {
	i := skipIndentUpto3(line)
	if i >= len(line) || line[i] != fence.char {
		return false
	}
	j := i
	for j < len(line) && line[j] == fence.char {
		j++
	}
	ln := j - i
	if ln < fence.length {
		return false
	}
	return isBlank(line[j:])
}

func isBlank(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

func skipIndentUpto3(line []byte) int {
	i := 0
	for i < len(line) && i < 3 && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i
}

func lineNestingDepth(line []byte) int {
	d := containerMarkerRun(line)
	if id := listItemIndentDepth(line); id > d {
		d = id
	}
	return d
}

// containerMarkerRun counts consecutive blockquote, bullet, and ordered list markers
// on one line, including mixed sequences like "> - > -".
func containerMarkerRun(line []byte) int {
	i := skipIndentUpto3(line)
	depth := 0
	for {
		switch {
		case tryBlockquoteMarker(line, &i):
			depth++
		case tryBulletMarker(line, &i):
			depth++
		case tryOrderedMarker(line, &i):
			depth++
		default:
			return depth
		}
	}
}

func tryBlockquoteMarker(line []byte, i *int) bool {
	if *i >= len(line) || line[*i] != '>' {
		return false
	}
	*i++
	if *i < len(line) && (line[*i] == ' ' || line[*i] == '\t') {
		*i++
	}
	return true
}

func tryBulletMarker(line []byte, i *int) bool {
	if *i+2 > len(line) || !isBulletMarker(line[*i]) || line[*i+1] != ' ' {
		return false
	}
	*i += 2
	return true
}

func tryOrderedMarker(line []byte, i *int) bool {
	start := *i
	j := start
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	if j == start || j+1 >= len(line) {
		return false
	}
	if (line[j] == '.' || line[j] == ')') && line[j+1] == ' ' {
		*i = j + 2
		return true
	}
	return false
}

func isBulletMarker(b byte) bool {
	return b == '-' || b == '+' || b == '*'
}

func listItemIndentDepth(line []byte) int {
	indent := leadingIndent(line)
	off := skipIndentPrefix(line)
	if off >= len(line) || !isListItemAt(line[off:]) {
		return 0
	}
	depth := indent/4 + 1
	if depth < 1 {
		return 1
	}
	return depth
}

func leadingIndent(line []byte) int {
	w := 0
	for _, c := range line {
		switch c {
		case ' ':
			w++
		case '\t':
			w += 4 - w%4
		default:
			return w
		}
	}
	return w
}

func skipIndentPrefix(line []byte) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i
}

func isListItemAt(s []byte) bool {
	if len(s) < 2 {
		return false
	}
	if isBulletMarker(s[0]) && s[1] == ' ' {
		return true
	}
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j > 0 && j+1 < len(s) && (s[j] == '.' || s[j] == ')') && s[j+1] == ' ' {
		return true
	}
	return false
}
