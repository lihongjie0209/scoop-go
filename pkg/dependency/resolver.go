// Package dependency resolves app dependency trees using topological sort.
// Mirrors Get-Dependency() from lib/depends.ps1.
package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// Resolve returns all dependencies of an app in install order (topological sort),
// including the app itself as the last element.
// Includes both explicit depends and implicit installation helpers (lessmsi, innounp, dark).
// Entries may be plain app names or "bucket/app" when the dependency was specified that way.
func Resolve(appName, arch string) ([]string, error) {
	return resolve(appName, arch, nil, nil)
}

// AppName extracts the bare app name from "bucket/app", a URL, or a plain name.
func AppName(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimSuffix(ref, ".json")
	// URL or path: take last path segment without query
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, `\\`) {
		ref = strings.Split(ref, "?")[0]
		ref = strings.TrimRight(ref, "/\\")
		if i := strings.LastIndexAny(ref, "/\\"); i >= 0 {
			ref = ref[i+1:]
		}
		return strings.TrimSuffix(ref, ".json")
	}
	// bucket/app
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// IsInstalled reports whether the app has a current installation (local or global).
func IsInstalled(appName string) bool {
	name := AppName(appName)
	for _, global := range []bool{false, true} {
		if _, err := os.Stat(app.AppCurrentDir(name, global)); err == nil {
			return true
		}
	}
	return false
}

// Missing returns dependencies from resolved (install order) that are not yet installed.
// The root app is included if includeRoot is true and it is not installed.
func Missing(resolved []string, rootApp string, includeRoot bool) []string {
	root := AppName(rootApp)
	var out []string
	seen := make(map[string]bool)
	for _, dep := range resolved {
		name := AppName(dep)
		if seen[name] {
			continue
		}
		if name == root && !includeRoot {
			continue
		}
		if IsInstalled(name) {
			continue
		}
		seen[name] = true
		out = append(out, dep)
	}
	return out
}

func resolve(appName, arch string, resolved, unresolved []string) ([]string, error) {
	displayName := appName
	cleanName := AppName(appName)

	for _, r := range resolved {
		if AppName(r) == cleanName {
			return resolved, nil
		}
	}
	for _, u := range unresolved {
		if u == cleanName {
			return nil, fmt.Errorf("circular dependency detected: '%s'", displayName)
		}
	}

	unresolved = append(unresolved, cleanName)

	// Prefer explicit bucket if provided as bucket/app
	var manifestPath string
	var foundBucket string
	if i := strings.Index(appName, "/"); i >= 0 && !strings.Contains(appName, "://") {
		b := appName[:i]
		p := filepath.Join(bucket.ManifestDir(b), cleanName+".json")
		if _, err := os.Stat(p); err == nil {
			foundBucket = b
			manifestPath = p
		}
	}
	if manifestPath == "" {
		foundBucket, manifestPath = bucket.AppManifestPath(cleanName)
	}
	if manifestPath == "" {
		// No manifest: keep the requested name so install can still try URL/local paths.
		return append(resolved, displayName), nil
	}

	m, err := manifest.ParseFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("parsing manifest for '%s': %w", cleanName, err)
	}

	helpers := getInstallationHelpers(m, arch)
	deps := uniqueStrings(append(helpers, m.Depends...))
	for _, dep := range deps {
		r, err := resolve(dep, arch, resolved, unresolved)
		if err != nil {
			return nil, err
		}
		resolved = r
	}

	// Prefer bucket/app form when we know the bucket (matches PowerShell Get-Dependency output).
	entry := cleanName
	if foundBucket != "" {
		entry = foundBucket + "/" + cleanName
	}
	return append(resolved, entry), nil
}

// getInstallationHelpers detects which helpers an app needs based on its URLs and scripts.
// Mirrors Get-InstallationHelper from lib/depends.ps1.
// Already-installed helpers are omitted.
func getInstallationHelpers(m *manifest.Manifest, arch string) []string {
	var helpers []string

	urls := m.GetURL(arch)
	scripts := collectScripts(m, arch)

	// lessmsi: MSI URLs when use_lessmsi is enabled
	if needsLessMSI(urls, scripts) {
		helpers = append(helpers, "lessmsi")
	}

	// innounp: innosetup flag or Expand-InnoArchive in scripts
	if m.InnoSetup || scriptMentions(scripts, "Expand-InnoArchive") {
		helpers = append(helpers, "innounp")
	}

	// dark: Expand-DarkArchive in scripts
	if scriptMentions(scripts, "Expand-DarkArchive") {
		helpers = append(helpers, "dark")
	}

	// 7zip is handled natively in Go for common formats; only request when scripts
	// explicitly call Expand-7zipArchive and the helper app is not installed.
	if scriptMentions(scripts, "Expand-7zipArchive") {
		helpers = append(helpers, "7zip")
	}

	// Drop helpers that are already installed
	var needed []string
	for _, h := range helpers {
		if !IsInstalled(h) {
			needed = append(needed, h)
		}
	}
	return needed
}

func collectScripts(m *manifest.Manifest, arch string) []string {
	var scripts []string
	scripts = append(scripts, m.GetPreInstall(arch)...)
	scripts = append(scripts, m.GetPostInstall(arch)...)
	if inst := m.GetInstaller(arch); inst != nil {
		scripts = append(scripts, inst.Script...)
	}
	return scripts
}

func scriptMentions(scripts []string, needle string) bool {
	for _, s := range scripts {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func needsLessMSI(urls []string, scripts []string) bool {
	if scriptMentions(scripts, "Expand-MsiArchive") {
		return true
	}
	// Mirror PS: only when use_lessmsi is configured. We always suggest lessmsi
	// for .msi URLs if the helper is missing; install may still use msiexec.
	for _, u := range urls {
		if strings.HasSuffix(strings.ToLower(strings.Split(u, "?")[0]), ".msi") {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
