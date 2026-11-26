// Command made is a Markdown-driven task runner.
//
// Tasks are H3 headings in a Markdown file (the nearest Madefile.md by default), each
// followed by a fenced code block. `made <task> [args...]` runs that block with args
// as its positional parameters; `made` with no task lists them. `made init` writes a
// starter Madefile.md in the current directory, `made completion <shell>` prints a
// bash, zsh or fish completion script, and `made self-update` replaces made with the
// latest release.
package main

import (
	"os"
)

// version is set at release time via -ldflags "-X main.version=<version>".
var version = ""

func main() {
	os.Exit(newApp(version).run(os.Args[1:], os.Stdout, os.Stderr))
}
