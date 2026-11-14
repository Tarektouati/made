package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveVersionUsesLdflagsVar(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })

	version = "v9.9.9"
	if got := resolveVersion(); got != "v9.9.9" {
		t.Errorf("resolveVersion() = %q, want %q", got, "v9.9.9")
	}

	// With no injected version, the resolver still returns a non-empty fallback.
	version = ""
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion() = empty, want a non-empty fallback")
	}
}

func TestFindUp(t *testing.T) {
	root := filepath.FromSlash("/r")
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	readme := func(dir string) string { return filepath.Join(dir, defaultFile) }

	tests := []struct {
		name   string
		files  []string // paths for which isFile reports true
		start  string
		want   string
		wantOK bool
	}{
		{"in start", []string{readme(at("a"))}, at("a"), at("a"), true},
		{"in ancestor", []string{readme(root)}, at("a", "b"), root, true},
		{"nearest wins", []string{readme(root), readme(at("a"))}, at("a", "b"), at("a"), true},
		{"not found", nil, at("a", "b"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, f := range tt.files {
				set[f] = true
			}
			got, ok := findUp(tt.start, func(p string) bool { return set[p] })
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("findUp(%q) = (%q, %v), want (%q, %v)", tt.start, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIsRegularFileSkipsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, defaultFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if isRegularFile(filepath.Join(dir, defaultFile)) {
		t.Error("isRegularFile(directory) = true, want false")
	}
}

func TestResolveFileExplicitIsUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := resolveFile("some/tasks.md")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got != "some/tasks.md" {
		t.Errorf("resolveFile = %q, want %q", got, "some/tasks.md")
	}
}

func TestResolveFileInCwdKeepsShortName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, defaultFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := resolveFile("")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if got != defaultFile {
		t.Errorf("resolveFile = %q, want %q", got, defaultFile)
	}
}
