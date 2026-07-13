//go:build !windows

package install

// persistPermission is a no-op on non-Windows platforms.
func persistPermission(_ string) {}
