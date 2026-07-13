package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/spf13/cobra"
)

// AppInfo holds parsed install.json data.
type AppInfo struct {
	Architecture string `json:"architecture,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	URL          string `json:"url,omitempty"`
	Hold         bool   `json:"hold,omitempty"`
}

var listFlags struct {
	outdated bool
	failed   bool
	held     bool
}

var listCmd = &cobra.Command{
	Use:   "list [query]",
	Short: "List installed apps",
	Long:  "Lists all installed apps, or the apps matching the supplied query.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = strings.ToLower(args[0])
		}

		found := 0

		// Column headers
		color.New(color.FgWhite, color.Bold).Printf("%-30s %-15s %-15s %s\n", "Name", "Version", "Source", "Info")

		// List apps in both scopes
		for _, global := range []bool{false, true} {
			appsDir := app.AppDir(global)
			entries, err := os.ReadDir(appsDir)
			if err != nil {
				continue
			}

			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == "scoop" {
					continue
				}

				name := entry.Name()
				if query != "" && !strings.Contains(strings.ToLower(name), query) {
					continue
				}

				// Get detailed app info
				version, info, failed := getAppDetails(name, global)

				// Apply filters
				if listFlags.failed && !failed {
					continue
				}
				if listFlags.held && !info.Hold {
					continue
				}
				if listFlags.outdated {
					// Check if a newer version exists in the bucket
					if !isOutdated(name, version, info.Bucket) {
						continue
					}
				}

				// Source column
				sourceStr := info.Bucket
				if sourceStr == "" {
					sourceStr = info.URL
				}
				if sourceStr == "" {
					sourceStr = "-"
				}

				// Info column
				var infoParts []string
				if failed {
					infoParts = append(infoParts, "failed")
				}
				if info.Hold {
					infoParts = append(infoParts, "held")
				}
				if info.Architecture != "" {
					infoParts = append(infoParts, info.Architecture)
				}
				// Check deprecated
				if info.Bucket != "" {
					deprecatedPath := filepath.Join(bucket.Dir(info.Bucket), "deprecated", name+".json")
					if _, err := os.Stat(deprecatedPath); err == nil {
						infoParts = append(infoParts, "deprecated")
					}
				}
				if global {
					infoParts = append(infoParts, "global")
				}

				infoStr := strings.Join(infoParts, ", ")

				if version != "" {
					color.White("%-30s %-15s %-15s %s\n", name, version, sourceStr, infoStr)
				} else {
					color.Yellow("%-30s %-15s %-15s %s\n", name, "?no-version?", sourceStr, "failed")
				}
				found++
			}
		}

		if found == 0 {
			if query != "" || listFlags.failed || listFlags.held || listFlags.outdated {
				app.LogInfo("No apps match the given criteria")
				return fmt.Errorf("no apps match the given criteria")
			}
			// Match PowerShell: empty install list exits non-zero
			return fmt.Errorf("there aren't any apps installed")
		}

		return nil
	},
}

// getAppDetails returns the version, install info, and whether the app is in a failed state.
func getAppDetails(appName string, global bool) (string, AppInfo, bool) {
	appDir := app.AppDir(global)
	dir := filepath.Join(appDir, appName)
	currentPath := app.AppCurrentDir(appName, global)

	var info AppInfo
	versionDir := ""
	failed := false

	// Resolve version directory
	if target, err := os.Readlink(currentPath); err == nil {
		if filepath.IsAbs(target) {
			versionDir = target
		} else {
			versionDir = filepath.Join(dir, target)
		}
	} else {
		// Check if current exists as a regular directory
		if st, err := os.Stat(currentPath); err == nil && st.IsDir() {
			versionDir = currentPath
		} else {
			// Fallback: scan version dirs
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
					versionDir = filepath.Join(dir, e.Name())
					break
				}
			}
		}
	}

	if versionDir == "" {
		return "", info, true
	}

	version := filepath.Base(versionDir)

	// Read install.json
	installData, err := os.ReadFile(filepath.Join(versionDir, "install.json"))
	if err == nil {
		json.Unmarshal(installData, &info)
	}

	// Check if app is in a failed state (no manifest.json or no install.json)
	if err != nil {
		failed = true
	}
	if _, err := os.Stat(filepath.Join(versionDir, "manifest.json")); err != nil {
		failed = true
	}

	return version, info, failed
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVarP(&listFlags.outdated, "outdated", "o", false, "Only list outdated apps")
	listCmd.Flags().BoolVarP(&listFlags.failed, "failed", "x", false, "Only list apps with failed installs")
	listCmd.Flags().BoolVarP(&listFlags.held, "held", "H", false, "Only list held apps")
}

// isOutdated checks whether a newer version is available in the local bucket.
func isOutdated(appName, installedVersion, bucketName string) bool {
	if bucketName == "" {
		return false
	}
	manifestPath := filepath.Join(bucket.ManifestDir(bucketName), appName+".json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return m.Version != installedVersion
}
