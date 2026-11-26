package runner

import (
	"runtime"
	"slices"
	"strings"
)

// environmentKey follows os/exec's case-insensitive keys on Windows.
func environmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func environmentValue(env []string, name string) string {
	key := environmentKey(name)
	for i := len(env) - 1; i >= 0; i-- {
		k, value, ok := strings.Cut(env[i], "=")
		if ok && environmentKey(k) == key {
			return value
		}
	}
	return ""
}

func mergeEnvironment(base, defaults []string) []string {
	env := slices.Clone(base)
	inherited := make(map[string]bool, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		inherited[environmentKey(key)] = true
	}
	added := make(map[string]int, len(defaults))
	for _, entry := range defaults {
		key, _, _ := strings.Cut(entry, "=")
		key = environmentKey(key)
		if inherited[key] {
			continue
		}
		if i, ok := added[key]; ok {
			env[i] = entry
		} else {
			added[key] = len(env)
			env = append(env, entry)
		}
	}
	return env
}
