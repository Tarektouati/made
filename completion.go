package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Tarektouati/made/internal/completion"
)

func completionCommand(fs *flag.FlagSet, file string, stdout, stderr io.Writer) int {
	if file != "" {
		fmt.Fprintln(stderr, "made: completion does not take -f")
		return 2
	}
	if f := firstSetFlag(fs, "f", "file"); f != "" {
		fmt.Fprintf(stderr, "made: completion does not take %s\n", f)
		return 2
	}
	var script []byte
	ok := fs.NArg() == 2
	if ok {
		script, ok = completion.Script(fs.Arg(1))
	}
	if !ok {
		fmt.Fprintf(stderr, "made: completion takes one of: %s\n", strings.Join(completion.Shells, ", "))
		return 2
	}
	_, _ = stdout.Write(script)
	return 0
}
