//go:build !windows

package install

import "os"

// createJunction creates a directory junction on Windows.
// On Unix, this falls back to creating a symbolic link.
// The link argument is the junction path and target is what it points to.
func createJunction(link, target string) error {
	return os.Symlink(target, link)
}

// setJunctionReadOnly sets the read-only attribute on a reparse point.
// On Unix this is a no-op since junctions don't exist.
func setJunctionReadOnly(path string) error {
	return nil
}
