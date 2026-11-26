package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tarektouati/made/internal/runner"
)

// printPrepared describes the command without starting it or exposing its
// environment. Quoted fields use Go escapes; script line breaks stay readable.
func printPrepared(w io.Writer, file string, p *runner.Prepared) error {
	absFile, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(p.Dir)
	if err != nil {
		return err
	}
	quote := func(s string) string { return escapeDisplay(strconv.Quote(s)) }
	fmt.Fprintf(w, "Task file: %s\nWorking directory: %s\nInterpreter: %s\n",
		quote(absFile), quote(absDir), quote(p.Path))
	args := make([]string, len(p.Args))
	for i, arg := range p.Args {
		args[i] = quote(arg)
	}
	fmt.Fprintf(w, "Arguments: %s\nScript:\n", strings.Join(args, " "))
	lines := strings.Split(p.Script, "\n")
	for i, line := range lines {
		lines[i] = escapeDisplay(line)
	}
	fmt.Fprint(w, strings.Join(lines, "\n"))
	if !strings.HasSuffix(p.Script, "\n") {
		fmt.Fprintln(w)
	}
	return nil
}
