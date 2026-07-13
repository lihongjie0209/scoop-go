package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/dependency"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/spf13/cobra"
)

// confirmManifest is overridable in tests. Production prompts on stdin/stdout.
var confirmManifest = func(m *manifest.Manifest, appName string) (bool, error) {
	return install.ConfirmManifestInstall(m, appName, os.Stdin, os.Stdout)
}

var installFlags struct {
	global      bool
	independent bool
	noCache     bool
	skipHash    bool
	arch        string
}

// installTarget is one app to install after dependency expansion.
type installTarget struct {
	ref      string
	version  string
	explicit bool
}

// expandInstallTargets expands CLI args into install order.
// When independent is true, only the requested apps are returned.
// resolve is injected for tests; production passes dependency.Resolve.
func expandInstallTargets(args []string, independent bool, arch string, resolve func(string, string) ([]string, error)) ([]installTarget, error) {
	var targets []installTarget
	seen := make(map[string]bool)

	addTarget := func(ref, version string, explicit bool) {
		name := dependency.AppName(ref)
		key := name
		if version != "" {
			key = name + "@" + version
		}
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, installTarget{ref: ref, version: version, explicit: explicit})
	}

	for _, rawApp := range args {
		appName, bucketName, version := parseAppRef(rawApp)
		ref := appName
		if bucketName != "" {
			ref = bucketName + "/" + appName
		}

		if independent {
			addTarget(ref, version, true)
			continue
		}

		resolved, err := resolve(ref, arch)
		if err != nil {
			return nil, fmt.Errorf("resolving dependencies for '%s': %w", rawApp, err)
		}
		for _, dep := range resolved {
			if dependency.AppName(dep) == appName {
				addTarget(dep, version, true)
			} else {
				addTarget(dep, "", false)
			}
		}
	}
	return targets, nil
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
			if err := checkAdminRights(); err != nil {
				return fmt.Errorf("you need admin rights to install global apps")
			}
		}

		targets, err := expandInstallTargets(args, installFlags.independent, arch, dependency.Resolve)
		if err != nil {
			return err
		}

		checkNames := make([]string, 0, len(targets))
		for _, t := range targets {
			checkNames = append(checkNames, dependency.AppName(t.ref))
		}
		install.EnsureNoneFailed(checkNames)

		for _, target := range targets {
			appName, bucketName, _ := parseAppRef(target.ref)
			version := target.version

			if version == "" && dependency.IsInstalled(appName) {
				if target.explicit {
					cur := currentInstalledVersion(appName, global)
					app.LogWarn("'%s' (%s) is already installed.\nUse 'scoop update %s%s' to install a new version.",
						appName, cur, appName, globalFlagSuffix(global))
				}
				continue
			}

			// Load from preferred bucket when user specified bucket/app
			m, foundBucket, err := install.FindAvailableManifest(appName, bucketName)
			if err != nil {
				return err
			}

			versionToInstall := m.Version
			if version != "" && version != m.Version {
				m, err = install.GenerateVersionManifest(context.Background(), appName, m, version, arch, useCache)
				if err != nil {
					return err
				}
				versionToInstall = version
			}

			if version != "" {
				if cur := currentInstalledVersion(appName, global); cur == versionToInstall {
					app.LogWarn("'%s' (%s) is already installed.", appName, cur)
					continue
				}
			}

			// show_manifest config: display manifest and ask to continue
			if cfg := app.Config(); cfg != nil && install.ShouldShowManifest(cfg.Config().ShowManifest) {
				ok, err := confirmManifest(m, appName)
				if err != nil {
					return err
				}
				if !ok {
					app.LogInfo("Installation of '%s' cancelled.", appName)
					continue
				}
			}

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

			if suggest := m.GetSuggest(arch); suggest != nil {
				install.ShowSuggestions(suggest)
			}
		}

		return nil
	},
}

func currentInstalledVersion(appName string, global bool) string {
	for _, g := range []bool{global, !global} {
		p := filepath.Join(app.AppCurrentDir(appName, g), "manifest.json")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &m) == nil && m.Version != "" {
			return m.Version
		}
	}
	return "unknown"
}

func globalFlagSuffix(global bool) string {
	if global {
		return " --global"
	}
	return ""
}

// parseAppRef parses "bucket/app@version", URL, or local path.
// URLs and absolute/UNC paths are never split on '/'.
func parseAppRef(ref string) (appName, bucketName, version string) {
	// URL / UNC / absolute path — whole string is the app ref (optional .json@version)
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, `\\`) || filepath.IsAbs(ref) {
		if i := strings.LastIndex(ref, "@"); i >= 0 && strings.Contains(strings.ToLower(ref[:i]), ".json") {
			return ref[:i], "", ref[i+1:]
		}
		return ref, "", ""
	}

	if idx := strings.LastIndex(ref, "@"); idx >= 0 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}
	if idx := strings.Index(ref, "/"); idx >= 0 {
		bucketName = ref[:idx]
		appName = ref[idx+1:]
	} else {
		appName = ref
	}
	appName = strings.TrimSuffix(appName, ".json")
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
