//go:build !windows && !linux

package runner

// becomeSubreaper does nothing: child subreapers are Linux-only.
func becomeSubreaper() {}
