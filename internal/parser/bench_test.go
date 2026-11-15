package parser

import (
	"fmt"
	"strings"
	"testing"
)

const smallReadme = `# Tasks

Some intro text.

### build
Compiles all packages.
` + "```bash\ngo build ./...\n```" + `

### test
Runs the test suite.
` + "```bash\ngo test ./...\n```\n"

// genReadme returns a README with n tasks, each with a description and a script.
func genReadme(n int) []byte {
	var b strings.Builder
	b.WriteString("# Tasks\n\n")
	for i := range n {
		fmt.Fprintf(&b, "### task-%d\nDescription of task %d.\n```bash\necho task %d\n```\n\n", i, i, i)
	}
	return []byte(b.String())
}

func BenchmarkParse(b *testing.B) {
	for _, tc := range []struct {
		name   string
		source []byte
	}{
		{"small", []byte(smallReadme)},
		{"tasks=1000", genReadme(1000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := Parse(tc.source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
