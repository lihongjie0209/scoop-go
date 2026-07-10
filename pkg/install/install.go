// Package install handles the app installation lifecycle.
// It mirrors lib/install.ps1 from the original Scoop.
package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/config"
	"github.com/scoopinstaller/scoop-go/pkg/download"
	"github.com/scoopinstaller/scoop-go/pkg/env"
	"github.com/scoopinstaller/scoop-go/pkg/extract"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/scoopinstaller/scoop-go/pkg/shortcut"
)

// Engine orchestrates the full installation process.
type Engine struct {
	AppName     string
	Manifest    *manifest.Manifest
	Bucket      string
	Version     string
	Arch        string
	Global      bool
	UseCache    bool
	CheckHash   bool
	Independent bool
}

// AlreadyInstalledError indicates the app is already installed.
type AlreadyInstalledError struct {
	App     string
	Version string
}

func (e *AlreadyInstalledError) Error() string {
	return fmt.Sprintf("'%s' (%s) is already installed", e.App, e.Version)
}

// Install runs the full installation pipeline.
// Mirrors install_app() from lib/install.ps1.
func (e *Engine) Install(ctx context.Context) error {
	m := e.Manifest
	appName := e.AppName
	version := e.Version

	// Validate version
	if version == "" {
		return fmt.Errorf("manifest doesn't specify a version")
	}

	// Nightly version handling
	isNightly := e.Version == "nightly"
	if isNightly {
		version = nightlyVersion()
		e.CheckHash = false
		app.LogWarn("This is a nightly version. Downloaded files won't be verified.")
	}

	// Architecture support check
	supportedArch := m.ResolveArch(e.Arch)
	if supportedArch == "" {
		return fmt.Errorf("'%s' doesn't support architecture %s", appName, e.Arch)
	}
	e.Arch = supportedArch

	app.LogInfo("Installing '%s' (%s) [%s]", appName, version, e.Arch)

	// Step 1: Create version directory
	versionDir := app.AppVersionDir(appName, version, e.Global)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("creating version directory: %w", err)
	}
	originalDir := versionDir

	// Step 2: Download files
	downloadedFiles, err := e.downloadFiles(ctx, m, versionDir)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Step 3: Extract archives
 if err := e.extractFiles(ctx, m, versionDir, downloadedFiles); err != nil {
		// Extraction failed -- remove cache files so retry re-downloads
		for _, f := range downloadedFiles {
			cacheKey := fmt.Sprintf("%s#%s#%s", e.AppName, e.Version, shortHash(f))
			cachedPath := filepath.Join(app.Dirs().CacheDir, cacheKey)
			os.Remove(cachedPath)
			app.LogDebug("Removed cache entry for corrupted download: %s", filepath.Base(f))
		}
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Step 4: Run pre_install hooks
	if err := e.runHooks(ctx, m.GetPreInstall(e.Arch), versionDir); err != nil {
		return fmt.Errorf("pre_install hook failed: %w", err)
	}

	// Step 5: Run installer (if configured)
	if err := e.runInstaller(ctx, m, versionDir, downloadedFiles); err != nil {
		return fmt.Errorf("installer failed: %w", err)
	}

	// Step 6: Create current version link
	currentDir, err := e.linkCurrent(versionDir)
	if err != nil {
		return fmt.Errorf("linking current: %w", err)
	}

	// Step 7a: Create shims for all binaries
	if err := e.createShims(m, currentDir); err != nil {
		return fmt.Errorf("creating shims: %w", err)
	}

	// Step 7b: Create start menu shortcuts
	if err := e.createShortcuts(m, currentDir); err != nil {
		return fmt.Errorf("creating shortcuts: %w", err)
	}

	// Step 7c: Install PowerShell module
	if err := e.installPSModule(m, currentDir); err != nil {
		return fmt.Errorf("installing PS module: %w", err)
	}

	// Step 7d: Add PATH entries
	if err := e.envAddPath(m, currentDir); err != nil {
		return fmt.Errorf("adding PATH: %w", err)
	}

	// Step 7e: Set environment variables
	if err := e.envSet(m); err != nil {
		return fmt.Errorf("setting env: %w", err)
	}

	// Step 8: Persist data
	if err := e.persistData(ctx, m, originalDir); err != nil {
		return fmt.Errorf("persisting data: %w", err)
	}

	// Step 9: Run post_install hooks
	if err := e.runHooks(ctx, m.GetPostInstall(e.Arch), versionDir); err != nil {
		return fmt.Errorf("post_install hook failed: %w", err)
	}

	// Save install info + manifest
	e.saveInstallInfo(versionDir)
	e.saveManifest(versionDir)

	// Show notes
	if len(m.Notes) > 0 {
		fmt.Println()
		app.LogInfo("Notes")
		app.LogInfo("-----")
		for _, note := range m.Notes {
			fmt.Println(note)
		}
	}

	app.LogSuccess("'%s' (%s) was installed successfully!", appName, version)
	_ = currentDir

	return nil
}

