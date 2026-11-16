//go:build linux

package runner

import (
	"bytes"
	"errors"
	"os"
	"strconv"
)

// processTable maps every visible pid to its parent pid, read from /proc. Processes
// that exit while the table is being read are skipped.
func processTable() (map[int]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	table := make(map[int]int, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		ppid, err := parseStatPPID(stat)
		if err != nil {
			continue
		}
		table[pid] = ppid
	}
	return table, nil
}

// parseStatPPID extracts the parent pid from a /proc/<pid>/stat line,
// "pid (comm) state ppid ...". comm may contain spaces and parentheses, so fields are
// counted from the last ')'.
func parseStatPPID(stat []byte) (int, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, errors.New("malformed stat: no command name")
	}
	fields := bytes.Fields(stat[i+1:])
	if len(fields) < 2 {
		return 0, errors.New("malformed stat: missing ppid")
	}
	return strconv.Atoi(string(fields[1]))
}
