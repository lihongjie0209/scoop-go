package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
)

var cleanupFlags struct {
	all    bool
	global bool
	cache  bool
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup <app>",
	Short: "Cleanup apps by removing old versions",
	Long:  `Remove old versions of an app. Use '*' or '--all' to cleanup all apps.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Admin check for global operations
		if cleanupFlags.global {
			if err := checkAdminRights(); err != nil {
				return fmt.Errorf("global cleanup requires administrator privileges: %w", err)
			}
		}

		// Determine apps to clean
		var apps []appTuple
		if cleanupFlags.all || (len(args) == 1 && args[0] == "*") {
			for _, g := range []bool{false, true} {
				if g && !cleanupFlags.global {
					continue
				}
				entries, _ := os.ReadDir(app.AppDir(g))
				for _, e := range entries {
					if e.IsDir() && e.Name() != "scoop" {
						apps = append(apps, appTuple{e.Name(), g})
					}
				}
			}
		} else if len(args) == 1 {
			apps = append(apps, appTuple{args[0], cleanupFlags.global})
		} else {
			return fmt.Errorf("<app> missing")
		}

		for _, a := range apps {
			cleanupApp(a.name, a.global, cleanupFlags.cache)
		}

		if cleanupFlags.cache {
			os.RemoveAll(filepath.Join(app.Dirs().CacheDir, "*.download"))
			app.LogSuccess("Cache cleaned.")
		}

		return nil
	},
}

type appTuple struct {
	name   string
	global bool
}

func cleanupApp(appName string, global, cleanCache bool) {
	appsDir := app.AppDir(global)
	appPath := filepath.Join(appsDir, appName)

	// Find current version
	currentPath := filepath.Join(appPath, "current")
	var currentVersion string
	if target, err := os.Readlink(currentPath); err == nil {
		currentVersion = filepath.Base(target)
	} else {
		currentVersion = ""
	}

	// Remove old caches
	if cleanCache {
		cacheDir := app.Dirs().CacheDir
		entries, _ := filepath.Glob(filepath.Join(cacheDir, appName+"#*"))
		for _, entry := range entries {
			if currentVersion != "" && strings.Contains(entry, "#"+currentVersion+"#") {
				continue
			}
			os.Remove(entry)
		}
	}

	// List version directories
	entries, err := os.ReadDir(appPath)
	if err != nil {
		return
	}

	var removed []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "current" || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if e.Name() == currentVersion {
			continue
		}

		versionDir := filepath.Join(appPath, e.Name())

		// Unlink persist data before removing the version directory
		// This ensures any junctions/hardlinks in the version dir pointing
		// to persist data are cleaned up first, preventing any issues with
		// dangling reparse points.
		unlinkPersistData(appName, global, versionDir)

		app.LogInfo("Removing %s %s", appName, e.Name())
		os.RemoveAll(versionDir)
		removed = append(removed, e.Name())
	}

	if len(removed) == 0 {
		app.LogInfo("%s is already clean", appName)
	}
}

// unlinkPersistData removes persist data junctions/links from a version directory,
// leaving the actual persisted data in the persist directory untouched.
func unlinkPersistData(appName string, global bool, versionDir string) {
	persistDir := app.PersistDir(appName, global)
	if _, err := os.Stat(persistDir); os.IsNotExist(err) {
		return // No persist directory for this app
	}

	// Check for a manifest in the version dir to get persist items
	manifestPath := filepath.Join(versionDir, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return
	}

	// Parse the persist field using simple string search
	// This avoids importing the manifest package and creating a circular dependency concern
	content := string(manifestData)

	// If the version directory has a "persist" item in its manifest,
	// remove the junction/link from the version directory.
	// The persist items would be junctions pointing to app.PersistDir/appName/...
	// We just need to remove those junctions from the version dir's path.
	idx := strings.Index(content, `"persist"`)
	if idx < 0 {
		return
	}

	// Simple check: if the persist dir exists, the links FROM versionDir TO persistDir
	// need to be cleaned up. We can't easily parse the manifest here without importing,
	// but os.RemoveAll on the version dir will handle junctions correctly.
	// The explicit cleanup here is for safety: remove the junctions first.

	// Try to remove common persist item paths from the version directory
	persistItems := extractPersistItemNames(manifestData)
	for _, item := range persistItems {
		itemPath := filepath.Join(versionDir, item)
		if _, err := os.Stat(itemPath); err == nil {
			app.LogDebug("Removing persist link: %s", itemPath)
			os.RemoveAll(itemPath)
		}
	}
}

// extractPersistItemNames extracts the source item names from a manifest's persist field.
func extractPersistItemNames(manifestData []byte) []string {
	content := string(manifestData)
	var items []string

	// Find the persist field
	persistIdx := strings.Index(content, `"persist"`)
	if persistIdx < 0 {
		return items
	}

	rest := content[persistIdx+len(`"persist"`):]

	// Skip colon and whitespace
	rest = strings.TrimSpace(rest)
	if len(rest) == 0 || rest[0] != ':' {
		return items
	}
	rest = strings.TrimSpace(rest[1:])

	if len(rest) == 0 {
		return items
	}

	// Handle array of arrays [[src, dst], ...]
	if len(rest) > 0 && rest[0] == '[' {
		rest = strings.TrimSpace(rest[1:]) // skip opening bracket

		// Check if it's an array of strings ["a", "b"] or array of arrays [["a","b"],...]
		for len(rest) > 0 && rest[0] == '[' {
			// Array of arrays: [[src1, dst1], [src2, dst2]]
			rest = strings.TrimSpace(rest[1:]) // skip inner opening bracket
			// Extract first string (src)
			rest = strings.TrimSpace(rest)
			if len(rest) > 0 && rest[0] == '"' {
				end := strings.IndexByte(rest[1:], '"')
				if end >= 0 {
					item := rest[1 : end+1]
					items = append(items, item)
					rest = rest[end+2:] // skip past closing " and comma/whitespace
				}
			}
			// Find the closing bracket
			bracketEnd := strings.IndexByte(rest, ']')
			if bracketEnd >= 0 {
				rest = strings.TrimSpace(rest[bracketEnd+1:])
			}
			if len(rest) > 0 && rest[0] == ',' {
				rest = strings.TrimSpace(rest[1:])
			}
		}
	} else if len(rest) > 0 && rest[0] == '"' {
		// Single string persistance items: "item"
		end := strings.IndexByte(rest[1:], '"')
		if end >= 0 {
			items = append(items, rest[1:end+1])
		}
	}

	return items
}

// checkAdminRights verifies that the current process has administrator privileges.
// On Windows, this uses token elevation check via golang.org/x/sys/windows.
// On non-Windows platforms, admin check is not applicable.
func checkAdminRights() error {
	if runtime.GOOS != "windows" {
		return nil // No admin concept on non-Windows
	}

	var token windows.Token
	h := windows.CurrentProcess()
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("opening process token: %w", err)
	}
	defer token.Close()

	if !token.IsElevated() {
		return fmt.Errorf("this operation requires administrator privileges (run as Administrator)")
	}

	return nil
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
	cleanupCmd.Flags().BoolVarP(&cleanupFlags.all, "all", "a", false, "Cleanup all apps")
	cleanupCmd.Flags().BoolVarP(&cleanupFlags.global, "global", "g", false, "Cleanup globally installed apps")
	cleanupCmd.Flags().BoolVarP(&cleanupFlags.cache, "cache", "k", false, "Remove outdated download cache")
}
