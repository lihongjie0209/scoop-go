// Package update handles updating Scoop itself, buckets, and individual apps.
// Mirrors scoop-update.ps1 from the original Scoop.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/db"
	"github.com/scoopinstaller/scoop-go/pkg/env"
	"github.com/scoopinstaller/scoop-go/pkg/gitutil"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/scoopinstaller/scoop-go/pkg/shortcut"
	"github.com/scoopinstaller/scoop-go/pkg/version"
)

// InstallInfo mirrors the install.json format stored per app version.
type InstallInfo struct {
	Architecture string `json:"architecture,omitempty"`
	URL          string `json:"url,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Hold         bool   `json:"hold,omitempty"`
}

// SyncScoop updates Scoop itself via git.
// Mirrors Sync-Scoop() from libexec/scoop-update.ps1 L64-L153.
func SyncScoop() error {
	currentDir := app.AppVersionDir("scoop", "current", false)

	cfg := app.Config()
	repo := cfg.Config().SCOOPRepo
	if repo == "" {
		repo = "https://github.com/ScoopInstaller/Scoop"
	}
	branch := cfg.Config().SCOOPBranch
	if branch == "" {
		branch = "master"
	}

	if !gitutil.IsRepo(currentDir) {
		app.LogInfo("Updating Scoop...")
		return cloneScoop(currentDir, repo)
	}

	app.LogInfo("Updating Scoop...")

	// Save previous commit for log
	prevHash, _ := gitutil.HeadHash(currentDir)

	// Check for uncommitted changes
	hasChanges, _ := gitutil.HasUncommittedChanges(currentDir)
	if hasChanges {
		app.LogWarn("Uncommitted changes detected. Skipping update (stash not supported in pure Go).")
		return nil
	}

	// Check if we need to switch branches
	currentBranch, _ := gitutil.CurrentBranch(currentDir)
	if currentBranch != "" && branch != "" && currentBranch != branch {
		app.LogWarn("Branch mismatch: current=%s, config=%s. Re-cloning...", currentBranch, branch)
		os.RemoveAll(currentDir)
		return cloneScoop(currentDir, repo)
	}

	// Pull
	if err := gitutil.Pull(currentDir); err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}

	// Show update log if config enabled
	showUpdateLog := cfg.Config().ShowUpdateLog
	if showUpdateLog != nil && *showUpdateLog {
		newHash, _ := gitutil.HeadHash(currentDir)
		if prevHash != "" && newHash != "" && prevHash != newHash {
			displayCommitLog(currentDir, prevHash, newHash)
		}
	}

	// Re-shim scoop
	shimScoop(currentDir)

	// Write last_update timestamp
	writeLastUpdate()

	return nil
}

// SyncBuckets updates all local buckets via git pull, then rebuilds the
// SQLite search cache if enabled (DB-02). Also displays per-bucket commit
// logs when show_update_log is enabled.
func SyncBuckets() error {
	app.LogInfo("Updating Buckets...")
	buckets := bucket.ListLocal()

	cfg := app.Config()
	showUpdateLog := cfg.Config().ShowUpdateLog
	showLog := showUpdateLog != nil && *showUpdateLog

	for _, b := range buckets {
		bucketDir := bucket.Dir(b.Name)
		var prevHash string
		if showLog && gitutil.IsRepo(bucketDir) {
			prevHash, _ = gitutil.HeadHash(bucketDir)
		}

		if err := bucket.Sync(b.Name); err != nil {
			app.LogWarn("Failed to update '%s' bucket: %v", b.Name, err)
		} else {
			app.LogDebug("Updated '%s' bucket", b.Name)
		}

		// Show per-bucket update log if enabled
		if showLog && gitutil.IsRepo(bucketDir) {
			newHash, _ := gitutil.HeadHash(bucketDir)
			if prevHash != "" && newHash != "" && prevHash != newHash {
				app.LogInfo("Changes in '%s' bucket:", b.Name)
				displayCommitLog(bucketDir, prevHash, newHash)
			}
		}
	}

	// Rebuild search cache after bucket sync (DB-02)
	if db.IsEnabled() {
		if err := db.RebuildAll(); err != nil {
			app.LogWarn("Failed to rebuild search cache: %v", err)
		}
	}

	return nil
}

// UpdateApp updates a single app to the latest version.
// It handles old version cleanup, running process checks, nightly versions,
// architecture detection, and delegates the actual install to install.Engine.
func UpdateApp(ctx context.Context, appName string, global, force, quiet bool, useCache, checkHash bool) error {
	appDir := app.AppDir(global)
	currentPath := filepath.Join(appDir, appName, "current")

	// --- Read current state ---
	currentVersion := ""
	var currentManifest *manifest.Manifest
	if data, err := os.ReadFile(filepath.Join(currentPath, "manifest.json")); err == nil {
		if m, err := manifest.Parse(data); err == nil {
			currentVersion = m.Version
			currentManifest = m
		}
	}

	installInfo := InstallInfo{}
	installBucket := ""
	if data, err := os.ReadFile(filepath.Join(currentPath, "install.json")); err == nil {
		if err := json.Unmarshal(data, &installInfo); err == nil {
			installBucket = installInfo.Bucket
		}
	}

	// --- Find new manifest ---
	newManifest, _, err := install.FindManifest(appName)
	if err != nil {
		return fmt.Errorf("couldn't find manifest for '%s': %w", appName, err)
	}

	newVersion := newManifest.Version

	// --- Version comparison ---
	if !force && currentVersion != "" {
		cmp := version.Compare(currentVersion, newVersion)
		if cmp >= 0 {
			if !quiet {
				app.LogWarn("The latest version of '%s' (%s) is already installed.", appName, currentVersion)
			}
			return nil
		}
	}

	// --- Determine architecture from install.json or config ---
	arch := installInfo.Architecture
	if arch == "" {
		cfg := app.Config()
		if cfg != nil && cfg.Config().DefaultArchitecture != "" {
			arch = cfg.Config().DefaultArchitecture
		} else {
			arch = "64bit"
		}
	}

	// --- Check for running processes ---
	if !checkRunningProcesses(appDir, appName) {
		return fmt.Errorf("skipping update for '%s': application is running", appName)
	}

	// --- Nightly version handling ---
	isNightly := currentVersion == "nightly" || strings.HasPrefix(currentVersion, "nightly-") || newVersion == "nightly"
	if isNightly {
		if newVersion == "nightly" {
			newVersion = nightlyVersion()
		}
		checkHash = false
		app.LogWarn("Nightly version: hash verification disabled")
	}

	app.LogInfo("Updating '%s' (%s -> %s)", appName, currentVersion, newVersion)

	// Resolve the new architecture before changing any part of the working
	// installation. An unsupported update must leave the old app untouched.
	resolvedArch := newManifest.ResolveArch(arch)
	if resolvedArch == "" {
		return fmt.Errorf("'%s' doesn't support architecture %s", appName, arch)
	}

	// --- Old version cleanup (before installing new) ---
	rollbackCurrent := ""
	if currentManifest != nil && currentVersion != "" {
		oldVersionDir := app.AppVersionDir(appName, currentVersion, global)
		rollbackCurrent = currentPath + ".scoop-go-rollback"
		if err := prepareCurrentRollback(currentPath, rollbackCurrent); err != nil {
			return fmt.Errorf("preparing update rollback for '%s': %w", appName, err)
		}

		// Run pre_uninstall hooks
		if err := runHooks(ctx, currentManifest.GetPreUninstall(arch), oldVersionDir); err != nil {
			app.LogWarn("Pre-uninstall hooks failed for '%s': %v", appName, err)
		}

		// Remove shims for old version
		removeShimsForManifest(currentManifest, arch, appName, global)

		// Remove shortcuts for old version
		removeShortcutsForManifest(currentManifest, arch, global)

		// Remove env_set entries from old version
		removeEnvSetForManifest(currentManifest, arch, global)

	}

	// --- Install new version ---
	engine := &install.Engine{
		AppName:   appName,
		Manifest:  newManifest,
		Bucket:    installBucket,
		Version:   newVersion,
		Arch:      resolvedArch,
		Global:    global,
		UseCache:  useCache,
		CheckHash: checkHash,
	}

	if err := engine.Install(ctx); err != nil {
		if rollbackCurrent != "" {
			if rollbackErr := restoreFailedUpdate(currentPath, rollbackCurrent, currentManifest, arch, appName, global); rollbackErr != nil {
				return fmt.Errorf("installing '%s': %w (rollback also failed: %v)", appName, err, rollbackErr)
			}
		}
		return fmt.Errorf("installing '%s': %w", appName, err)
	}
	if rollbackCurrent != "" {
		if err := os.RemoveAll(rollbackCurrent); err != nil {
			app.LogWarn("Failed to remove update rollback link: %v", err)
		}
	}

	// Write last_update after successful update
	writeLastUpdate()

	return nil
}

func prepareCurrentRollback(currentPath, rollbackPath string) error {
	if err := os.RemoveAll(rollbackPath); err != nil {
		return err
	}
	if _, err := os.Lstat(currentPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Rename(currentPath, rollbackPath)
}

func restoreFailedUpdate(currentPath, rollbackPath string, m *manifest.Manifest, arch, appName string, global bool) error {
	if err := os.RemoveAll(currentPath); err != nil {
		return fmt.Errorf("removing failed current link: %w", err)
	}
	if _, err := os.Lstat(rollbackPath); err != nil {
		return fmt.Errorf("rollback link is unavailable: %w", err)
	}
	if err := os.Rename(rollbackPath, currentPath); err != nil {
		return fmt.Errorf("restoring current link: %w", err)
	}

	var restoreErrors []string
	if err := restoreShimsForManifest(m, arch, currentPath, global); err != nil {
		restoreErrors = append(restoreErrors, err.Error())
	}
	if err := restoreShortcutsForManifest(m, arch, currentPath, global); err != nil {
		restoreErrors = append(restoreErrors, err.Error())
	}
	for name, value := range m.GetEnvSet(arch) {
		value = strings.ReplaceAll(value, "$dir", currentPath)
		if err := env.SetEnv(name, value, global); err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("restoring env %s: %v", name, err))
		}
	}
	if len(restoreErrors) > 0 {
		return fmt.Errorf("restoring integrations: %s", strings.Join(restoreErrors, "; "))
	}
	app.LogWarn("Update failed; restored the previous installation of '%s'.", appName)
	return nil
}

func restoreShimsForManifest(m *manifest.Manifest, arch, currentPath string, global bool) error {
	shimDir := app.ShimDir(global)
	for _, bin := range manifest.BinEntries(m.GetBin(arch)) {
		if err := shim.Create(&shim.Config{
			TargetPath: filepath.Join(currentPath, bin[0]),
			Name:       bin[1],
			Args:       bin[2],
			ShimDir:    shimDir,
			Global:     global,
		}); err != nil {
			return fmt.Errorf("restoring shim %s: %w", bin[1], err)
		}
	}
	return nil
}

func restoreShortcutsForManifest(m *manifest.Manifest, arch, currentPath string, global bool) error {
	for _, item := range m.GetShortcuts(arch) {
		if len(item) < 2 {
			continue
		}
		target := filepath.Join(currentPath, item[0])
		args, icon := "", ""
		if len(item) > 2 {
			args = item[2]
		}
		if len(item) > 3 {
			icon = filepath.Join(currentPath, item[3])
		}
		if err := shortcut.Create(&shortcut.Config{
			TargetPath: target,
			Name:       item[1],
			Arguments:  args,
			IconPath:   icon,
			WorkingDir: filepath.Dir(target),
			Global:     global,
		}); err != nil {
			return fmt.Errorf("restoring shortcut %s: %w", item[1], err)
		}
	}
	return nil
}

// cloneScoop does a fresh git clone of Scoop from the given repo URL.
func cloneScoop(targetDir, repo string) error {
	parentDir := filepath.Dir(targetDir)
	newDir := filepath.Join(parentDir, "new")
	oldDir := filepath.Join(parentDir, "old")

	if err := gitutil.Clone(gitutil.CloneOptions{URL: repo, Dest: newDir}); err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}

	if _, err := os.Stat(filepath.Join(newDir, "bin", "scoop.ps1")); os.IsNotExist(err) {
		os.RemoveAll(newDir)
		return fmt.Errorf("scoop download failed")
	}

	if _, err := os.Stat(targetDir); err == nil {
		os.RemoveAll(oldDir)
		os.Rename(targetDir, oldDir)
	}
	os.Rename(newDir, targetDir)

	return nil
}

// shimScoop creates/updates the scoop shim in the shims directory.
// It finds the currently running executable and creates a shim for it.
func shimScoop(currentDir string) {
	// Find the scoop binary path (the currently running executable)
	exePath, err := os.Executable()
	if err != nil {
		app.LogWarn("Failed to find scoop executable for shimming: %v", err)
		return
	}

	shimDir := app.ShimDir(false)
	if err := shim.Create(&shim.Config{
		TargetPath: exePath,
		Name:       "scoop",
		Args:       "",
		ShimDir:    shimDir,
		Global:     false,
	}); err != nil {
		app.LogWarn("Failed to create scoop shim: %v", err)
		return
	}

	app.LogDebug("Scoop shim updated at %s", shimDir)
}

// checkRunningProcesses checks for processes running from the app directory.
// Returns true if it's safe to update, false if running processes were found
// and the config says not to proceed (ignore_running_processes is false).
func checkRunningProcesses(appDir, appName string) bool {
	cfg := app.Config()
	if cfg != nil && cfg.Config().IgnoreRunningProcesses {
		return true
	}

	if runtime.GOOS != "windows" {
		return true
	}

	appPath := filepath.Join(appDir, appName)

	// Use PowerShell to check for running processes from this app directory
	psCmd := fmt.Sprintf(
		`Get-Process | Where-Object { $_.Path -like '%s*' } | Measure-Object | Select-Object -ExpandProperty Count`,
		strings.ReplaceAll(appPath, "'", "''"),
	)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	output, err := cmd.Output()
	if err != nil {
		// If we can't check, assume it's safe to proceed
		return true
	}

	count := strings.TrimSpace(string(output))
	if count != "0" && count != "" {
		app.LogWarn("'%s' is running. Close it before updating, or set 'ignore_running_processes' to true in config.", appName)
		return false
	}

	return true
}

// displayCommitLog shows the commit log between two hashes for a git repo.
func displayCommitLog(repoPath, oldHash, newHash string) {
	logs, err := gitutil.LogRange(repoPath, oldHash, newHash)
	if err != nil {
		app.LogDebug("Failed to get commit log: %v", err)
		return
	}
	if len(logs) > 0 {
		for _, l := range logs {
			app.LogInfo("  %s", l)
		}
	}
}

// runHooks executes manifest hook scripts (pre_uninstall, post_uninstall, etc.).
// Reuses the same logic as install.Engine.runHooks but operates in this package
// for the old-version cleanup phase of an update.
func runHooks(ctx context.Context, hooks []string, dir string) error {
	if len(hooks) == 0 {
		return nil
	}
	for _, hook := range hooks {
		// Variable substitution
		hook = strings.ReplaceAll(hook, "$dir", dir)
		hook = strings.ReplaceAll(hook, "$original_dir", dir)

		app.LogDebug("Running hook: %s", hook)

		// Execute via PowerShell if it's a PowerShell expression
		if strings.Contains(hook, "|") || strings.Contains(hook, "$") ||
			strings.HasPrefix(hook, "if ") || strings.Contains(hook, "foreach") {
			app.LogDebug("Executing PowerShell hook...")
			cmd := exec.Command("powershell.exe", "-NoProfile", "-Ex", "Unrestricted", "-Command", hook)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("PowerShell hook failed: %w\nOutput: %s", err, string(output))
			}
			if len(output) > 0 {
				app.LogDebug("Hook output: %s", string(output))
			}
			continue
		}

		// Simple commands can be run directly
		parts := strings.Fields(hook)
		if len(parts) > 0 {
			c := exec.CommandContext(ctx, parts[0], parts[1:]...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				app.LogWarn("Hook command failed (ignored): %v", err)
			}
		}
	}
	return nil
}

// removeShimsForManifest removes shims for all bin entries in a manifest.
func removeShimsForManifest(m *manifest.Manifest, arch, appName string, global bool) {
	bins := manifest.BinEntries(m.GetBin(arch))
	shimDir := app.ShimDir(global)
	for _, bin := range bins {
		name := bin[1]
		if err := shim.Remove(name, shimDir, appName); err != nil {
			app.LogWarn("Failed to remove shim '%s': %v", name, err)
		}
	}
}

// removeShortcutsForManifest removes shortcuts for all entries in a manifest.
func removeShortcutsForManifest(m *manifest.Manifest, arch string, global bool) {
	shortcuts := m.GetShortcuts(arch)
	if err := shortcut.RemoveAll(shortcuts, global); err != nil {
		app.LogWarn("Failed to remove shortcuts: %v", err)
	}
}

// removeEnvSetForManifest removes env_set entries from the manifest.
func removeEnvSetForManifest(m *manifest.Manifest, arch string, global bool) {
	envSet := m.GetEnvSet(arch)
	for name := range envSet {
		if err := env.SetEnv(name, "", global); err != nil {
			app.LogWarn("Failed to remove env var '%s': %v", name, err)
		}
	}
}

// writeLastUpdate writes the current UTC timestamp to the last_update config key.
func writeLastUpdate() {
	cfg := app.Config()
	if cfg == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := cfg.Set("last_update", now); err != nil {
		app.LogDebug("Failed to set last_update: %v", err)
		return
	}
	if err := cfg.Save(); err != nil {
		app.LogDebug("Failed to save config: %v", err)
	}
}

// nightlyVersion generates a dated nightly version string.
func nightlyVersion() string {
	return "nightly-" + time.Now().Format("20060102")
}

func shortHash(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%08x", h)[:7]
}

func extractBucket(json string) string {
	key := `"bucket":"`
	idx := strings.Index(json, key)
	if idx < 0 {
		key = `"bucket": "`
		idx = strings.Index(json, key)
	}
	if idx < 0 {
		return "main"
	}
	start := idx + len(key)
	end := strings.Index(json[start:], `"`)
	if end < 0 {
		return "main"
	}
	return json[start : start+end]
}
