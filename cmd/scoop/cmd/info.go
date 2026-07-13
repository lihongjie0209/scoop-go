package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/gitutil"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/version"
	"github.com/spf13/cobra"
)

var infoFlags struct {
	verbose bool
}

var infoCmd = &cobra.Command{
	Use:   "info <app>",
	Short: "Display information about an app",
	Long:  `Display information about an installed app or available app manifest.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw := args[0]
		appName, preferredBucket, _ := parseAppRef(raw)

		// Find manifest from preferred bucket when user specified bucket/app
		m, bucketName, err := install.FindAvailableManifest(appName, preferredBucket)
		if err != nil {
			return err
		}

		// Display basic info
		fmt.Printf("Name:     %s\n", appName)
		fmt.Printf("Version:  %s\n", m.Version)
		source := bucketName
		if m.URL != nil {
			if u := string(m.URL[0]); strings.HasPrefix(u, "http") {
				source = "URL"
			}
		}
		if source != "" {
			fmt.Printf("Source:   %s\n", source)
		} else {
			fmt.Printf("Source:   local\n")
		}

		if updatedAt, updatedBy := manifestCommitInfo(bucketName, appName); !updatedAt.IsZero() && updatedBy != "" {
			fmt.Printf("Updated:  %s  by %s\n", updatedAt.Format("2006-01-02 15:04 MST"), updatedBy)
		}
		if installed := installedVersions(appName); len(installed) > 0 {
			fmt.Printf("Installed: %s\n", strings.Join(installed, ", "))
		}

		if infoFlags.verbose {
			if bucketName != "" {
				if p := filepath.Join(bucket.ManifestDir(bucketName), appName+".json"); pathExists(p) {
					fmt.Printf("Manifest: %s\n", p)
				}
			}
			for _, g := range []bool{false, true} {
				cur := app.AppCurrentDir(appName, g)
				if pathExists(cur) {
					fmt.Printf("Installed:%s (%s)\n", cur, map[bool]string{false: "user", true: "global"}[g])
				}
			}
		}
		fmt.Printf("Summary:  %s\n", m.Description)
		fmt.Printf("Website:  %s\n", m.Homepage)

		// License
		if m.License != nil {
			switch v := m.License.(type) {
			case string:
				fmt.Printf("License:  %s\n", v)
			case map[string]interface{}:
				if id, ok := v["identifier"]; ok {
					fmt.Printf("License:  %s\n", id)
				}
			case *manifest.LicenseObj:
				fmt.Printf("License:  %s\n", v.Identifier)
			default:
				fmt.Printf("License:  %v\n", v)
			}
		}

		// Architecture
		if m.Architecture != nil {
			var archs []string
			if m.Architecture.X64bit != nil {
				archs = append(archs, "64bit")
			}
			if m.Architecture.X32bit != nil {
				archs = append(archs, "32bit")
			}
			if m.Architecture.Arm64 != nil {
				archs = append(archs, "arm64")
			}
			if len(archs) > 0 {
				fmt.Printf("Arch:     %s\n", strings.Join(archs, ", "))
			}
		}

		// Bin
		bins := manifest.BinEntries(m.Bin)
		if len(bins) > 0 {
			var names []string
			for _, b := range bins {
				names = append(names, b[1])
			}
			fmt.Printf("Bin:      %s\n", strings.Join(names, ", "))
		}

		// URL (first one)
		if len(m.URL) > 0 {
			fmt.Printf("URL:      %s\n", m.URL[0])
			if len(m.URL) > 1 {
				fmt.Printf("          ... and %d more\n", len(m.URL)-1)
			}
		}

		// Dependencies
		if len(m.Depends) > 0 {
			fmt.Printf("Depends:  %s\n", strings.Join(m.Depends, ", "))
		}

		// Suggest
		if m.Suggest != nil {
			var suggestions []string
			for feature, apps := range m.Suggest {
				suggestions = append(suggestions, fmt.Sprintf("%s -> %s", feature, strings.Join(apps, ", ")))
			}
			fmt.Printf("Suggest:  %s\n", strings.Join(suggestions, "; "))
		}

		// Environment
		if len(m.EnvAddPath) > 0 {
			fmt.Printf("Env Path: %s\n", strings.Join(m.EnvAddPath, ", "))
		}
		if len(m.EnvSet) > 0 {
			var envs []string
			for k, v := range m.EnvSet {
				envs = append(envs, fmt.Sprintf("%s=%s", k, v))
			}
			fmt.Printf("Env Set:  %s\n", strings.Join(envs, ", "))
		}

		// Notes
		if len(m.Notes) > 0 {
			fmt.Println()
			fmt.Println("Notes:")
			for _, note := range m.Notes {
				fmt.Println(note)
			}
		}

		return nil
	},
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func manifestCommitInfo(bucketName, appName string) (time.Time, string) {
	if bucketName == "" {
		return time.Time{}, ""
	}

	repoDir := bucket.Dir(bucketName)
	manifestPath := filepath.Join(bucket.ManifestDir(bucketName), appName+".json")
	updatedAt, updatedBy, err := gitutil.LastCommitInfo(repoDir, manifestPath)
	if err != nil {
		app.LogDebug("unable to read manifest commit info for %s: %v", appName, err)
		return time.Time{}, ""
	}
	return updatedAt, updatedBy
}

func installedVersions(appName string) []string {
	type installedVersion struct {
		version string
		scope   string
		global  bool
	}

	var installed []installedVersion
	for _, global := range []bool{false, true} {
		appDir := filepath.Join(app.AppDir(global), appName)
		entries, err := os.ReadDir(appDir)
		if err != nil {
			continue
		}

		scope := "user"
		if global {
			scope = "global"
		}

		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "current" {
				continue
			}
			installed = append(installed, installedVersion{
				version: entry.Name(),
				scope:   scope,
				global:  global,
			})
		}
	}

	sort.Slice(installed, func(i, j int) bool {
		if installed[i].version != installed[j].version {
			return version.Compare(installed[i].version, installed[j].version) > 0
		}
		if installed[i].global != installed[j].global {
			return !installed[i].global
		}
		return installed[i].scope < installed[j].scope
	})

	out := make([]string, 0, len(installed))
	for _, item := range installed {
		out = append(out, fmt.Sprintf("%s (%s)", item.version, item.scope))
	}
	return out
}

func init() {
	rootCmd.AddCommand(infoCmd)
	infoCmd.Flags().BoolVarP(&infoFlags.verbose, "verbose", "v", false, "Show full paths for installed app and manifest")
}
