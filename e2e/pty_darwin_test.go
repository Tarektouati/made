package e2e

import (
	"bytes"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pseudo-terminal pair and returns its master and slave ends.
func openPTY() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	if err = unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	if err = unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	// TIOCPTYGNAME fills a 128-byte buffer with the slave's NUL-terminated path.
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		_ = master.Close()
		return nil, nil, errno
	}
	path := string(name[:bytes.IndexByte(name[:], 0)])
	slave, err = os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
