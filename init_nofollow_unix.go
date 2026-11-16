//go:build unix

package main

import "syscall"

// initForceNoFollow is OR'd into made init --force opens so a swapped-in symlink is not followed.
const initForceNoFollow = syscall.O_NOFOLLOW
