//go:build !unix

package selfupdate

// writable reports whether the current user may create files in dir. Outside unix
// it cannot tell, so it says yes and leaves the decision to creating the temporary
// file. Platforms other than unix have no release archive, so made self-update does
// not get here anyway.
func writable(string) bool {
	return true
}
