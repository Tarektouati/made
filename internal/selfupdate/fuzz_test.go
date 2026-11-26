package selfupdate

import (
	"bytes"
	"strings"
	"testing"
	"unicode"
)

func FuzzNormalizeTag(f *testing.F) {
	for _, seed := range normalizeTagCases {
		f.Add(seed.in)
	}
	for _, seed := range []string{"latest", "v1\x00", "v1\xff", "v1.2.3\n", "v١.2.3", "v1.2.3+Ü", "V0.7.0-rc.1+build-2"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		tag, err := NormalizeTag(input)
		if err != nil {
			if tag != "" {
				t.Fatalf("rejected tag returned %q", tag)
			}
			return
		}
		if len(tag) < 2 || tag[0] != 'v' || tag[1] < '0' || tag[1] > '9' {
			t.Fatalf("normalized tag has invalid prefix: %q", tag)
		}
		const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.+-"
		for _, c := range []byte(tag[1:]) {
			if !strings.ContainsRune(allowed, rune(c)) {
				t.Fatalf("normalized tag contains invalid byte %#x: %q", c, tag)
			}
		}
		if again, err := NormalizeTag(tag); err != nil || again != tag {
			t.Fatalf("NormalizeTag(%q) = (%q, %v), want the same tag", tag, again, err)
		}
	})
}

func FuzzChecksumFor(f *testing.F) {
	const asset = "made_0.7.0_linux_amd64.tar.gz"
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, seed := range []struct{ sums, name string }{
		{"", asset},
		{strings.ToUpper(digest) + "  " + asset + "\n", asset},
		{digest + "\t" + asset + "\r\n", asset},
		{"AAAA  other.tar.gz\nBBBB  " + asset + "\nCCCC  " + asset + "\n", asset},
		{"\nchecksum-only\n\t\n", asset},
		{digest + "  " + asset + ".bak\n", asset},
		{digest + "  " + asset + " extra-field\n", asset},
		{"\xff\x00  asset\n", "asset"},
		{"AAAA  asset\n", ""},
	} {
		f.Add([]byte(seed.sums), seed.name)
	}
	f.Fuzz(func(t *testing.T, sums []byte, name string) {
		got, err := checksumFor(sums, name)
		if err != nil {
			if got != "" {
				t.Fatalf("missing checksum returned %q", got)
			}
		} else {
			// This helper extracts a field, not a validated digest: Run compares
			// it with the archive's SHA-256 before installing anything.
			if got == "" || got != strings.ToLower(got) || strings.ContainsFunc(got, unicode.IsSpace) {
				t.Fatalf("invalid normalized checksum field %q", got)
			}
			row := []byte(got + "  " + name + "\r\n")
			if again, err := checksumFor(row, name); err != nil || again != got {
				t.Fatalf("checksum field did not round-trip: (%q, %v), want %q", again, err, got)
			}
		}

		// A known first matching row must win even when arbitrary input follows
		// it, including another row for the same asset.
		prefixed := bytes.Join([][]byte{[]byte(strings.ToUpper(digest) + "  " + asset), sums}, []byte("\n"))
		if first, err := checksumFor(prefixed, asset); err != nil || first != digest {
			t.Fatalf("first checksum = (%q, %v), want %q", first, err, digest)
		}
	})
}