// downloadFiles downloads all URLs defined in the manifest with hash verification.
func (e *Engine) downloadFiles(ctx context.Context, m *manifest.Manifest, destDir string) ([]string, error) {
	urls := m.GetURL(e.Arch)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs defined for architecture %s", e.Arch)
	}

	downloaded := make([]string, 0, len(urls))

	for _, url := range urls {
		fname := manifest.URLFilename(url)
		targetPath := filepath.Join(destDir, fname)

		expectedHash := ""
		if e.CheckHash {
			expectedHash = m.HashForURL(url, e.Arch)
		}

		cfg := &download.Config{
			URL:          url,
			Destination:  targetPath,
			CacheDir:     app.Dirs().CacheDir,
			CacheKey:     fmt.Sprintf("%s#%s#%s", e.AppName, e.Version, shortHash(url)),
			UseCache:     e.UseCache,
			Cookies:      m.Cookie,
			ExpectedHash: expectedHash,
			GithubToken:  app.Config().Config().GH_TOKEN,
			Proxy:        app.Config().Config().Proxy,
			Headers:      matchingPrivateHeaders(app.Config().Config().PrivateHosts, url),
		}
dl := download.NewDownloader(cfg)
	result, err := dl.Download(ctx)
	if err != nil {
		return downloaded, fmt.Errorf("downloading %s: %w", url, err)
	}
	// Cache hit returns cache path — ensure destination file exists
	if result.FromCache && result.Path != targetPath {
		if _, statErr := os.Stat(targetPath); os.IsNotExist(statErr) {
			if copyErr := copyFile(targetPath, result.Path); copyErr != nil {
				app.LogDebug("Cache-to-destination copy failed: %v", copyErr)
			}
		}
	}
	downloaded = append(downloaded, targetPath)
	}

	return downloaded, nil
}

// matchingPrivateHeaders returns headers from private_hosts rules that match the given URL.
// Each rule's Headers field can be:
//   - A JSON object: {"X-Api-Key": "abc123"}
//   - Comma-separated "Key: Value" pairs: "X-Api-Key: abc123, Authorization: Bearer xyz"
func matchingPrivateHeaders(rules []config.PrivateHostRule, rawURL string) map[string]string {
	if len(rules) == 0 {
		return nil
	}
	for _, rule := range rules {
		if rule.Match != "" && strings.Contains(rawURL, rule.Match) {
			headers := make(map[string]string)
			parsePrivateHostHeaders(rule.Headers, headers)
			return headers
		}
	}
	return nil
}

// parsePrivateHostHeaders parses private host header strings into a map.
// Supports JSON object format and comma-separated "Key: Value" pairs.
func parsePrivateHostHeaders(raw string, target map[string]string) {
	if raw == "" {
		return
	}
	trimmed := strings.TrimSpace(raw)
	// Try JSON object format: {"Key": "Value"}
	if strings.HasPrefix(trimmed, "{") {
		var m map[string]string
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil {
			for k, v := range m {
				target[k] = v
			}
			return
		}
	}
	// Parse as comma-separated "Key: Value" pairs
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, ":"); idx >= 0 {
			key := strings.TrimSpace(part[:idx])
			val := strings.TrimSpace(part[idx+1:])
			target[key] = val
		}
	}
}

