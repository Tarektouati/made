package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
		line         string
	}{
		{name: "empty and comments", source: "\n # comment\r\n\t\n"},
		{name: "literal values", source: "PLAIN=hello world\nEMPTY=\nA_B2=x=y # literal\n", want: []string{"PLAIN=hello world", "EMPTY=", "A_B2=x=y # literal"}},
		{name: "export BOM and CRLF", source: "\ufeffexport A = one\r\nexport\tB=two\r\nexport=value\n", want: []string{"A=one", "B=two", "export=value"}},
		{name: "quotes and whitespace", source: " A = ' spaces ' \nB=\"single ' quote\"\nC='double \" quote'\n", want: []string{"A= spaces ", "B=single ' quote", "C=double \" quote"}},
		{name: "no expansions", source: "A=\"$HOME ${OTHER} $(false) `false` \\n\"\nB=can't\n", want: []string{"A=$HOME ${OTHER} $(false) `false` \\n", "B=can't"}},
		{name: "duplicates remain ordered", source: "A=one\nA=two\n", want: []string{"A=one", "A=two"}},
		{name: "missing equals", source: "secret-value", line: "line 1:"},
		{name: "empty name", source: "=secret-value", line: "line 1:"},
		{name: "leading digit", source: "3A=secret-value", line: "line 1:"},
		{name: "dash in name", source: "A-B=secret-value", line: "line 1:"},
		{name: "unicode name", source: "Ä=secret-value", line: "line 1:"},
		{name: "space in name", source: "A B=secret-value", line: "line 1:"},
		{name: "unfinished quote", source: "\nA='secret-value", line: "line 2:"},
		{name: "multiline quote", source: "A=\"secret-value\ncontinued\"", line: "line 1:"},
		{name: "text after quote", source: "A='secret-value' # comment", line: "line 1:"},
		{name: "embedded matching quote", source: "A='secret' value'", line: "line 1:"},
		{name: "NUL", source: "A=secret-value\x00", line: "line 1:"},
		{name: "invalid UTF-8", source: "A=secret-value\xff", line: "line 1:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEnvFile(tc.source)
			if tc.line != "" {
				if err == nil || !strings.Contains(err.Error(), tc.line) {
					t.Fatalf("parse error = %v, want %s", err, tc.line)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Errorf("parse error exposed a value: %v", err)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("parse = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestReadEnvFileBoundsAndType(t *testing.T) {
	dir := t.TempDir()
	if _, err := readEnvFile(dir); !errors.Is(err, errNotRegular) {
		t.Errorf("directory = %v, want not regular", err)
	}
	path := filepath.Join(dir, "vars.env")
	if _, err := readEnvFile(path); !os.IsNotExist(err) {
		t.Errorf("missing file = %v, want not found", err)
	}
	data := "#" + strings.Repeat("x", maxEnvFileBytes-2) + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvFile(path); err != nil {
		t.Errorf("file at limit: %v", err)
	}
	if err := os.WriteFile(path, []byte(data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvFile(path); !errors.Is(err, errTooLarge) {
		t.Errorf("file over limit = %v, want too large", err)
	}
}
