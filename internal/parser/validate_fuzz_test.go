package parser

import (
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// fuzzLimit is a small nesting limit, so short fuzz inputs can go over it.
const fuzzLimit = 6

// astDepth returns the deepest nesting of blockquotes and list items in goldmark's
// parse of source.
func astDepth(source []byte) int {
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))
	maxDepth := 0
	var walk func(n ast.Node, d int)
	walk = func(n ast.Node, d int) {
		switch n.Kind() {
		case ast.KindBlockquote, ast.KindListItem:
			d++
		}
		if d > maxDepth {
			maxDepth = d
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c, d)
		}
	}
	walk(doc, 0)
	return maxDepth
}

// acceptedSeeds are fuzz seeds the scan accepts at fuzzLimit, so the fuzz test
// compares them with goldmark. They cover lines the scan counts below their marker
// count.
var acceptedSeeds = []string{
	"- - - - - - - -\n",
	"- - - - - - * * *\n",
	"> > > > > > - - -\n",
	"-\t-\t-\t-\t-\t-\t-\n",
	"- - - x\n      - - - -\n",
}

func TestAcceptedSeedsPassScan(t *testing.T) {
	for _, seed := range acceptedSeeds {
		if err := validateNestingLimit([]byte(seed), fuzzLimit); err != nil {
			t.Errorf("validateNestingLimit(%q) = %v, want nil", seed, err)
		}
	}
}

// FuzzValidateNesting checks the nesting scan never accepts a document that goldmark
// nests deeper than the limit. A difference means lineNestingDepth undercounts some
// line. The seeds include inputs that got past earlier versions of the scan.
func FuzzValidateNesting(f *testing.F) {
	for _, seed := range acceptedSeeds {
		f.Add([]byte(seed))
	}
	for _, seed := range []string{
		"- - - - - - - * * *\n",
		"> > > > > > > x\n",
		"- - - - - - - x\n",
		"1. 2. 3) - > + x\n",
		"\t```\n> > > > > > > x\n",
		"```a`b\n> > > > > > > x\n",
		"- a\n\n  ```\n> > > > > > > x\n",
		"<div>\n```\n\n> > > > > > > x\n",
		"- <div>\n  ```\n\n  > > > > > > > x\n",
		"<pre>\n\n```\n</pre>\n> > > > > > > x\n",
		"- - x\n\n    > > > > > > > x\n",
		"- - - x\n      - - - - x\n",
		"- a\n\n  ```\n    ```\n  > > > > > > > x\n",
		"1. a\n\n   ```\n      ```\n   > > > > > > > x\n",
		"*\n  >>>>>*",
		" ```\n0\n```\n>>>>>>*",
		"0) ```\n   ```\n   >>>>>*",
		"  ```\n     ```\n```\n> > > > > > > x\n",
		"~~~a`b\n> > > > > > > x\n~~~\n",
		"### t\nSome text.\n```bash\necho > out\n```\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 4096 {
			return
		}
		if validateNestingLimit(src, fuzzLimit) != nil {
			return
		}
		if d := astDepth(src); d > fuzzLimit {
			t.Fatalf("scan accepted %q, goldmark nests it %d deep (limit %d)", src, d, fuzzLimit)
		}
	})
}