// extractFiles extracts downloaded archives based on their types.
func (e *Engine) extractFiles(ctx context.Context, m *manifest.Manifest, destDir string, files []string) error {
	for _, file := range files {
		var extractor extract.Extractor

		// Route .exe files based on manifest configuration and file detection:
		//   innosetup=true    -> InnoExtractor (innounp)
		//   IsWixInstaller    -> WixExtractor (dark)
		//   otherwise         -> not an archive, skip extraction
		if strings.HasSuffix(strings.ToLower(file), ".exe") {
			if m.InnoSetup {
				app.LogDebug("Using InnoExtractor for %s (innosetup=true)", filepath.Base(file))
				extractor = &extract.InnoExtractor{}
			} else if extract.IsWixInstaller(file) {
				app.LogDebug("Using WixExtractor for %s (WiX bundle detected)", filepath.Base(file))
				extractor = &extract.WixExtractor{}
			} else {
				app.LogDebug("Skipping extraction for %s (not a supported archive type)", filepath.Base(file))
				continue
			}
		} else {
			extractor = extract.DetectExtractor(file)
		}

		// Read extract_to from manifest; it specifies a subdirectory within
		// the version directory where archive contents should be placed.
		extractTo := normalizeExtractDir(m.GetExtractTo(e.Arch))

		effectiveDest := destDir
		if extractTo != "" {
			effectiveDest = filepath.Join(destDir, extractTo)
		}

		cfg := &extract.Config{
			Source:      file,
			Destination: effectiveDest,
			ExtractDir:  normalizeExtractDir(m.GetExtractDir(e.Arch)),
			ExtractTo:   extractTo,
			RemoveSrc:   true,
		}

		result, err := extractor.Extract(cfg)
		if err != nil {
			// Skip non-archive files (they're stand-alone executables/installers)
			if _, ok := extractor.(*extract.UnknownExtractor); ok {
				app.LogDebug("Skipping extraction for %s (not an archive)", filepath.Base(file))
				continue
			}
			if strings.Contains(err.Error(), "not an archive") {
				app.LogDebug("Skipping extraction for %s: %v", filepath.Base(file), err)
				continue
			}
			return fmt.Errorf("extracting %s: %w", filepath.Base(file), err)
		}
		_ = result
	}
	return nil
}

// runHooks executes manifest hook scripts (pre_install, post_install, etc.).
// Supports inline PowerShell execution and simple commands.
func (e *Engine) runHooks(ctx context.Context, hooks []string, dir string) error {
	// Join all hooks into a single PowerShell script if any contain PS syntax.
	// Manifest hooks can be multi-line (e.g., ForEach-Object blocks spanning
	// multiple array elements) and must be executed as one script.
	if hasPowerShellHooks(hooks) {
		return e.runPowerShellScript(hooks, dir)
	}

	// Simple commands — run each directly
	for _, hook := range hooks {
		simpleHook := hook
		simpleHook = strings.ReplaceAll(simpleHook, "$dir", dir)
		simpleHook = strings.ReplaceAll(simpleHook, "$original_dir", dir)
		simpleHook = strings.ReplaceAll(simpleHook, "$version", e.Version)
		simpleHook = strings.ReplaceAll(simpleHook, "$global", fmt.Sprintf("%v", e.Global))

		parts := strings.Fields(simpleHook)
		if len(parts) > 0 {
			cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				app.LogWarn("Hook command failed (ignored): %v", err)
			}
		}
	}
	return nil
}

// runPowerShellScript joins all hooks into a single PowerShell script and executes it.
func (e *Engine) runPowerShellScript(hooks []string, dir string) error {
		// Build a single PowerShell script from all hook lines.
		// Variables like $dir, $version are defined in the PS preamble so
		// PowerShell resolves them natively within double-quoted strings.
		fullScript := strings.Join(hooks, "\n")
		app.LogDebug("Executing PowerShell hook script:\n%s", fullScript)

		// Set Scoop PowerShell variables (PS runs hooks in-session, Go spawns new process)
		psDir := strings.ReplaceAll(dir, "'", "''")
		scoopDir := strings.ReplaceAll(app.Dirs().ScoopDir, "'", "''")
		bucketsDir := strings.ReplaceAll(app.Dirs().BucketsDir, "'", "''")
		appsDir := strings.ReplaceAll(app.Dirs().AppsDir, "'", "''")
		cacheDir := strings.ReplaceAll(app.Dirs().CacheDir, "'", "''")
		shimsDir := strings.ReplaceAll(app.Dirs().ShimsDir, "'", "''")
		persistDataDir := strings.ReplaceAll(app.PersistDir(e.AppName, e.Global), "'", "''")
		bucketName := e.Bucket
		psGlobal := "$false"
		if e.Global { psGlobal = "$true" }
		psCmd := fmt.Sprintf(
			"$OutputEncoding = [Console]::OutputEncoding = [System.Text.Encoding]::UTF8;"+
			" $dir = '%s';"+
			" $original_dir = '%s';"+
			" $version = '%s';"+
			" $global = %s;"+
			" $scoopdir = '%s';"+
			" $bucketsdir = '%s';"+
			" $appsdir = '%s';"+
			" $cachedir = '%s';"+
			" $shimsdir = '%s';"+
			" $persist_dir = '%s';"+
			" $bucket = '%s';"+
			" $architecture = '%s';"+
			" $app = '%s';"+
			" %s",
		psDir, psDir, e.Version, psGlobal,
		scoopDir, bucketsDir, appsDir, cacheDir, shimsDir,
		persistDataDir, bucketName, e.Arch, e.AppName, fullScript)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Ex", "Unrestricted",
		"-Command", psCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell hook failed: %w\nOutput: %s", err, string(output))
	}
	if len(output) > 0 {
		app.LogDebug("Hook output: %s", string(output))
	}
	return nil
}
// hasPowerShellHooks checks if any hook in the list contains PowerShell syntax.
func hasPowerShellHooks(hooks []string) bool {
	for _, hook := range hooks {
		if isPowerShellHook(hook) {
			return true
		}
	}
	return false
}

