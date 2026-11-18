//go:build darwin

package runner

import (
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// sZomb is the p_stat of a zombie (SZOMB in <sys/proc.h>).
const sZomb = 5

// processTable maps every visible pid to its parent pid, read from the kern.proc.all
// sysctl.
func processTable() (map[int]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	table := make(map[int]int, len(procs))
	for _, p := range procs {
		table[int(p.Proc.P_pid)] = int(p.Eproc.Ppid)
	}
	return table, nil
}

// startedAt returns made's start time in microseconds, or 0 if it cannot be read.
var startedAt = sync.OnceValue(func() int64 {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return 0
	}
	return micros(p.Proc.P_starttime)
})

func micros(tv unix.Timeval) int64 { return tv.Sec*1e6 + int64(tv.Usec) }

// orphans returns the running processes in made's process group that were reparented
// to launchd (pid 1) and started after made: processes of the task whose parent exited.
// macOS has no child subreaper, so this is how they are found.
func orphans() []int {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", syscall.Getpgrp())
	if err != nil {
		return nil
	}
	start := startedAt()
	var pids []int
	for _, p := range procs {
		if p.Eproc.Ppid == 1 && p.Proc.P_stat != sZomb && start > 0 && micros(p.Proc.P_starttime) >= start {
			pids = append(pids, int(p.Proc.P_pid))
		}
	}
	return pids
}
