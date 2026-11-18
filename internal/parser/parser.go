// Package parser turns a Markdown document into a list of runnable tasks.
//
// A task is an H3 heading whose name is the heading text, optionally followed by
// descriptive prose, and a fenced code block holding the script to run:
//
//	### say-hello
//	Prints a friendly greeting.
//	```bash
//	echo hello
//	```
package parser

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// taskHeadingLevel is the heading level that introduces a task.
const taskHeadingLevel = 3

// utf8BOM is the byte order mark some editors (e.g. Windows Notepad) write at the
// start of UTF-8 files.
var utf8BOM = []byte("\xef\xbb\xbf")

// Task is a single runnable unit parsed from the Markdown source.
type Task struct {
	Name        string
	Description string
	Lang        string // fence language (first word of the info string, e.g. "bash")
	Script      string // code block body
}

// Parse extracts tasks from Markdown source, preserving document order.
//
// Each H3 heading starts a task; the first fenced code block after it provides the
// script and completes the task. Paragraph text between the heading and that code
// block becomes the description. Headings of any other level end the current task's
// scope. A heading with no following code block is skipped. A leading UTF-8 byte
// order mark is ignored.
func Parse(source []byte) ([]Task, error) {
	source = bytes.TrimPrefix(source, utf8BOM)
	if err := validateNesting(source); err != nil {
		return nil, err
	}
	reader := text.NewReader(source)
	doc := goldmark.DefaultParser().Parse(reader)

	var tasks []Task
	var cur *Task     // task currently being assembled
	var desc []string // description paragraphs collected so far
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch node := n.(type) {
		case *ast.Heading:
			if node.Level == taskHeadingLevel {
				cur = &Task{Name: NormalizeSpace(headingText(node, source))}
				desc = nil
			} else {
				cur = nil
				desc = nil
			}
		case *ast.Paragraph:
			if cur != nil && cur.Script == "" {
				if t := NormalizeSpace(headingText(node, source)); t != "" {
					desc = append(desc, t)
				}
			}
		case *ast.FencedCodeBlock:
			if cur != nil && cur.Script == "" {
				cur.Lang = fenceLanguage(node, source)
				cur.Description = strings.Join(desc, "\n")
				cur.Script = codeText(node, source)
				tasks = append(tasks, *cur)
				cur = nil
				desc = nil
			}
		}
	}
	return tasks, nil
}

// Fence is a fenced code block found under a task heading.
type Fence struct {
	Line   int    // 1-based line of the opening fence
	Lang   string // as Task.Lang
	Script string // as Task.Script
}

// RawTask is an H3 heading with its source position and every top-level fenced code
// block between it and the next heading. It describes what Parse sees, for tools that
// report on a task file (made --check) rather than run it.
type RawTask struct {
	Line int    // 1-based line of the heading
	Raw  string // the heading's source text, whitespace-normalized
	Name string // the task name Parse gives this heading
	// Altered is set when the heading has inline markup (code span, emphasis, link,
	// raw HTML) whose text is left out of Name.
	Altered bool
	// Fences are the top-level fenced code blocks before the next heading, in order.
	// Parse runs only Fences[0]; a heading with none is not a task.
	Fences []Fence
}

// ParseDetailed walks source like Parse but keeps every H3 heading, including ones
// without a code block, with line positions and all of their fences. A leading UTF-8
// byte order mark is ignored and does not shift line numbers. Like Parse, it rejects
// over-deep nesting with a *NestingError before parsing.
func ParseDetailed(source []byte) ([]RawTask, error) {
	source = bytes.TrimPrefix(source, utf8BOM)
	if err := validateNesting(source); err != nil {
		return nil, err
	}
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))
	lineOf := func(n ast.Node) int {
		pos := n.Pos()
		if pos < 0 {
			return 0
		}
		return 1 + bytes.Count(source[:pos], []byte("\n"))
	}

	var tasks []RawTask
	cur := -1 // index in tasks of the heading in scope, or -1
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch node := n.(type) {
		case *ast.Heading:
			cur = -1
			if node.Level != taskHeadingLevel {
				continue
			}
			tasks = append(tasks, RawTask{
				Line:    lineOf(node),
				Raw:     NormalizeSpace(string(node.Lines().Value(source))),
				Name:    NormalizeSpace(headingText(node, source)),
				Altered: hasMarkup(node),
			})
			cur = len(tasks) - 1
		case *ast.FencedCodeBlock:
			if cur >= 0 {
				tasks[cur].Fences = append(tasks[cur].Fences, Fence{
					Line:   lineOf(node),
					Lang:   fenceLanguage(node, source),
					Script: codeText(node, source),
				})
			}
		}
	}
	return tasks, nil
}

