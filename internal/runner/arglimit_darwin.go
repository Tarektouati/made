package runner

import "golang.org/x/sys/unix"

// hostLimits reports macOS's limit on arguments plus environment together
// (kern.argmax); it has no cap on a single argument.
func hostLimits() osLimits {
	l := osLimits{os: "macOS", unit: ArgUnit}
	if n, err := unix.SysctlUint32("kern.argmax"); err == nil {
		l.total = int(n)
	}
	return l
}
