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
	"github.com/scoopinstaller/scoop-go/pkg/dependency"
	"github.com/scoopinstaller/scoop-go/pkg/env"
	"github.com/scoopinstaller/scoop-go/pkg/gitutil"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/proc"
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
// Respects HOLD_UPDATE_UNTIL config to defer updates.
func SyncScoop(currentVersion string) error {
	cfg := app.Config()
	if cfg != nil {
		if holdUntil := cfg.Config().HoldUpdateUntil; holdUntil != "" {
			// Parse in local timezone so hold expires at local midnight, not UTC midnight.
			if t, err := time.ParseInLocation("2006-01-02", holdUntil, time.Local); err == nil {
				if time.Now().Before(t) {
					app.LogInfo("Scoop update is on hold until %s", holdUntil)
					return nil
				}
			}
		}
	}

	app.LogInfo("Checking for Scoop Go updates...")
	started, err := SelfUpdate(currentVersion)
	if err != nil {
		return err
	}
	if started {
		app.LogSuccess("Scoop Go update downloaded and verified. It will be installed when this command exits.")
	} else {
		app.LogSuccess("Scoop Go is already up to date.")
	}
	return nil
}

// SyncBuckets updates all local buckets via git pull, then incrementally
// updates the SQLite search cache when possible (full rebuild as fallback).
// Also displays per-bucket commit logs when show_update_log is enabled.
func SyncBuckets() error {
	app.LogInfo("Updating Buckets...")
	buckets := bucket.ListLocal()

	cfg := app.Config()
	showUpdateLog := cfg.Config().ShowUpdateLog
	showLog := showUpdateLog != nil && *showUpdateLog

	var changeSet db.ChangeSet
	needFullRebuild := false

	for _, b := range buckets {
		bucketDir := bucket.Dir(b.Name)
		var prevHash string
		if gitutil.IsRepo(bucketDir) {
			prevHash, _ = gitutil.HeadHash(bucketDir)
		}

		if err := bucket.Sync(b.Name); err != nil {
			app.LogWarn("Failed to update '%s' bucket: %v", b.Name, err)
		} else {
			app.LogDebug("Updated '%s' bucket", b.Name)
		}

		newHash, _ := gitutil.HeadHash(bucketDir)
		if showLog && prevHash != "" && newHash != "" && prevHash != newHash {
			app.LogInfo("Changes in '%s' bucket:", b.Name)
			displayCommitLog(bucketDir, prevHash, newHash)
		}

		if !db.IsEnabled() {
			continue
		}
		if prevHash == "" || newHash == "" {
			// Non-git or hash unavailable: re-clone style sync cannot diff
			needFullRebuild = true
			continue
		}
		if prevHash == newHash {
			continue
		}
		// Hash changed (including after re-clone). Prefer incremental name-status;
		// if history is shallow/missing, force full rebuild (do NOT treat as unchanged).
		entries, err := gitutil.NameStatus(bucketDir, prevHash, newHash)
		if err != nil {
			app.LogDebug("name-status for '%s' failed: %v; will full rebuild", b.Name, err)
			needFullRebuild = true
			continue
		}
		if len(entries) == 0 && prevHash != newHash {
			// Unexpected empty diff with different hashes — rebuild to be safe
			needFullRebuild = true
			continue
		}
		var ns []db.NameStatusChange
		for _, e := range entries {
			ns = append(ns, db.NameStatusChange{Status: e.Status, Path: e.Path, OldPath: e.OldPath})
		}
		cs := db.ChangeSetFromNameStatus(bucketDir, b.Name, ns)
		changeSet.UpsertPaths = append(changeSet.UpsertPaths, cs.UpsertPaths...)
		changeSet.Removals = append(changeSet.Removals, cs.Removals...)
	}

	if db.IsEnabled() {
		switch {
		case needFullRebuild:
			if err := db.RebuildAll(); err != nil {
				app.LogWarn("Failed to rebuild search cache: %v", err)
			}
		case len(changeSet.UpsertPaths) > 0 || len(changeSet.Removals) > 0:
			app.LogInfo("Updating search cache (%d changed, %d removed)...",
				len(changeSet.UpsertPaths), len(changeSet.Removals))
			if err := db.ApplyChanges(changeSet); err != nil {
				app.LogWarn("Incremental cache update failed: %v; falling back to full rebuild", err)
				if err := db.RebuildAll(); err != nil {
					app.LogWarn("Failed to rebuild search cache: %v", err)
				}
			}
		default:
			app.LogDebug("Search cache unchanged")
		}
	}

	return nil
}

