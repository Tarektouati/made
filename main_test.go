package main

import "testing"

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