// hasMarkup reports whether n has an inline child other than plain text, whose
// content headingText drops.
func hasMarkup(n ast.Node) bool {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.Text); !ok {
			return true
		}
	}
	return false
}

// headingText concatenates the text of a node's inline children, decoding backslash
// escapes and entity references. It works for any node whose meaningful content is
// plain text segments (headings, paragraphs).
func headingText(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			if t.IsRaw() {
				b.Write(t.Segment.Value(source))
			} else {
				b.WriteString(decodeText(t.Segment.Value(source)))
			}
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

// fenceLanguage returns the language word of a fence's info string. It ends at the
// first whitespace, '{', or ',', so "bash{.x}" and "bash,title" both yield "bash".
// An info string that starts with '{' or ',' falls back to its first
// whitespace-delimited word, so the runner rejects it instead of defaulting to bash.
func fenceLanguage(n *ast.FencedCodeBlock, source []byte) string {
	if n.Info == nil {
		return ""
	}
	info := n.Info.Segment.Value(source)
	cut := bytes.IndexFunc(info, func(r rune) bool {
		return unicode.IsSpace(r) || r == '{' || r == ','
	})
	if cut == 0 {
		cut = bytes.IndexFunc(info, unicode.IsSpace)
	}
	if cut < 0 {
		return string(info)
	}
	return string(info[:cut])
}

// maxEntityLen bounds how far decodeText scans for the ';' closing an entity reference.
const maxEntityLen = 32

// decodeText resolves backslash escapes and entity references in one pass, as
// CommonMark renders inline text: "\&amp;" stays "&amp;" and "&#92;*" stays "\*".
func decodeText(b []byte) string {
	var out strings.Builder
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '\\' && i+1 < len(b) && util.IsPunct(b[i+1]) {
			out.WriteByte(b[i+1])
			i++
			continue
		}
		if c == '&' {
			j := i + 1
			for j < len(b) && j-i < maxEntityLen && (util.IsAlphaNumeric(b[j]) || b[j] == '#') {
				j++
			}
			if j < len(b) && b[j] == ';' {
				if r, ok := resolveEntity(b[i+1 : j]); ok {
					out.WriteString(r)
					i = j
					continue
				}
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

// resolveEntity decodes the body of an entity reference (between '&' and ';'). Numeric
// references are always decimal or hex, as CommonMark specifies; goldmark's
// util.ResolveNumericReferences reads a leading zero as octal.
func resolveEntity(body []byte) (string, bool) {
	if len(body) < 2 || body[0] != '#' {
		e, ok := util.LookUpHTML5EntityByName(string(body))
		if !ok {
			return "", false
		}
		return string(e.Characters), true
	}
	digits, base, maxLen, isDigit := body[1:], 10, 7, util.IsNumeric
	if body[1] == 'x' || body[1] == 'X' {
		digits, base, maxLen, isDigit = body[2:], 16, 6, util.IsHexDecimal
	}
	if len(digits) == 0 || len(digits) > maxLen {
		return "", false
	}
	for _, d := range digits {
		if !isDigit(d) {
			return "", false
		}
	}
	v, err := strconv.ParseUint(string(digits), base, 32)
	if err != nil {
		return "", false
	}
	if v > unicode.MaxRune {
		return string(unicode.ReplacementChar), true
	}
	return string(util.ToValidRune(rune(v))), true
}

// NormalizeSpace drops control characters and invisible formatting characters (bidi
// controls, zero-width space, stray tags; see ReplaceInvisible), then trims s and
// collapses each run of whitespace (tabs, newlines, repeated spaces, line and
// paragraph separators) to a single space. Both kinds are stripped from listed names,
// so keeping them would make a listed name unrunnable, or let it display differently
// from what runs. Task names are stored normalized; apply it to a name typed by the
// user before comparing.
func NormalizeSpace(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = ReplaceInvisible(s, func(rune) string { return "" })
	return strings.Join(strings.Fields(s), " ")
}

// codeText joins the raw lines of a fenced code block, normalising CRLF to LF.
func codeText(n *ast.FencedCodeBlock, source []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		line := seg.Value(source)
		if bytes.HasSuffix(line, []byte("\r\n")) {
			b.Write(line[:len(line)-2])
			b.WriteByte('\n')
			continue
		}
		b.Write(line)
	}
	return b.String()
}
