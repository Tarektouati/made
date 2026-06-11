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
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// taskHeadingLevel is the heading level that introduces a task.
const taskHeadingLevel = 3

// Task is a single runnable unit parsed from the Markdown source.
type Task struct {
	Name        string
	Description string
	Lang        string // fence info string (e.g. "bash"); captured but unused in v1
	Script      string // code block body
}

// Parse extracts tasks from Markdown source, preserving document order.
//
// Each H3 heading starts a task; the first fenced code block after it provides the
// script and completes the task. Paragraph text between the heading and that code
// block becomes the description. Headings of any other level end the current task's
// scope. A heading with no following code block is skipped.
func Parse(source []byte) ([]Task, error) {
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
				cur.Lang = string(node.Language(source))
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

// codeText joins the raw lines of a fenced code block.
func codeText(n *ast.FencedCodeBlock, source []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
	return b.String()
}