// isPowerShellHook detects if a hook script contains PowerShell syntax.
func isPowerShellHook(hook string) bool {
	return strings.Contains(hook, "|") ||
		strings.Contains(hook, "$") ||
		strings.HasPrefix(hook, "if ") ||
		strings.Contains(hook, "foreach") ||
		strings.Contains(hook, "-replace") ||
		strings.Contains(hook, "-match")
}


// runInstaller runs the application's installer if configured.
// Handles installer.file (with fallback to first downloaded URL's filename),
// installer.script (PowerShell commands), and installer.keep (file removal).
func (e *Engine) runInstaller(ctx context.Context, m *manifest.Manifest, dir string, files []string) error {
	inst := m.GetInstaller(e.Arch)
	if inst == nil {
		return nil // No installer configured
	}

	// Variable substitution values used for both script and file installer args
	substitutions := map[string]string{
		"$dir":     dir,
		"$global":  fmt.Sprintf("%v", e.Global),
		"$version": e.Version,
	}

	// --- Handle installer.script ---
	// installer.script is an array of PowerShell commands that replaces (or
	// precedes) a file-based installer. Each command supports $dir, $version,
	// and $global variable substitution.
	if len(inst.Script) > 0 {
		for i, line := range inst.Script {
			for k, v := range substitutions {
				line = strings.ReplaceAll(line, k, v)
			}
			app.LogDebug("Running installer script [%d/%d]: %s", i+1, len(inst.Script), line)
			cmd := exec.Command("powershell.exe", "-NoProfile", "-Ex", "Unrestricted", "-Command", line)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("installer script failed: %w\nOutput: %s", err, string(output))
			}
			if len(output) > 0 {
				fmt.Print(string(output))
			}
		}
		// Script-only installers have no file to clean up
		if inst.File == "" {
			return nil
		}
	}

	// --- Determine installer file ---
	installerFile := inst.File
	if installerFile == "" && len(files) > 0 {
		// INS-009: Fall back to first downloaded URL's filename
		installerFile = filepath.Base(files[0])
	}

	if installerFile == "" {
		return nil // Nothing to run
	}

	// Resolve installer path
	var installerPath string
	if filepath.IsAbs(installerFile) {
		installerPath = installerFile
	} else {
		installerPath = filepath.Join(dir, installerFile)
	}

	// Check file existence before attempting to run
	if _, err := os.Stat(installerPath); os.IsNotExist(err) {
		return fmt.Errorf("installer not found: %s", installerPath)
	}

	// Variable substitution for args
	args := make([]string, len(inst.Args))
	for i, arg := range inst.Args {
		for k, v := range substitutions {
			arg = strings.ReplaceAll(arg, k, v)
		}
		args[i] = arg
	}

	app.LogInfo("Running installer: %s %s", installerFile, strings.Join(args, " "))

	// For .ps1 installers
	if strings.HasSuffix(installerFile, ".ps1") {
		psArgs := append([]string{"-NoProfile", "-Ex", "Unrestricted", "-File", installerPath}, args...)
		cmd := exec.Command("powershell.exe", psArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("PowerShell installer failed: %w\nOutput: %s", err, string(output))
		}
		if len(output) > 0 {
			fmt.Print(string(output))
		}
		// INS-022: Remove installer file after install (INS-020: unless keep is true)
		if !inst.Keep {
			e.removeInstallerFile(installerPath)
		}
		return nil
	}

	// For executable installers (.exe, .msi, .bat, .cmd)
	if isExecutableExt(installerFile) {
		cmd := exec.CommandContext(ctx, installerPath, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("installer exited with error: %w", err)
		}
		// INS-022: Remove installer file after install (INS-020: unless keep is true)
		if !inst.Keep {
			e.removeInstallerFile(installerPath)
		}
		return nil
	}

	return nil
}

// removeInstallerFile removes an installer file after a successful installation.
// Logs a debug message on failure but does not return an error, since the
// installation itself succeeded.
func (e *Engine) removeInstallerFile(path string) {
	if err := os.Remove(path); err != nil {
		app.LogDebug("Failed to remove installer file %s: %v", path, err)
	}
}

