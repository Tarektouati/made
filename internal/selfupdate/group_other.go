//go:build !unix

package selfupdate

import "os"

// preserveGroup does nothing on platforms without Unix file groups.
func preserveGroup(*os.File, os.FileInfo) {}
