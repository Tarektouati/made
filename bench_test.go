package main

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkFindUp measures findMadefile walking up a deep tree: the Madefile sits at
// the top, so every level below it is read and rejected first.
func BenchmarkFindUp(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Madefile.md"), []byte("# Tasks\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	deep := root
	for i := range 40 {
		deep = filepath.Join(deep, "level", string(rune('a'+i%26)))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, _, _, _, ok := findMadefile(deep); !ok {
			b.Fatal("Madefile not found")
		}
	}
}