// isExecutableExt returns true for file extensions that indicate an executable
// installer (beyond .exe which is handled directly).
func isExecutableExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".msi", ".bat", ".cmd", ".com":
		return true
	}
	return false
}

// linkCurrent creates a "current" directory junction pointing to the version directory.
// On Windows: creates a reparse point (junction) using mklink /J (no admin privileges needed).
// On non-Windows: creates a symlink via os.Symlink.
// When NO_JUNCTION config is set: returns versionDir as-is.
func (e *Engine) linkCurrent(versionDir string) (string, error) {
	if e.isNoJunction() {
		return versionDir, nil
	}

	currentDir := filepath.Join(filepath.Dir(versionDir), "current")
	if currentDir == versionDir {
		return versionDir, nil
	}

	// Remove existing current directory if present
	if _, err := os.Stat(currentDir); err == nil {
		os.RemoveAll(currentDir)
	}

	// Create junction (Windows) or symlink (other platforms)
	if err := createJunction(currentDir, versionDir); err != nil {
		// Fallback: just return versionDir without junction
		app.LogWarn("Could not create current link: %v", err)
		return versionDir, nil
	}

	// Protect the junction with read-only attribute (Windows only)
	// This prevents accidental deletion of the reparse point itself.
	if runtime.GOOS == "windows" {
		if err := setJunctionReadOnly(currentDir); err != nil {
			app.LogDebug("Failed to set read-only on current junction: %v", err)
		}
	}

	return currentDir, nil
}

func (e *Engine) isNoJunction() bool {
	cfg := app.Config()
	if cfg == nil {
		return false
	}
	return cfg.Config().NoJunction
}

// persistData creates junctions/hardlinks for persistent data.
// Mirrors persist_data() from lib/install.ps1 L445-531.
//
// Uses createPersistLink which selects the right strategy per item:
//   - Directories: creates a junction (Windows mklink /J) or os.Symlink (other platforms)
//   - Files: creates a hard link via os.Link with file copy fallback
//   - When NO_JUNCTION is set: copies files/directories instead of linking
func (e *Engine) persistData(ctx context.Context, m *manifest.Manifest, originalDir string) error {
	persist := m.Persist
	if persist == nil {
		return nil
	}

	persistBase := app.PersistDir(e.AppName, e.Global)
	if err := os.MkdirAll(persistBase, 0755); err != nil {
		return err
	}

	// Normalize persist items
	var items [][2]string
	switch p := persist.(type) {
	case string:
		items = [][2]string{{p, p}}
	case []interface{}:
		for _, item := range p {
			switch v := item.(type) {
			case string:
				items = append(items, [2]string{v, v})
			case []interface{}:
				if len(v) >= 2 {
					src, _ := v[0].(string)
					dst, _ := v[1].(string)
					items = append(items, [2]string{src, dst})
				}
			}
		}
	}

	for _, pair := range items {
		source := strings.TrimRight(pair[0], "/\\")
		target := strings.TrimRight(pair[1], "/\\")
		sourcePath := filepath.Join(originalDir, source)
		targetPath := filepath.Join(persistBase, target)
		noJunction := e.isNoJunction()

		app.LogDebug("Persisting %s -> %s", source, target)

		// Determine if this persist item is a directory
		isDir := e.isPersistDir(sourcePath, targetPath)

		if _, err := os.Stat(targetPath); err == nil {
			// Persist data exists — create link back to it
			if _, err := os.Stat(sourcePath); err == nil {
				// Backup existing source
				backupPath := sourcePath + ".original"
				os.Rename(sourcePath, backupPath)
			}

			if noJunction {
				// Copy persisted data back to the version directory
				if err := copyPersistData(sourcePath, targetPath, isDir); err != nil {
					app.LogWarn("Failed to restore persist data: %v", err)
				}
			} else {
				// Create junction/hardlink from source to persist target
				if err := e.createPersistLink(sourcePath, targetPath, isDir); err != nil {
					app.LogWarn("Failed to link persist data: %v", err)
				}
			}
		} else if _, err := os.Stat(sourcePath); err == nil {
			// Move source to persist dir, then link
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}

			if noJunction {
				// Copy to persist dir, leaving source in place
				if err := copyPersistData(targetPath, sourcePath, isDir); err != nil {
					return fmt.Errorf("copying persist data: %w", err)
				}
			} else {
				// Move source to persist dir, then create link back.
				// Use copy+delete fallback on EXDEV (cross-device rename).
				if err := os.Rename(sourcePath, targetPath); err != nil {
					app.LogDebug("Rename failed, falling back to copy+delete: %v", err)
					if err := copyPersistData(targetPath, sourcePath, isDir); err != nil {
						return fmt.Errorf("copying persist data (rename failed): %w", err)
					}
					if err := os.RemoveAll(sourcePath); err != nil {
						return fmt.Errorf("removing source after copy: %w", err)
					}
				}
				// Create link back from source to target
				if err := e.createPersistLink(sourcePath, targetPath, isDir); err != nil {
					app.LogWarn("Failed to link persist data: %v", err)
				}
			}
		} else {
			// Neither exists — create empty directory in persist dir
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("creating persist directory: %w", err)
			}
		}
	}

	return nil
}

