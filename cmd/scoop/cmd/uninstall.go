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
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/proc"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/spf13/cobra"
)

var uninstallFlags struct {
	global  bool
	purge   bool
	force   bool
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <app> [app...]",
	Short: "Uninstall an app",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if uninstallFlags.global {
			if err := checkAdminRights(); err != nil {
				return fmt.Errorf("you need admin rights to uninstall global apps")
			}
		}
		var firstErr error
		for _, appName := range args {
			if err := uninstallOne(appName); err != nil {
				app.LogError("%v", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	},
}

func uninstallOne(appName string) error {
	if appName == "scoop" {
		return fmt.Errorf("use the uninstall script to remove scoop itself")
	}

	global := uninstallFlags.global
	appPath := filepath.Join(app.AppDir(global), appName)
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		if !global {
			globalPath := filepath.Join(app.Dirs().GlobalDir, "apps", appName)
			if _, err := os.Stat(globalPath); err == nil {
				return fmt.Errorf("'%s' is installed globally, use --global flag", appName)
			}
		}
		return fmt.Errorf("'%s' isn't installed", appName)
	}

	versionDir, m, installInfo := findInstallInfo(appName, global)
	if versionDir == "" {
		return fmt.Errorf("no installed version found for '%s'", appName)
	}

	version := filepath.Base(versionDir)
	app.LogInfo("Uninstalling '%s' (%s).", appName, version)

	if !uninstallFlags.force && m != nil {
		if err := checkRunningProcessesForUninstall(appName, m, installInfo.Architecture); err != nil {
			return err
		}
	}

	if m != nil {
		for _, hook := range m.GetPreUninstall(installInfo.Architecture) {
			runHook(hook, versionDir)
		}
	}

	if m != nil {
		runAppUninstaller(m, versionDir, installInfo.Architecture)
	}

	if m != nil {
		shimDir := app.ShimDir(global)
		bins := manifest.BinEntries(m.GetBin(installInfo.Architecture))
		for _, bin := range bins {
			app.LogDebug("Removing shim: %s", bin[1])
			shim.Remove(bin[1], shimDir, appName)
		}
	}

	removeAppShortcuts(appName, m, installInfo.Architecture, global)
	removePSModule(m, installInfo.Architecture, global)

	if m != nil {
		// Use the `current` symlink path — that's what was added to PATH at install time.
		currentDir := filepath.Join(app.AppDir(global), appName, "current")
		removeEnvPaths(m, currentDir, installInfo.Architecture, global)
		removeEnvSet(m, installInfo.Architecture, global)
	}

	if m != nil {
		for _, hook := range m.GetPostUninstall(installInfo.Architecture) {
			runHook(hook, versionDir)
		}
	}

	entries, _ := os.ReadDir(appPath)
	for _, e := range entries {
		if e.Name() == "current" {
			os.RemoveAll(filepath.Join(appPath, e.Name()))
		} else if !strings.HasPrefix(e.Name(), "_") {
			os.RemoveAll(filepath.Join(appPath, e.Name()))
		}
	}

	remaining, _ := os.ReadDir(appPath)
	if len(remaining) == 0 {
		os.Remove(appPath)
	}

	if uninstallFlags.purge {
		persistDir := app.PersistDir(appName, global)
		if _, err := os.Stat(persistDir); err == nil {
			app.LogInfo("Removing persisted data.")
			os.RemoveAll(persistDir)
		}
	}

	app.LogSuccess("'%s' was uninstalled.", appName)
	return nil
}

// checkRunningProcessesForUninstall checks if any of the app's binaries are currently running.
func checkRunningProcessesForUninstall(appName string, m *manifest.Manifest, arch string) error {
	cfg := app.Config()
	if cfg != nil && cfg.Config().IgnoreRunningProcesses {
		return nil
	}

	if runtime.GOOS != "windows" {
		return nil
	}

	// Prefer path-based detection under the app directory (more accurate than image name).
	appPath := filepath.Join(app.AppDir(uninstallFlags.global), appName)
	if running, names := proc.AnyRunningUnderPath(appPath, nil); running {
		return fmt.Errorf("'%s' is currently running (%s). Close it first or use --force", appName, strings.Join(names, ", "))
	}

	// Fallback: image-name check for bins (covers processes started outside app dir)
	bins := manifest.BinEntries(m.GetBin(arch))
	var images []string
	for _, bin := range bins {
		images = append(images, bin[1])
	}
	if proc.AnyRunningByImageName(images, nil) {
		return fmt.Errorf("'%s' is currently running. Close it first or use --force", appName)
	}
	return nil
}

// findInstallInfo reads the version directory, manifest, and install info.
func findInstallInfo(appName string, global bool) (string, *manifest.Manifest, installInfo) {
	var info installInfo
	appPath := filepath.Join(app.AppDir(global), appName)
	currentPath := filepath.Join(appPath, "current")

	// Try to resolve current junction
	target, err := os.Readlink(currentPath)
	if err == nil {
		versionDir := target
		if !filepath.IsAbs(versionDir) {
			versionDir = filepath.Join(appPath, versionDir)
		}

		// Read install.json
		if data, err := os.ReadFile(filepath.Join(versionDir, "install.json")); err == nil {
			info.Architecture = extractJSONValue(string(data), "architecture")
			info.Bucket = extractJSONValue(string(data), "bucket")
		}

		// Read manifest
		m, _ := manifest.ParseFile(filepath.Join(versionDir, "manifest.json"))
		return versionDir, m, info
	}

	// Fallback: scan version directories
	entries, _ := os.ReadDir(appPath)
	for _, e := range entries {
		if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
			versionDir := filepath.Join(appPath, e.Name())
			m, _ := manifest.ParseFile(filepath.Join(versionDir, "manifest.json"))
			if data, err := os.ReadFile(filepath.Join(versionDir, "install.json")); err == nil {
				info.Architecture = extractJSONValue(string(data), "architecture")
			}
			return versionDir, m, info
		}
	}

	return "", nil, info
}

