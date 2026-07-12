//go:build !windows

package proc

// ListProcessPaths is a no-op on non-Windows platforms.
func ListProcessPaths() ([]string, error) {
	return nil, nil
}

// ListProcessImages is a no-op on non-Windows platforms.
func ListProcessImages() ([]string, error) {
	return nil, nil
}
