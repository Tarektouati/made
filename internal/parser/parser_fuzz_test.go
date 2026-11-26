package parser

import (
	"bytes"
	"testing"
)

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"plain text\n",
		"### task\n```sh\necho hello\n```\n",
		"### empty\n```\n```",
		"###\n```sh\necho unnamed\n```\n",
		"### first\n```sh\ntrue\n```\n### second\n~~~python\nprint(1)\n~~~\n",
		"### *task* &amp; `code`\nA [description](https://example.invalid).\n```bash {.class}\necho ok\n```\n",
		"\xef\xbb\xbf### task\r\n```sh\r\necho windows\r\n```\r\n",
		"### task\r```sh\recho classic\r```\r",
		"### task\n```sh\necho unclosed",
		"> ### quoted\n> ```sh\n> true\n> ```\n",
		"- ### listed\n  ```sh\n  true\n  ```\n",
		"<div>\n### hidden\n```sh\ntrue\n```\n</div>\n",
		"### \x00\xff\n```sh\n\x00\xfe\n```\n",
	} {
		f.Add([]byte(seed))
	}
	for _, seed := range acceptedSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		tasks, err := Parse(source)
		if err != nil {
			return // Rejecting excessive Markdown nesting is expected.
		}
		// CR-only files use CR as a line ending; mixed files count LF.
		separator := []byte("\n")
		if !bytes.Contains(source, separator) {
			separator = []byte("\r")
		}
		lines := bytes.Count(source, separator) + 1
		previous := 0
		for _, task := range tasks {
			if task.Line < 1 || task.Line > lines {
				t.Fatalf("task %q has line %d outside 1..%d", task.Name, task.Line, lines)
			}
			if task.Line <= previous {
				t.Fatalf("task %q has line %d after line %d", task.Name, task.Line, previous)
			}
			previous = task.Line
		}
	})
}
