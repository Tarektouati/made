package selfupdate

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The tests in this file read .goreleaser.yaml and install.sh, which publish and
// install the releases made self-update downloads, so that a change to the asset
// names, the tag resolution or the platforms in one of them fails here.

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	// #nosec G304 -- Reads fixed repository files to compare the release and installer contracts.
	b, err := os.ReadFile("../../" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lfText returns src with CRLF line endings turned into LF. Line endings belong to
// the checkout (Git on Windows may write CRLF), not to the committed file, so every
// line-based parse below starts from this.
func lfText(src string) string {
	return strings.ReplaceAll(src, "\r\n", "\n")
}

// yamlSection returns the lines of the top-level key in a YAML file, without the key.
func yamlSection(t *testing.T, src, key string) []string {
	t.Helper()
	var out []string
	in := false
	for line := range strings.SplitSeq(src, "\n") {
		if strings.TrimSpace(line) != "" && line[0] != ' ' && line[0] != '#' {
			in = strings.HasPrefix(line, key+":")
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	if out == nil {
		t.Fatalf(".goreleaser.yaml has no %s section", key)
	}
	return out
}

// yamlValue returns the scalar value of the first "key:" line in lines, unquoted.
func yamlValue(t *testing.T, lines []string, key string) string {
	t.Helper()
	for _, line := range lines {
		k, v, ok := strings.Cut(strings.TrimLeft(strings.TrimSpace(line), "- "), ":")
		if ok && k == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	t.Fatalf("no %s in %q", key, lines)
	return ""
}

// yamlList returns the "- item" lines that follow the "key:" line in lines.
func yamlList(t *testing.T, lines []string, key string) []string {
	t.Helper()
	var out []string
	indent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		n := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent < 0 && trimmed == key+":":
			indent = n
		case indent >= 0 && n > indent && strings.HasPrefix(trimmed, "- "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		case indent >= 0:
			if out == nil {
				t.Fatalf("%s: list is empty", key)
			}
			return out
		}
	}
	if out == nil {
		t.Fatalf("no %s list in %q", key, lines)
	}
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return slices.Compact(s)
}

// eolVariants returns src as checked out, with CRLF line endings (as a Windows
// checkout may have them), and with a blank line opening each top-level section, so
// the parsing below is shown not to depend on either.
func eolVariants(src string) map[string]string {
	lf := lfText(src)
	blank := regexp.MustCompile(`(?m)^(\w+:)\n`).ReplaceAllString(lf, "$1\n\n")
	return map[string]string{
		"as checked out": src,
		"crlf":           strings.ReplaceAll(lf, "\n", "\r\n"),
		"blank lines":    blank,
		"crlf and blank": strings.ReplaceAll(blank, "\n", "\r\n"),
	}
}

func TestGoreleaserMatchesAssetName(t *testing.T) {
	for name, src := range eolVariants(readRepoFile(t, ".goreleaser.yaml")) {
		t.Run(name, func(t *testing.T) { checkGoreleaser(t, src) })
	}
}

func checkGoreleaser(t *testing.T, src string) {
	src = lfText(src)
	project := yamlValue(t, strings.Split(src, "\n"), "project_name")
	builds := yamlSection(t, src, "builds")
	archives := yamlSection(t, src, "archives")
	checksum := yamlSection(t, src, "checksum")

	if got := yamlValue(t, checksum, "name_template"); got != ChecksumsName {
		t.Errorf("checksum name_template = %q, want %q", got, ChecksumsName)
	}
	for _, line := range archives {
		k, v, _ := strings.Cut(strings.TrimSpace(line), ":")
		if (k == "format" || k == "formats") && !strings.Contains(v, "tar.gz") {
			t.Errorf("archives set %s, but made self-update and install.sh expect tar.gz", strings.TrimSpace(line))
		}
		if strings.HasPrefix(strings.TrimSpace(line), "format_overrides") {
			t.Error("archives have format_overrides; made self-update expects tar.gz everywhere")
		}
	}
	if ld := strings.Join(builds, "\n"); !strings.Contains(ld, "-X main.version={{ .Version }}") {
		t.Error("builds no longer set -X main.version={{ .Version }}, which install-method detection relies on (Install.Stamped)")
	}

	goos := yamlList(t, builds, "goos")
	goarch := yamlList(t, builds, "goarch")
	tmpl := yamlValue(t, archives, "name_template")
	if len(platforms) != len(goos)*len(goarch) {
		t.Errorf("AssetName knows %d platforms, .goreleaser.yaml builds %d (goos %v, goarch %v)", len(platforms), len(goos)*len(goarch), goos, goarch)
	}
	for _, o := range goos {
		for _, a := range goarch {
			want := strings.NewReplacer(
				"{{ .ProjectName }}", project,
				"{{ .Version }}", "1.2.3",
				"{{ .Os }}", o,
				"{{ .Arch }}", a,
			).Replace(tmpl) + ".tar.gz"
			if got, ok := AssetName("v1.2.3", o, a); !ok || got != want {
				t.Errorf("AssetName(v1.2.3, %s, %s) = (%q, %v), .goreleaser.yaml publishes %q", o, a, got, ok, want)
			}
		}
	}
}

func TestInstallScriptMatchesAssetName(t *testing.T) {
	for name, src := range eolVariants(readRepoFile(t, "install.sh")) {
		t.Run(name, func(t *testing.T) { checkInstallScript(t, src) })
	}
}

func checkInstallScript(t *testing.T, src string) {
	src = lfText(src)
	assign := func(name string) string {
		t.Helper()
		m := regexp.MustCompile(`(?m)^[ \t]*` + name + `="?([^"\n]*)"?$`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("install.sh no longer assigns %s", name)
		}
		return m[1]
	}

	if got := assign("REPO"); got != Repo {
		t.Errorf("install.sh REPO = %q, want %q", got, Repo)
	}
	latest := regexp.MustCompile(`"(https://[^"]*/releases/latest)"`).FindStringSubmatch(src)
	if latest == nil {
		t.Fatal("install.sh no longer resolves the tag from a releases/latest URL")
	}
	if got, want := strings.ReplaceAll(latest[1], "$REPO", Repo), DefaultBaseURL+"/"+Repo+"/releases/latest"; got != want {
		t.Errorf("install.sh resolves the latest tag from %q, made self-update from %q", got, want)
	}
	if !strings.Contains(src, `tag="${latest_url##*/}"`) {
		t.Error("install.sh no longer takes the tag from the last path segment of the redirect")
	}

	expand := strings.NewReplacer("$REPO", Repo, "$tag", "v1.2.3", "${version}", "1.2.3", "${os}", "linux", "${arch}", "arm64")
	if got, want := expand.Replace(assign("base_url"))+"/x", ReleaseURL(DefaultBaseURL, "v1.2.3", "x"); got != want {
		t.Errorf("install.sh downloads from %q, made self-update from %q", got, want)
	}
	if want, _ := AssetName("v1.2.3", "linux", "arm64"); expand.Replace(assign("archive")) != want {
		t.Errorf("install.sh archive = %q, want %q", assign("archive"), want)
	}
	if !strings.Contains(src, `"$base_url/checksums.txt"`) || ChecksumsName != "checksums.txt" {
		t.Error("install.sh and made self-update read different checksum files")
	}

	var oses, arches []string
	for _, m := range regexp.MustCompile(`\)\s*os=(\w+)\s*;;`).FindAllStringSubmatch(src, -1) {
		oses = append(oses, m[1])
	}
	for _, m := range regexp.MustCompile(`\)\s*arch=(\w+)\s*;;`).FindAllStringSubmatch(src, -1) {
		arches = append(arches, m[1])
	}
	oses, arches = sorted(oses), sorted(arches)
	if len(oses) == 0 || len(arches) == 0 {
		t.Fatal("cannot find the os=/arch= mappings in install.sh")
	}
	if len(platforms) != len(oses)*len(arches) {
		t.Errorf("AssetName knows %d platforms, install.sh maps %v x %v", len(platforms), oses, arches)
	}
	for _, o := range oses {
		for _, a := range arches {
			if _, ok := AssetName("v1.2.3", o, a); !ok {
				t.Errorf("install.sh installs %s/%s, which AssetName does not know", o, a)
			}
		}
	}
}
