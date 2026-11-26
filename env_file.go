package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxEnvFileBytes = 1 << 20 // 1 MiB

func resolveEnvFile(path, taskFile string, regular bool) (string, error) {
	if !filepath.IsAbs(path) && regular {
		path = filepath.Join(filepath.Dir(taskFile), path)
	}
	return filepath.Abs(path)
}

// readEnvFile accepts only regular files and bounds the read even if the file
// grows after Stat. On Unix the open is nonblocking, so a swapped-in FIFO cannot
// hang before the second type check. Symlinks to regular files are allowed.
func readEnvFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	if info.Size() > maxEnvFileBytes {
		return nil, fmt.Errorf("%w (%d bytes)", errTooLarge, maxEnvFileBytes)
	}
	f, err := openEnvFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	source, err := io.ReadAll(io.LimitReader(f, maxEnvFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(source) > maxEnvFileBytes {
		return nil, fmt.Errorf("%w (%d bytes)", errTooLarge, maxEnvFileBytes)
	}
	return parseEnvFile(string(source))
}

// parseEnvFile deliberately accepts a small literal format: NAME=VALUE, optional
// export and enclosing quotes, and whole-line comments. There is no expansion,
// escape decoding or multiline value syntax. Errors never include file values.
func parseEnvFile(source string) ([]string, error) {
	var env []string
	for i, raw := range strings.Split(strings.TrimPrefix(source, "\ufeff"), "\n") {
		fail := func(reason string) ([]string, error) {
			return nil, fmt.Errorf("line %d: %s", i+1, reason)
		}
		if !utf8.ValidString(raw) {
			return fail("invalid UTF-8")
		}
		if strings.ContainsRune(raw, 0) {
			return fail("NUL bytes are not allowed")
		}
		line := strings.Trim(raw, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimLeft(line[6:], " \t")
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.Trim(name, " \t")
		if !ok || !envName(name) {
			return fail("expected NAME=VALUE with an ASCII variable name")
		}
		value = strings.Trim(value, " \t")
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			quote := value[0]
			if len(value) < 2 || value[len(value)-1] != quote {
				return fail("unmatched value quote")
			}
			value = value[1 : len(value)-1]
			if strings.IndexByte(value, quote) >= 0 {
				return fail("enclosing quotes cannot appear inside a quoted value")
			}
		}
		env = append(env, name+"="+value)
	}
	return env, nil
}

func envName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
