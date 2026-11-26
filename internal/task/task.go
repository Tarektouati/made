// Package task defines the runnable unit shared by the parser and the runner.
package task

// Task is a single runnable unit parsed from the Markdown source.
type Task struct {
	Name        string
	Description string
	Group       string // nearest enclosing H2 heading, or empty outside a section
	GroupLine   int    // H2 line, so repeated section titles remain distinct
	Lang        string // fence language (first word of the info string, e.g. "bash")
	Script      string // code block body
	Line        int    // 1-based line of the heading
}
