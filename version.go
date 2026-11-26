package main

import (
	"runtime/debug"
)

// resolveVersion reports the build version: the ldflags-injected value if present,
// otherwise the module version from the build info (e.g. a `go install …@v1.2.3`),
// falling back to "dev" for local builds.
func resolveVersion(buildVersion string) string {
	if buildVersion != "" {
		return normalizeVersion(buildVersion)
	}
	if info, ok := debug.ReadBuildInfo(); ok &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		return normalizeVersion(info.Main.Version)
	}
	return "dev"
}

// normalizeVersion gives ldflags and build-info versions one format: a leading "v"
// before a numeric version (goreleaser strips it from .Version; Go module versions keep it).
func normalizeVersion(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}