// UpdateApp updates a single app to the latest version.
// It handles old version cleanup, running process checks, nightly versions,
// architecture detection, and delegates the actual install to install.Engine.
// When independent is false, missing dependencies of the new manifest are installed first.
func UpdateApp(ctx context.Context, appName string, global, force, quiet, independent, useCache, checkHash bool) error {
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

	// Skip held apps unless forced
	if installInfo.Hold && !force {
		if !quiet {
			app.LogWarn("Skipping '%s': app is on hold", appName)
		}
		return nil
	}

	// --- Find new manifest from bucket (never from installed copy) ---
	// PS scoop-update: re-read bucket manifest using install.json.bucket
	newManifest, foundBucket, err := install.FindAvailableManifest(appName, installBucket)
	if err != nil {
		// Fallback: any bucket if install bucket missing/removed
		newManifest, foundBucket, err = install.FindAvailableManifest(appName, "")
		if err != nil {
			return fmt.Errorf("couldn't find manifest for '%s': %w", appName, err)
		}
	}
	if installBucket == "" {
		installBucket = foundBucket
	}

	newVersion := newManifest.Version

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

	// Nightly version handling (before compare/prefetch so cache keys match install)
	isNightly := currentVersion == "nightly" || strings.HasPrefix(currentVersion, "nightly-") || newVersion == "nightly"
	if isNightly {
		if newVersion == "nightly" {
			newVersion = nightlyVersion()
			newManifest.Version = newVersion
		}
		checkHash = false
		app.LogWarn("Nightly version: hash verification disabled")
	}

	// --- Version comparison ---
	cfg := app.Config()
	updateNightly := cfg != nil && cfg.Config().UpdateNightly
	if !force && currentVersion != "" {
		// Nightly: refresh daily when update_nightly is set
		if isNightly && updateNightly && currentVersion != newVersion {
			// proceed
		} else if isNightly && !force && !updateNightly && strings.HasPrefix(currentVersion, "nightly-") {
			if !quiet {
				app.LogWarn("The latest version of '%s' (%s) is already installed.", appName, currentVersion)
			}
			return nil
		} else {
			cmp := version.Compare(currentVersion, newVersion)
			if cmp <= 0 {
				if !quiet {
					app.LogWarn("The latest version of '%s' (%s) is already installed.", appName, currentVersion)
				}
				return nil
			}
		}
	}

	// Install missing dependencies before tearing down the current app
	if !independent {
		ref := appName
		if installBucket != "" {
			ref = installBucket + "/" + appName
		}
		resolved, err := dependency.Resolve(ref, arch)
		if err != nil {
			return fmt.Errorf("resolving dependencies for '%s': %w", appName, err)
		}
		for _, dep := range dependency.Missing(resolved, appName, false) {
			depName := dependency.AppName(dep)
			depBucket := ""
			if i := strings.Index(dep, "/"); i >= 0 && !strings.Contains(dep, "://") {
				depBucket = dep[:i]
			}
			m, fb, err := install.FindAvailableManifest(depName, depBucket)
			if err != nil {
				return fmt.Errorf("dependency '%s': %w", depName, err)
			}
			if depBucket == "" {
				depBucket = fb
			}
			engine := &install.Engine{
				AppName:   depName,
				Manifest:  m,
				Bucket:    depBucket,
				Version:   m.Version,
				Arch:      arch,
				Global:    global,
				UseCache:  useCache,
				CheckHash: checkHash,
			}
			if err := engine.Install(ctx); err != nil {
				if _, ok := err.(*install.AlreadyInstalledError); ok {
					continue
				}
				return fmt.Errorf("installing dependency '%s': %w", depName, err)
			}
		}
	}

	// --- Check for running processes ---
	if !checkRunningProcesses(appDir, appName) {
		return fmt.Errorf("skipping update for '%s': application is running", appName)
	}

	app.LogInfo("Updating '%s' (%s -> %s)", appName, currentVersion, newVersion)

	// Resolve the new architecture before changing any part of the working
	// installation. An unsupported update must leave the old app untouched.
	resolvedArch := newManifest.ResolveArch(arch)
	if resolvedArch == "" {
		return fmt.Errorf("'%s' doesn't support architecture %s", appName, arch)
	}

	// Prefetch with final version string (incl. nightly-YYYYMMDD) so cache keys match Install
	app.LogInfo("Downloading new version")
	prefetchManifest := *newManifest
	prefetchManifest.Version = newVersion
	if err := PrefetchApp(ctx, appName, &prefetchManifest, resolvedArch, useCache, checkHash); err != nil {
		return fmt.Errorf("downloading new version of '%s': %w", appName, err)
	}

	// --- Old version cleanup (before installing new) ---
	// Mirrors PS: pre_uninstall → uninstaller hooks → remove integrations → post_uninstall
	rollbackCurrent := ""
	var oldPathEntries []string
	if currentManifest != nil && currentVersion != "" {
		oldVersionDir := app.AppVersionDir(appName, currentVersion, global)
		// Capture PATH entries before removing current
		for _, p := range currentManifest.GetEnvAddPath(arch) {
			oldPathEntries = append(oldPathEntries, filepath.Join(currentPath, p))
		}

		rollbackCurrent = currentPath + ".scoop-go-rollback"
		if err := prepareCurrentRollback(currentPath, rollbackCurrent); err != nil {
			return fmt.Errorf("preparing update rollback for '%s': %w", appName, err)
		}

		if err := runHooks(ctx, currentManifest.GetPreUninstall(arch), oldVersionDir); err != nil {
			app.LogWarn("Pre-uninstall hooks failed for '%s': %v", appName, err)
		}

		removeShimsForManifest(currentManifest, arch, appName, global)
		removeShortcutsForManifest(currentManifest, arch, global)
		removeEnvSetForManifest(currentManifest, arch, global)
		if len(oldPathEntries) > 0 {
			_ = env.RemovePath(oldPathEntries, app.Dirs().PathEnvVar, global)
		}

		if err := runHooks(ctx, currentManifest.GetPostUninstall(arch), oldVersionDir); err != nil {
			app.LogWarn("Post-uninstall hooks failed for '%s': %v", appName, err)
		}
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
	// Restore env_add_path entries pointing at restored current
	if m != nil {
		var paths []string
		for _, p := range m.GetEnvAddPath(arch) {
			paths = append(paths, filepath.Join(currentPath, p))
		}
		if len(paths) > 0 {
			_ = env.AddPath(paths, app.Dirs().PathEnvVar, global)
		}
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
	running, names := proc.AnyRunningUnderPath(appPath, nil)
	if running {
		app.LogWarn("'%s' is running (%s). Close it before updating, or set 'ignore_running_processes' to true in config.",
			appName, strings.Join(names, ", "))
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
