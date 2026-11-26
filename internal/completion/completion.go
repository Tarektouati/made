// Package completion holds the shell completion scripts printed by
// `made completion <shell>`.
//
// The scripts are hand-written and embedded. They get task names from the hidden
// `made --list-names` mode, which prints "name<TAB>description" lines, and forward any
// -f/--file value already on the command line so completion reads the same file a run
// would.
package completion

import "embed"

//go:embed made.bash made.zsh made.fish made.powershell
var scripts embed.FS

// Shells lists the supported shells, in the order shown in messages.
var Shells = []string{"bash", "zsh", "fish", "powershell"}

// Script returns the completion script for shell, or false if the shell is not
// supported.
func Script(shell string) ([]byte, bool) {
	for _, s := range Shells {
		if s == shell {
			b, err := scripts.ReadFile("made." + s)
			return b, err == nil
		}
	}
	return nil, false
}
