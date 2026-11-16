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
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
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
				cur = &Task{Name: strings.TrimSpace(headingText(node, source))}
				desc = nil
			} else {
				cur = nil
				desc = nil
			}
		case *ast.Paragraph:
			if cur != nil && cur.Script == "" {
				if t := strings.TrimSpace(headingText(node, source)); t != "" {
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

// headingText concatenates the raw text of a node's inline children. It works for
// any node whose meaningful content is plain text segments (headings, paragraphs).
func headingText(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
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
