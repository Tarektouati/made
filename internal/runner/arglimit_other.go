//go:build !windows && !linux && !darwin

package runner

// hostLimits reports no known cap: the OS limits arguments plus environment in total.
func hostLimits() osLimits {
	return osLimits{os: "the OS", unit: ArgUnit}
}
