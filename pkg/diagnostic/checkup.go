// Package diagnostic runs health checks on the Scoop installation.
// Mirrors lib/diagnostic.ps1 from the original Scoop.
package diagnostic

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
)

// Check represents a single diagnostic check result.
type Check struct {
	Name    string
	Passed  bool
	Message string
	Fix     string
}

// RunAll runs all diagnostic checks.
func RunAll() []Check {
	var results []Check
	results = append(results, checkGit())
	results = append(results, checkMainBucket())
	results = append(results, checkLongPaths())
	results = append(results, checkDeveloperMode())
	results = append(results, checkWindowsDefender())
	results = append(results, checkHelperTools())
	results = append(results, checkNtfsVolume())
	return results
}

func checkGit() Check {
	c := Check{Name: "Git availability"}
	if err := exec.Command("git", "--version").Run(); err == nil {
		c.Passed = true
		c.Message = "Git is available"
	} else {
		c.Passed = false
		c.Message = "Git is not installed"
		c.Fix = "Run 'scoop install git' (from a PowerShell with Scoop)"
	}
	return c
}

func checkMainBucket() Check {
	c := Check{Name: "Main bucket"}
	if bucket.IsLocal("main") {
		c.Passed = true
		c.Message = "Main bucket is added"
	} else {
		c.Passed = false
		c.Message = "Main bucket is not added"
		c.Fix = "Run 'scoop bucket add main'"
	}
	return c
}

func checkLongPaths() Check {
	c := Check{Name: "Long paths support"}
	enabled, err := isLongPathsEnabled()
	if err != nil {
		c.Passed = false
		c.Message = "Long paths: " + err.Error()
		c.Fix = "Run: reg add HKLM\\System\\CurrentControlSet\\Control\\FileSystem /v LongPathsEnabled /t REG_DWORD /d 1 /f"
		return c
	}
	if enabled {
		c.Passed = true
		c.Message = "Long paths support is enabled"
	} else {
		c.Passed = false
		c.Message = "Long paths support is disabled"
		c.Fix = "Run: reg add HKLM\\System\\CurrentControlSet\\Control\\FileSystem /v LongPathsEnabled /t REG_DWORD /d 1 /f"
	}
	return c
}

func checkDeveloperMode() Check {
	c := Check{Name: "Windows Developer Mode"}
	enabled, err := isDeveloperModeEnabled()
	if err != nil {
		c.Passed = false
		c.Message = "Developer mode: " + err.Error()
		c.Fix = "Enable Developer Mode in Windows Settings > Privacy & security > For developers"
		return c
	}
	if enabled {
		c.Passed = true
		c.Message = "Windows Developer Mode is enabled"
	} else {
		c.Passed = false
		c.Message = "Windows Developer Mode is disabled"
		c.Fix = "Enable Developer Mode in Windows Settings > Privacy & security > For developers > Developer Mode"
	}
	return c
}

// RunAllAndPrint runs checks and prints results.
func RunAllAndPrint() {
	checks := RunAll()
	hasIssues := false

	for _, c := range checks {
		if c.Passed {
			app.LogSuccess("%s: %s", c.Name, c.Message)
		} else {
			hasIssues = true
			app.LogWarn("%s: %s", c.Name, c.Message)
			if c.Fix != "" {
				fmt.Printf("  %s\n", c.Fix)
			}
		}
	}

	if !hasIssues {
		app.LogSuccess("All checks passed!")
	}
}

// EnsureCheckupDir creates the diagnostic directory.
func EnsureCheckupDir() error {
	return os.MkdirAll(app.Dirs().ScoopDir, 0755)
}
