// Package dependency resolves app dependency trees using topological sort.
// Mirrors Get-Dependency() from lib/depends.ps1.
package dependency

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// Resolve returns all dependencies of an app in install order (topological sort).
// Includes both explicit depends and implicit installation helpers (7zip, innounp, etc.).
func Resolve(appName, arch string) ([]string, error) {
	return resolve(appName, arch, nil, nil)
}

func resolve(appName, arch string, resolved, unresolved []string) ([]string, error) {
	cleanName := appName
	if idx := strings.Index(appName, "/"); idx >= 0 {
		cleanName = appName[idx+1:]
	}

	for _, r := range resolved {
		if r == cleanName {
			return resolved, nil
		}
	}
	for _, u := range unresolved {
		if u == cleanName {
			return nil, fmt.Errorf("circular dependency detected: '%s'", appName)
		}
	}

	unresolved = append(unresolved, cleanName)

	_, manifestPath := bucket.AppManifestPath(cleanName)
	if manifestPath == "" {
		return append(resolved, appName), nil
	}

	m, err := manifest.ParseFile(manifestPath)
	if err != nil {
		return append(resolved, appName), nil
	}

	// Get installation helpers (mirrors Get-InstallationHelper from lib/depends.ps1)
	helpers := getInstallationHelpers(m, arch)

	// Process explicit dependencies + helpers
	deps := append(helpers, m.Depends...)
	for _, dep := range deps {
		depApp := strings.Split(dep, "/")[0]
		depApp = strings.TrimSuffix(depApp, ".json")

		r, err := resolve(depApp, arch, resolved, unresolved)
		if err != nil {
			return nil, err
		}
		resolved = r
	}

	return append(resolved, cleanName), nil
}

// getInstallationHelpers detects which helpers an app needs based on its URLs and settings.
// Mirrors Get-InstallationHelper from lib/depends.ps1 L74-132.
func getInstallationHelpers(m *manifest.Manifest, arch string) []string {
	var helpers []string

	// Check all URLs for archive types that need external helpers
	for _, url := range m.GetURL(arch) {
		lowerURL := strings.ToLower(url)
		ext := filepath.Ext(lowerURL)

		switch ext {
		case ".msi":
			helpers = append(helpers, "lessmsi")
		case ".001", ".7z":
			// 7zip is handled natively in Go version, no helper needed
		}
	}

	// InnoSetup
	if m.InnoSetup {
		helpers = append(helpers, "innounp")
	}

	// Check installer file extensions
	if m.Installer != nil {
		instFile := strings.ToLower(m.Installer.File)
		if strings.HasSuffix(instFile, ".msi") {
			if !contains(helpers, "lessmsi") {
				helpers = append(helpers, "lessmsi")
			}
		}
	}

	return helpers
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