type installInfo struct {
	Architecture string
	Bucket       string
}

func extractJSONValue(json, key string) string {
	search := fmt.Sprintf(`"%s":"`, key)
	idx := strings.Index(json, search)
	if idx < 0 {
		search = fmt.Sprintf(`"%s": "`, key)
		idx = strings.Index(json, search)
	}
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(json[start:], `"`)
	if end < 0 {
		return ""
	}
	return json[start : start+end]
}

func runAppUninstaller(m *manifest.Manifest, dir, arch string) {
	u := m.GetUninstaller(arch)
	if u == nil {
		return
	}

	// Run PowerShell script lines if present.
	if len(u.Script) > 0 {
		lines := make([]string, len(u.Script))
		for i, s := range u.Script {
			lines[i] = strings.ReplaceAll(s, "$dir", dir)
		}
		script := strings.Join(lines, "\n")
		app.LogInfo("Running uninstaller script")
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			app.LogWarn("Uninstaller script failed: %v", err)
		}
		return
	}

	if u.File == "" {
		return
	}

	file := filepath.Join(dir, u.File)
	if _, err := os.Stat(file); os.IsNotExist(err) {
		// May be an absolute path or on PATH.
		file = u.File
	}

	app.LogInfo("Running uninstaller: %s", u.File)

	var args []string
	for _, a := range u.Args {
		args = append(args, strings.ReplaceAll(a, "$dir", dir))
	}

	cmd := exec.Command(file, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		app.LogWarn("Uninstaller exited with error: %v", err)
	}
}

func removeAppShortcuts(appName string, m *manifest.Manifest, arch string, global bool) {
	if m == nil {
		return
	}
	shortcuts := m.GetShortcuts(arch)
	for _, s := range shortcuts {
		if len(s) < 2 {
			continue
		}
		name := s[1]
		folder := shortcutFolder(global)
		shortcutPath := filepath.Join(folder, name+".lnk")
		if err := os.Remove(shortcutPath); err != nil && !os.IsNotExist(err) {
			app.LogDebug("Failed to remove shortcut: %v", err)
		}
	}
}

func shortcutFolder(global bool) string {
	if global {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return filepath.Join(programData, `Microsoft\Windows\Start Menu\Programs\Scoop Apps`)
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\Scoop Apps`)
}

func removePSModule(m *manifest.Manifest, arch string, global bool) {
	if m == nil {
		return
	}
	psModule := m.GetPsModule(arch)
	if psModule == nil || psModule.Name == "" {
		return
	}
	modulesDir := app.Dirs().ModulesDir
	if global {
		modulesDir = filepath.Join(app.Dirs().GlobalDir, "modules")
	}
	linkPath := filepath.Join(modulesDir, psModule.Name)
	if _, err := os.Stat(linkPath); err == nil {
		app.LogDebug("Removing PS module: %s", psModule.Name)
		os.RemoveAll(linkPath)
	}
}

func removeEnvPaths(m *manifest.Manifest, dir, arch string, global bool) {
	addPath := m.GetEnvAddPath(arch)
	if len(addPath) == 0 {
		return
	}
	var fullPaths []string
	for _, p := range addPath {
		fullPaths = append(fullPaths, filepath.Join(dir, p))
	}
	env.RemovePath(fullPaths, "PATH", global)
}

func removeEnvSet(m *manifest.Manifest, arch string, global bool) {
	envSet := m.GetEnvSet(arch)
	for name := range envSet {
		env.SetEnv(name, "", global)
	}
}

func runHook(hook, dir string) {
	hook = strings.ReplaceAll(hook, "$dir", dir)
	// Execute via PowerShell if it looks like a PowerShell expression
	if strings.Contains(hook, "|") || strings.Contains(hook, "$") ||
		strings.HasPrefix(hook, "if ") || strings.Contains(hook, "foreach") ||
		strings.Contains(hook, "Remove-Item") || strings.Contains(hook, "Get-") {
		app.LogDebug("Running PowerShell hook: %s", hook)
		cmd := exec.Command("powershell.exe", "-NoProfile", "-Ex", "Unrestricted",
			"-Command", hook)
		output, err := cmd.CombinedOutput()
		if err != nil {
			app.LogWarn("Hook failed (ignored): %v\nOutput: %s", err, string(output))
		} else if len(output) > 0 {
			app.LogDebug("Hook output: %s", string(output))
		}
		return
	}

	// Simple command
	parts := strings.Fields(hook)
	if len(parts) > 0 {
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			app.LogWarn("Hook command failed (ignored): %v", err)
		}
	}
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().BoolVarP(&uninstallFlags.global, "global", "g", false, "Uninstall globally")
	uninstallCmd.Flags().BoolVarP(&uninstallFlags.purge, "purge", "p", false, "Remove persistent data")
	uninstallCmd.Flags().BoolVarP(&uninstallFlags.force, "force", "f", false, "Force uninstall even if app is running")
}
