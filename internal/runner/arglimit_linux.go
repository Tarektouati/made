package runner

import "os"

// hostLimits reports Linux's cap on a single argument or environment string,
// MAX_ARG_STRLEN: 32 pages including the NUL, so 128 KiB with 4 KiB pages. The total
// depends on the stack size limit, so it is not given.
func hostLimits() osLimits {
	return osLimits{os: "Linux", unit: ArgUnit, perArg: 32 * os.Getpagesize()}
}
