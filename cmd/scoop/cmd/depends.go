package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/spf13/cobra"
)

var dependsCmd = &cobra.Command{
	Use:   "depends <app>",
	Short: "List dependencies for an app",
	Long:  "List dependencies for an app, in the order they'll be installed.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := strings.TrimSuffix(args[0], ".json")

		// Find the manifest
		_, manifestPath := bucket.AppManifestPath(appName)
		if manifestPath == "" {
			return fmt.Errorf("couldn't find manifest for '%s'", appName)
		}

		m, err := manifest.ParseFile(manifestPath)
		if err != nil {
			return fmt.Errorf("parsing manifest: %w", err)
		}

		if len(m.Depends) == 0 {
			app.LogInfo("'%s' has no dependencies.", appName)
			return nil
		}

		// Resolve full dependency tree
		resolved, err := resolveDeps(appName, "64bit", nil, nil)
		if err != nil {
			return err
		}

		app.LogInfo("Dependencies for '%s':", appName)
		for _, dep := range resolved {
			installed := isInstalled(dep)
			marker := ""
			if installed {
				marker = " (installed)"
			}
			fmt.Printf("  %s%s\n", dep, marker)
		}

		return nil
	},
}

// resolveDeps performs DFS dependency resolution (mirrors depends.ps1 Get-Dependency).
func resolveDeps(appName, arch string, resolved, unresolved []string) ([]string, error) {
	if unresolved == nil {
		unresolved = []string{}
	}
	if resolved == nil {
		resolved = []string{}
	}

	// Check if already resolved
	for _, r := range resolved {
		if r == appName {
			return resolved, nil
		}
	}

	// Check for circular deps
	for _, u := range unresolved {
		if u == appName {
			return nil, fmt.Errorf("circular dependency detected: '%s'", appName)
		}
	}

	unresolved = append(unresolved, appName)

	// Find the manifest
	_, manifestPath := bucket.AppManifestPath(appName)
	if manifestPath == "" {
		// Maybe it's already installed from a URL — just return
		return append(resolved, appName), nil
	}

	m, err := manifest.ParseFile(manifestPath)
	if err != nil {
		return append(resolved, appName), nil
	}

	// Process dependencies
	for _, dep := range m.Depends {
		depApp := strings.Split(dep, "/")[0]
		depApp = strings.TrimSuffix(depApp, ".json")

		subResolved, subErr := resolveDeps(depApp, arch, resolved, unresolved)
		if subErr != nil {
			return nil, subErr
		}
		resolved = subResolved
	}

	// Remove from unresolved
	var newUnresolved []string
	for _, u := range unresolved {
		if u != appName {
			newUnresolved = append(newUnresolved, u)
		}
	}

	return append(resolved, appName), nil
}

func isInstalled(appName string) bool {
	// Check both local and global scopes
	for _, global := range []bool{false, true} {
		if _, err := os.Stat(app.AppCurrentDir(appName, global)); err == nil {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(dependsCmd)
}
