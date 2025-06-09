//go:build disable_nvml

package nvidia_base

const IsNvmlSupported = false

// to be called at the end of main() - otherwise probably don't use this
func CleanupAtExit() {
	// do nothing if fan2go was compiled without nvml support
}
