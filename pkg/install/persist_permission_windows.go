//go:build windows

package install

import (
	"os/exec"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

// persistPermission grants the local Users group (S-1-5-32-545) Write access
// on the global persist directory so that non-admin users can modify persisted
// data after a global install.
//
// Mirrors persist_permission() from lib/install.ps1 L522-531.
// Only runs when installing globally and the manifest declares persist items.
func persistPermission(persistBase string) {
	// icacls <path> /grant *S-1-5-32-545:(OI)(CI)W /T /C /Q
	// (OI)(CI) = ObjectInherit + ContainerInherit  W = Write
	cmd := exec.Command("icacls", persistBase,
		"/grant", "*S-1-5-32-545:(OI)(CI)W",
		"/T", "/C", "/Q")
	if out, err := cmd.CombinedOutput(); err != nil {
		app.LogDebug("persist_permission: icacls failed: %v — %s", err, out)
	}
}
