package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/env"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/scoopinstaller/scoop-go/pkg/shortcut"
	"github.com/spf13/cobra"
)

var resetFlags struct {
	all   bool
	force bool
}

var resetCmd = &cobra.Command{
	Use:   "reset <app>",
	Short: "Reset an app to resolve conflicts",
	Long:  `Re-creates shims, shortcuts, PATH entries, and persisted data for an app.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !resetFlags.all {
			return fmt.Errorf("<app> missing")
		}

		var apps []appTuple
		if resetFlags.all || (len(args) == 1 && args[0] == "*") {
			for _, g := range []bool{false, true} {
				entries, _ := os.ReadDir(app.AppDir(g))
				for _, e := range entries {
					if e.IsDir() && e.Name() != "scoop" {
						apps = append(apps, appTuple{e.Name(), g})
					}
				}
			}
		} else {
			// Determine scope
			global := false
			apps = append(apps, appTuple{args[0], global})
		}

		for _, a := range apps {
			if err := resetApp(a.name, a.global); err != nil {
				app.LogError("Resetting '%s': %v", a.name, err)
			}
		}

		return nil
	},
}

func resetApp(appName string, global bool) error {
	appsDir := app.AppDir(global)
	appPath := filepath.Join(appsDir, appName)

	// Find current version
	currentPath := filepath.Join(appPath, "current")
	var version string
	if target, err := os.Readlink(currentPath); err == nil {
		version = filepath.Base(target)
	} else {
		// Find latest version directory
		entries, _ := os.ReadDir(appPath)
		for _, e := range entries {
			if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
				version = e.Name()
			}
		}
	}
	if version == "" {
		return fmt.Errorf("no installed version found")
	}

	versionDir := filepath.Join(appPath, version)
	app.LogInfo("Resetting %s (%s).", appName, version)

	// Read manifest
	m, err := manifest.ParseFile(filepath.Join(versionDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}

	// Read install info for architecture
	arch := "64bit"
	if data, err := os.ReadFile(filepath.Join(versionDir, "install.json")); err == nil {
		if a := extractJSONValue(string(data), "architecture"); a != "" {
			arch = a
		}
	}

	// Check for running processes
	if !resetFlags.force {
		if err := checkRunningProcesses(appName, global, m, arch); err != nil {
			return err
		}
	}

	// Create current link
	os.Remove(currentPath)
	os.Symlink(version, currentPath)

	// Clean old PATH entries before adding new
	addPath := m.GetEnvAddPath(arch)
	if len(addPath) > 0 {
		var oldPaths []string
		for _, p := range addPath {
			oldPaths = append(oldPaths, filepath.Join(versionDir, p))
		}
		env.RemovePath(oldPaths, "PATH", global)
	}

	// Create shims
	shimDir := app.ShimDir(global)
	bins := manifest.BinEntries(m.GetBin(arch))
	for _, bin := range bins {
		target := filepath.Join(versionDir, bin[0])
		if _, err := os.Stat(target); err == nil {
			shim.Create(&shim.Config{
				TargetPath: target,
				Name:       bin[1],
				Args:       bin[2],
				ShimDir:    shimDir,
				Global:     global,
			})
		}
	}

	// Re-create shortcuts
	shortcuts := m.GetShortcuts(arch)
	for _, s := range shortcuts {
		if len(s) < 2 {
			continue
		}
		target := filepath.Join(versionDir, s[0])
		name := s[1]
		args := ""
		iconPath := ""
		if len(s) >= 3 {
			args = s[2]
		}
		if len(s) >= 4 {
			iconPath = filepath.Join(versionDir, s[3])
		}
		shortcut.Create(&shortcut.Config{
			TargetPath: target,
			Name:       name,
			Arguments:  args,
			IconPath:   iconPath,
			WorkingDir: filepath.Dir(target),
			Global:     global,
		})
	}

	// Re-add PATH
	if len(addPath) > 0 {
		var paths []string
		for _, p := range addPath {
			paths = append(paths, filepath.Join(versionDir, p))
		}
		env.AddPath(paths, "PATH", global)
	}

	// Re-set env
	for k, v := range m.GetEnvSet(arch) {
		v = replaceVars(v, versionDir, version)
		env.SetEnv(k, v, global)
	}

	// Re-create persist data links
	if err := install.PersistData(appName, global, m, versionDir); err != nil {
		app.LogWarn("Persisting data: %v", err)
	}

	app.LogSuccess("%s was reset.", appName)
	return nil
}

// checkRunningProcesses checks if any of the app's binaries are currently running.
func checkRunningProcesses(appName string, global bool, m *manifest.Manifest, arch string) error {
	cfg := app.Config()
	if cfg != nil && cfg.Config().IgnoreRunningProcesses {
		return nil
	}

	if runtime.GOOS != "windows" {
		return nil
	}

	bins := manifest.BinEntries(m.GetBin(arch))
	for _, bin := range bins {
		name := bin[1]
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s.exe", name), "/NH")
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(output), name+".exe") {
			return fmt.Errorf("'%s' is currently running. Close it first or use --force", name)
		}
	}

	return nil
}

func replaceVars(s, dir, version string) string {
	s = strings.ReplaceAll(s, "$dir", dir)
	s = strings.ReplaceAll(s, "$version", version)
	return s
}

func init() {
	rootCmd.AddCommand(resetCmd)
	resetCmd.Flags().BoolVarP(&resetFlags.all, "all", "a", false, "Reset all apps")
	resetCmd.Flags().BoolVarP(&resetFlags.force, "force", "f", false, "Force reset even if app is running")
}
