//go:build !windows

package env

import "os"

// writeEnvVarWindows is a stub for non-Windows platforms.
// Environment variable persistence on non-Windows is handled at the process level
// by the caller functions (AddPath, RemovePath, SetEnv) which call os.Setenv directly.
func writeEnvVarWindows(name, value string, global bool) error {
	// On non-Windows, registry persistence is not available.
	// Process-level env changes are handled by the caller before calling this function.
	// This stub ensures compilation on non-Windows platforms.
	return nil
}

// readEnvFromRegistry is a stub for non-Windows platforms.
func readEnvFromRegistry(name string, global bool) (string, error) {
	return "", nil
}

// publishEnvChangeWindows is a stub for non-Windows platforms.
func publishEnvChangeWindows() error {
	return nil
}
