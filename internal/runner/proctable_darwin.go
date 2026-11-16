//go:build darwin

package runner

import "golang.org/x/sys/unix"

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