// saveInstallInfo saves installation metadata to install.json.
func (e *Engine) saveInstallInfo(versionDir string) {
	info := map[string]interface{}{
		"architecture": e.Arch,
		"bucket":       e.Bucket,
		"hold":         false,
	}
	if e.Bucket == "" && strings.HasPrefix(e.AppName, "http") {
		info["url"] = e.AppName
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		app.LogDebug("Failed to marshal install info: %v", err)
		return
	}

	path := filepath.Join(versionDir, "install.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		app.LogDebug("Failed to write install.json: %v", err)
	}
}

// saveManifest saves a copy of the manifest to the version directory.
func (e *Engine) saveManifest(versionDir string) {
	// Serialize manifest back to JSON
	data, err := json.MarshalIndent(e.Manifest, "", "  ")
	if err != nil {
		app.LogDebug("Failed to marshal manifest: %v", err)
		return
	}

	path := filepath.Join(versionDir, "manifest.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		app.LogDebug("Failed to write manifest.json: %v", err)
	}
}

// --- Public helpers ---

// EnsureNoneFailed checks for failed installations and repairs them.
// Mirrors ensure_none_failed() from lib/install.ps1.
func EnsureNoneFailed(apps []string) {
	for _, appName := range apps {
		appName = strings.TrimSuffix(appName, ".json")
		if idx := strings.Index(appName, "/"); idx >= 0 {
			appName = appName[idx+1:]
		}

		for _, global := range []bool{false, true} {
			currentPath := app.AppCurrentDir(appName, global)
			_, err := os.Stat(currentPath)
			appDir := filepath.Dir(currentPath)

			if _, dirErr := os.Stat(appDir); dirErr == nil && err != nil {
				// App directory exists but no "current" — failed installation
				if _, verErr := os.Stat(filepath.Join(appDir, "current")); verErr != nil {
					// Check if there's a version directory with install.json
					entries, _ := os.ReadDir(appDir)
					hasVersion := false
					for _, e := range entries {
						if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
							installPath := filepath.Join(appDir, e.Name(), "install.json")
							if _, err := os.Stat(installPath); err == nil {
								hasVersion = true
								break
							}
						}
					}
					if hasVersion {
						app.LogInfo("Repair previous failed installation of %s.", appName)
						// Reset the app (re-create current link, shims, etc.)
						resetApp(appName, global)
					} else {
						app.LogWarn("Purging previous failed installation of %s.", appName)
						os.RemoveAll(appDir)
					}
				}
			}
		}
	}
}

func resetApp(appName string, global bool) {
	// Find the installed version
	appDir := app.AppDir(global)
	appPath := filepath.Join(appDir, appName)
	entries, _ := os.ReadDir(appPath)
	var version string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
			version = e.Name()
			break
		}
	}
	if version == "" {
		return
	}
	versionDir := filepath.Join(appPath, version)

	// Create "current" junction
	currentPath := filepath.Join(appPath, "current")
	os.RemoveAll(currentPath)
	if err := createJunction(currentPath, versionDir); err != nil {
		app.LogWarn("Failed to create junction for %s: %v", appName, err)
	}

	app.LogSuccess("%s was reset to version %s", appName, version)
}

// ShowSuggestions displays suggested packages after install.
// Mirrors show_suggestions() from lib/install.ps1.
func ShowSuggestions(suggest map[string]manifest.FlexibleStrings) {
	if len(suggest) == 0 {
		return
	}

	// Collect installed apps
	isInstalled := func(name string) bool {
		for _, global := range []bool{false, true} {
			currentPath := app.AppCurrentDir(name, global)
			if _, err := os.Stat(currentPath); err == nil {
				return true
			}
		}
		return false
	}

	for feature, suggestions := range suggest {
		fulfilled := false
		for _, s := range suggestions {
			depApp := strings.Split(s, "/")[0]
			depApp = strings.TrimSuffix(depApp, ".json")
			if isInstalled(depApp) {
				fulfilled = true
				break
			}
		}

		if !fulfilled {
			app.LogInfo("'%s' suggests installing '%s'.",
				feature, strings.Join(suggestions, "' or '"))
		}
	}
}

