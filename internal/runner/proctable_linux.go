//go:build linux

package runner

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"syscall"
)

// procStat is the part of a /proc/<pid>/stat line teardown needs.
type procStat struct {
	state byte // 'Z' for a zombie
	ppid  int
	pgrp  int
}

// processTable maps every visible pid to its parent pid, read from /proc. Processes
// that exit while the table is being read are skipped.
func processTable() (map[int]int, error) {
	table := make(map[int]int)
	err := eachProc(func(pid int, st procStat) { table[pid] = st.ppid })
	return table, err
}

// zombie reports whether pid has exited and is waiting to be reaped.
func zombie(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	st, err := parseStat(stat)
	return err == nil && st.state == 'Z'
}

// groupAlive reports whether process group pgid has a member that is not a zombie.
// kill(-pgid, 0) also succeeds when only zombies are left.
func groupAlive(pgid int) bool {
	alive := false
	err := eachProc(func(_ int, st procStat) {
		if st.pgrp == pgid && st.state != 'Z' {
			alive = true
		}
	})
	if err != nil {
		return syscall.Kill(-pgid, 0) == nil
	}
	return alive
}

// orphans returns made's running children in its own process group. made is a child
// subreaper (see becomeSubreaper), so a process of the task whose parent exited is
// reparented to it; the interpreters made started are among them too.
func orphans() []int {
	self, pgrp := os.Getpid(), syscall.Getpgrp()
	var pids []int
	_ = eachProc(func(pid int, st procStat) {
		if st.ppid == self && st.pgrp == pgrp && st.state != 'Z' {
			pids = append(pids, pid)
		}
	})
	return pids
}

// eachProc calls fn for every process in /proc whose stat can be read and parsed.
func eachProc(fn func(pid int, st procStat)) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		st, err := parseStat(stat)
		if err != nil {
			continue
		}
		fn(pid, st)
	}
	return nil
}

// parseStat extracts the state, parent pid and process group from a /proc/<pid>/stat
// line, "pid (comm) state ppid pgrp ...". comm may contain spaces and parentheses, so
// fields are counted from the last ')'.
func parseStat(stat []byte) (procStat, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return procStat{}, errors.New("malformed stat: no command name")
	}
	fields := bytes.Fields(stat[i+1:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return procStat{}, errors.New("malformed stat: missing state, ppid or pgrp")
	}
	ppid, err := strconv.Atoi(string(fields[1]))
	if err != nil {
		return procStat{}, err
	}
	pgrp, err := strconv.Atoi(string(fields[2]))
	if err != nil {
		return procStat{}, err
	}
	return procStat{state: fields[0][0], ppid: ppid, pgrp: pgrp}, nil
}
