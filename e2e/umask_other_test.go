//go:build !unix

package e2e

// setTestUmask does nothing outside unix, which has no umask.
func setTestUmask() {}