// BucketForApp finds which local bucket contains the given app's manifest.
func BucketForApp(appName string) string {
	bucketName, _ := bucket.AppManifestPath(appName)
	return bucketName
}

// FindManifest locates a manifest by app name across all local buckets,
// or by downloading directly from a URL.
func FindManifest(appName string) (*manifest.Manifest, string, error) {
	appName = strings.TrimSuffix(appName, ".json")

	// URL manifest: download directly if appName is a URL
	if strings.HasPrefix(appName, "http://") || strings.HasPrefix(appName, "https://") {
		return loadManifestFromURL(appName)
	}

	// Check installed version first
	for _, global := range []bool{false, true} {
		manifestPath := filepath.Join(app.AppCurrentDir(appName, global), "manifest.json")
		if data, err := os.ReadFile(manifestPath); err == nil {
			m, err := manifest.Parse(data)
			if err == nil {
				return m, "", nil
			}
		}
	}

	// Search buckets
	bucketName, manifestPath := bucket.AppManifestPath(appName)
	if manifestPath != "" {
		m, err := manifest.ParseFile(manifestPath)
		if err != nil {
			return nil, "", err
		}
		return m, bucketName, nil
	}

	return nil, "", fmt.Errorf("couldn't find manifest for '%s'", appName)
}

// loadManifestFromURL downloads a manifest JSON from a URL and parses it.
func loadManifestFromURL(rawURL string) (*manifest.Manifest, string, error) {
	resp, err := http.Get(rawURL) //nolint:noctx
	if err != nil {
		return nil, "", fmt.Errorf("downloading manifest from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("downloading manifest from URL: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading manifest response: %w", err)
	}

	m, err := manifest.Parse(data)
	if err != nil {
		return nil, "", fmt.Errorf("parsing manifest from URL: %w", err)
	}

	return m, "URL", nil
}

// --- Helpers ---

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

func normalizeExtractDir(dir any) string {
	if dir == nil {
		return ""
	}
	switch v := dir.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// EnsureDir creates a directory and its parents.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// PersistData creates or recreates persist data links for an installed app.
// This is used by commands like reset to re-establish persist links without
// going through the full installation pipeline.
func PersistData(appName string, global bool, m *manifest.Manifest, versionDir string) error {
	if m.Persist == nil {
		return nil
	}
	e := &Engine{
		AppName: appName,
		Global:  global,
	}
	return e.persistData(context.Background(), m, versionDir)
}

// GetArchitecture resolves the default architecture from config or system.
func GetArchitecture(cfgArch string) string {
	if cfgArch != "" {
		return cfgArch
	}
	return "64bit"
}

// createShims creates shims for all bin entries in the manifest.
func (e *Engine) createShims(m *manifest.Manifest, dir string) error {
	bins := manifest.BinEntries(m.GetBin(e.Arch))
	if len(bins) == 0 {
		return nil
	}

	shimDir := app.ShimDir(e.Global)
	app.LogDebug("Creating shims in %s", shimDir)

	for _, bin := range bins {
		target := bin[0]
		name := bin[1]
		args := bin[2]

		// Resolve target path
		targetPath := filepath.Join(dir, target)
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			// Try as global command
			cmdPath, err := exec.LookPath(target)
			if err != nil {
				app.LogWarn("Shim target not found: %s", target)
				continue
			}
			targetPath = cmdPath
		}

		app.LogDebug("Shimming %s -> %s", name, targetPath)
		if err := shim.Create(&shim.Config{
			TargetPath: targetPath,
			Name:       name,
			Args:       args,
			ShimDir:    shimDir,
			Global:     e.Global,
		}); err != nil {
			return fmt.Errorf("shimming %s: %w", name, err)
		}

		// Add shim dir to PATH
		env.AddPath([]string{shimDir}, "PATH", e.Global)
	}

	return nil
}

// createShortcuts creates start menu shortcuts.
func (e *Engine) createShortcuts(m *manifest.Manifest, dir string) error {
	shortcuts := m.GetShortcuts(e.Arch)
	for _, s := range shortcuts {
		if len(s) < 2 {
			continue
		}
		target := filepath.Join(dir, s[0])
		name := s[1]
		args := ""
		icon := ""
		if len(s) >= 3 {
			args = s[2]
		}
		if len(s) >= 4 {
			icon = filepath.Join(dir, s[3])
		}

		if err := shortcut.Create(&shortcut.Config{
			TargetPath: target,
			Name:       name,
			Arguments:  args,
			IconPath:   icon,
			WorkingDir: filepath.Dir(target),
			Global:     e.Global,
		}); err != nil {
			app.LogWarn("Creating shortcut failed: %v", err)
		}
	}
	return nil
}

// installPSModule installs a PowerShell module from the manifest.
func (e *Engine) installPSModule(m *manifest.Manifest, dir string) error {
	if m.PsModule == nil || m.PsModule.Name == "" {
		return nil
	}

	moduleName := m.PsModule.Name
	modulesDir := app.Dirs().ModulesDir
	if e.Global {
		modulesDir = filepath.Join(app.Dirs().GlobalDir, "modules")
	}
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		return err
	}

	// Ensure modules dir is in PSModulePath
	env.EnsurePSModulePath(modulesDir, e.Global)

	linkPath := filepath.Join(modulesDir, moduleName)
	if _, err := os.Stat(linkPath); err == nil {
		os.RemoveAll(linkPath)
	}

	app.LogDebug("Linking PS module %s -> %s", linkPath, dir)
	// Create junction/symlink for the module directory
	if err := createJunction(linkPath, dir); err != nil {
		return fmt.Errorf("linking PS module: %w", err)
	}

	return nil
}

