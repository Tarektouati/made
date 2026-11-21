// Package task defines the runnable unit shared by the parser and the runner.
package task

// Task is a single runnable unit parsed from the Markdown source.
type Task struct {
	Name        string
	Description string
	Lang        string // fence language (first word of the info string, e.g. "bash")
	Script      string // code block body
	Line        int    // 1-based line of the heading
}
