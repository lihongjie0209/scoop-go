package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/spf13/cobra"
)

var installFlags struct {
	global     bool
	independent bool
	noCache    bool
	skipHash   bool
	arch       string
}

var installCmd = &cobra.Command{
	Use:   "install <app> [flags]",
	Short: "Install apps",
	Long: `Install an app from a manifest.

Examples:
  scoop install git
  scoop install gh@2.7.0
  scoop install -g git  (install globally)
  scoop install -a 32bit 7zip  (force 32-bit)`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		arch := install.GetArchitecture(installFlags.arch)
		global := installFlags.global
		useCache := !installFlags.noCache
		checkHash := !installFlags.skipHash

		if global {
			app.LogInfo("Global installation requested")
		}

		// Check for failed installations first
		install.EnsureNoneFailed(args)

		for _, rawApp := range args {
			appName, bucketName, version := parseAppRef(rawApp)

			// Find manifest
			m, foundBucket, err := install.FindManifest(appName)
			if err != nil {
				return err
			}
			if bucketName != "" {
				foundBucket = bucketName
			}

			// Resolve version
			versionToInstall := version
			if versionToInstall == "" {
				versionToInstall = m.Version
			}

			// Check if already installed
			for _, g := range []bool{false, true} {
				currentDir := app.AppCurrentDir(appName, g)
				if _, statErr := os.Stat(currentDir); statErr == nil && version == "" {
					if g == global {
						return &install.AlreadyInstalledError{
							App: appName, Version: versionToInstall,
						}
					}
				}
			}

			// Install
			engine := &install.Engine{
				AppName:     appName,
				Manifest:    m,
				Bucket:      foundBucket,
				Version:     versionToInstall,
				Arch:        arch,
				Global:      global,
				UseCache:    useCache,
				CheckHash:   checkHash,
				Independent: installFlags.independent,
			}

			if err := engine.Install(context.Background()); err != nil {
				if _, ok := err.(*install.AlreadyInstalledError); ok {
					app.LogWarn("%s", err.Error())
					continue
				}
				return fmt.Errorf("installing '%s': %w", appName, err)
			}

			// Show suggestions
			if m.Suggest != nil {
				install.ShowSuggestions(m.Suggest)
			}
		}

		return nil
	},
}

// parseAppRef parses "bucket/app@version" format.
func parseAppRef(ref string) (app, bucket, version string) {
	if idx := strings.LastIndex(ref, "@"); idx >= 0 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}
	if idx := strings.Index(ref, "/"); idx >= 0 {
		bucket = ref[:idx]
		app = ref[idx+1:]
	} else {
		app = ref
	}
	app = strings.TrimSuffix(app, ".json")
	return
}

func init() {
	rootCmd.AddCommand(installCmd)

	installCmd.Flags().BoolVarP(&installFlags.global, "global", "g", false, "Install globally")
	installCmd.Flags().BoolVarP(&installFlags.independent, "independent", "i", false, "Don't install dependencies automatically")
	installCmd.Flags().BoolVarP(&installFlags.noCache, "no-cache", "k", false, "Don't use cache")
	installCmd.Flags().BoolVarP(&installFlags.skipHash, "skip-hash-check", "s", false, "Skip hash check")
	installCmd.Flags().StringVarP(&installFlags.arch, "arch", "a", "", "Architecture (32bit, 64bit, arm64)")
}
