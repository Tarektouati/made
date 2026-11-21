//go:build !unix

package selfupdate

// syncDir does nothing: directories cannot be synced like files outside unix, and
// made self-update never replaces a binary there.
func syncDir(string) {}