// envAddPath adds the app's env_add_path directories to PATH.
func (e *Engine) envAddPath(m *manifest.Manifest, dir string) error {
	addPath := m.GetEnvAddPath(e.Arch)
	if len(addPath) == 0 {
		return nil
	}

	var fullPaths []string
	for _, p := range addPath {
		fullPath := filepath.Join(dir, p)
		fullPaths = append(fullPaths, fullPath)
	}

	app.LogDebug("Adding to PATH: %s", strings.Join(fullPaths, ", "))
	return env.AddPath(fullPaths, app.Dirs().PathEnvVar, e.Global)
}

// envSet sets environment variables defined in the manifest.
func (e *Engine) envSet(m *manifest.Manifest) error {
	envSet := m.GetEnvSet(e.Arch)
	for name, val := range envSet {
		// Variable substitution
		val = strings.ReplaceAll(val, "$dir", filepath.Join(app.AppDir(e.Global), e.AppName, "current"))
		val = strings.ReplaceAll(val, "$version", e.Version)

		app.LogDebug("Setting env %s=%s", name, val)
		if err := env.SetEnv(name, val, e.Global); err != nil {
			return fmt.Errorf("setting %s: %w", name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Persist link helpers
// ---------------------------------------------------------------------------

// createPersistLink creates the appropriate link for a persist item.
//   - Directories: creates a junction (or symlink on non-Windows)
//   - Files: creates a hard link via os.Link, with file copy as fallback
//
// The link is created such that sourcePath points to targetPath.
func (e *Engine) createPersistLink(sourcePath, targetPath string, isDir bool) error {
	if isDir {
		// Directory: create junction
		if err := createJunction(sourcePath, targetPath); err != nil {
			// Fallback: copy directory instead of linking
			app.LogWarn("Failed to create junction, falling back to copy: %v", err)
			return copyDir(sourcePath, targetPath)
		}
		// Protect the junction with read-only attribute (Windows only)
		if runtime.GOOS == "windows" {
			if err := setJunctionReadOnly(sourcePath); err != nil {
				app.LogDebug("Failed to set read-only on persist junction: %v", err)
			}
		}
		return nil
	}

	// File: try hard link, fall back to copy
	if err := os.Link(targetPath, sourcePath); err != nil {
		app.LogWarn("Failed to create hard link, falling back to copy: %v", err)
		return copyFile(sourcePath, targetPath)
	}
	return nil
}

// isPersistDir determines whether a persist item is a directory by checking the
// filesystem. If source exists, it's authoritative. Otherwise checks target.
// If neither exists, defaults to true (directory), which matches Scoop behavior
// for new persist items.
func (e *Engine) isPersistDir(sourcePath, targetPath string) bool {
	if info, err := os.Stat(sourcePath); err == nil {
		return info.IsDir()
	}
	if info, err := os.Stat(targetPath); err == nil {
		return info.IsDir()
	}
	// Default: treat as directory (Scoop convention for new persist items)
	return true
}

// copyPersistData copies a file or directory from src to dst.
func copyPersistData(dst, src string, isDir bool) error {
	if isDir {
		return copyDir(dst, src)
	}
	return copyFile(dst, src)
}

// copyFile copies a single file from src to dst, preserving the mode.
func copyFile(dst, src string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return nil
}

// copyDir recursively copies a directory tree from src to dst.
func copyDir(dst, src string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(dstPath, srcPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(dstPath, srcPath); err != nil {
				return err
			}
		}
	}
	return nil
}
