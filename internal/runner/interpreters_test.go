package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tarektouati/made/internal/task"
)

func TestRunAdditionalInterpreters(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--flag", "two words", "", "it's", `"quoted"`, `\tail\`, "line\nbreak", `$(throw 'argument was evaluated')`},
	} {
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		want := string(encoded)
		powershell := "$expected = '" + strings.ReplaceAll(want, "'", "''") + "' | ConvertFrom-Json\n" +
			"if ($args.Count -ne $expected.Count) { exit 1 }\n" +
			"for ($i = 0; $i -lt $args.Count; $i++) { if ($args[$i] -cne $expected[$i]) { exit 2 } }"
		for _, tc := range []struct{ lang, bin, script string }{
			{"pwsh", "pwsh", powershell},
			{"powershell", "pwsh", powershell},
			{"deno", "deno", "if (JSON.stringify(Deno.args) !== JSON.stringify(" + want + ")) Deno.exit(1)"},
			{"ts", "deno", "const expected: string[] = " + want + "; if (JSON.stringify(Deno.args) !== JSON.stringify(expected)) Deno.exit(1)"},
			{"bun", "bun", "if (JSON.stringify(process.argv.slice(1)) !== JSON.stringify(" + want + ")) process.exit(1)"},
		} {
			t.Run(tc.lang+"/"+want, func(t *testing.T) {
				requireCmd(t, tc.bin)
				if err := Run(t.Context(), nil, task.Task{Name: "args", Lang: tc.lang, Script: tc.script}, "", args); err != nil {
					t.Fatalf("Run: %v, want literal arguments %q", err, args)
				}
			})
		}
	}
}

func TestAdditionalInterpretersExitCodes(t *testing.T) {
	for _, tc := range []struct{ lang, bin, script string }{
		{"pwsh", "pwsh", "-1; exit 37"},
		{"powershell", "pwsh", "-1; exit 37"},
		{"deno", "deno", "-Deno.exit(37)"},
		{"ts", "deno", "-Deno.exit(37)"},
		{"bun", "bun", "-process.exit(37)"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			requireCmd(t, tc.bin)
			err := Run(context.Background(), nil, task.Task{Name: "exit", Lang: tc.lang, Script: tc.script}, "", nil)
			wantExitCode(t, err, 37)
		})
	}
}
