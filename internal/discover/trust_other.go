//go:build !unix

package discover

import "io/fs"

// checkParentMadefile checks nothing outside unix: there is no owner uid or mode bits
// to check a parent Madefile against. It returns no FileInfo, so ReadMadefile does
// not compare files either.
func checkParentMadefile(string) (fs.FileInfo, error) { return nil, nil }

// discoveryHome returns "": outside unix, Madefile discovery does not stop at the
// home directory.
func discoveryHome() string { return "" }
