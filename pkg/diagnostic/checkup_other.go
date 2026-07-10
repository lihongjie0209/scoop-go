//go:build !windows

package diagnostic

import (
	"fmt"
	"os/exec"
	"strings"
)

// isLongPathsEnabled returns a no-op result on non-Windows platforms.
func isLongPathsEnabled() (bool, error) {
	return true, nil
}

// isDeveloperModeEnabled returns a no-op result on non-Windows platforms.
func isDeveloperModeEnabled() (bool, error) {
	return true, nil
}

// checkWindowsDefender returns a not-applicable result on non-Windows platforms.
func checkWindowsDefender() Check {
	return Check{
		Name:    "Windows Defender",
		Passed:  true,
		Message: "Not applicable on this platform",
	}
}

// checkNtfsVolume returns a not-applicable result on non-Windows platforms.
func checkNtfsVolume() Check {
	return Check{
		Name:    "NTFS volume",
		Passed:  true,
		Message: "Not applicable on this platform",
	}
}

// checkHelperTools checks for the availability of common helper tools (innounp, dark, lessmsi).
func checkHelperTools() Check {
	c := Check{Name: "Helper tools"}

	tools := []struct {
		name string
		pkg  string
	}{
		{"innounp.exe", "innounp"},
		{"dark.exe", "dark"},
		{"lessmsi.exe", "lessmsi"},
	}

	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool.name); err != nil {
			missing = append(missing, tool.pkg)
		}
	}

	if len(missing) == 0 {
		c.Passed = true
		c.Message = "All helper tools are available"
	} else {
		c.Passed = false
		c.Message = fmt.Sprintf("Missing helper tools: %s", strings.Join(missing, ", "))
		var fixes []string
		for _, m := range missing {
			fixes = append(fixes, fmt.Sprintf("scoop install %s", m))
		}
		c.Fix = "Run: " + strings.Join(fixes, " && ")
	}

	return c
}
