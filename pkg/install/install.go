// Package install handles the app installation lifecycle.
// It mirrors lib/install.ps1 from the original Scoop.
package install

import (
	"context"
	"crypto/sha256"
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
func (e *Engine) Install(ctx context.Context) (retErr error) {
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
	_, statErr := os.Stat(versionDir)
	createdVersionDir := os.IsNotExist(statErr)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("creating version directory: %w", err)
	}
	originalDir := versionDir
	integrationStarted := false
	defer func() {
		if retErr == nil {
			return
		}
		if integrationStarted {
			e.rollbackIntegrations(m, versionDir)
		}
		if createdVersionDir {
			if err := os.RemoveAll(versionDir); err != nil {
				app.LogWarn("Failed to remove incomplete install directory: %v", err)
			}
		}
	}()

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
	integrationStarted = true
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
	if err := e.saveInstallInfo(versionDir); err != nil {
		return fmt.Errorf("saving install metadata: %w", err)
	}
	if err := e.saveManifest(versionDir); err != nil {
		return fmt.Errorf("saving installed manifest: %w", err)
	}

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

		// Check manifest configuration FIRST, then extension.
		// Many manifests use #/dl.7z rename but file is InnoSetup, not 7z.
		switch {
		case m.InnoSetup:
			app.LogDebug("Using InnoExtractor for %s (innosetup=true)", filepath.Base(file))
			if _, err := exec.LookPath("innounp"); err != nil {
				installHelper("innounp")
			}
			extractor = &extract.InnoExtractor{}
		case extract.IsWixInstaller(file):
			app.LogDebug("Using WixExtractor for %s (WiX bundle detected)", filepath.Base(file))
			if _, err := exec.LookPath("dark"); err != nil {
				installHelper("dark")
			}
			extractor = &extract.WixExtractor{}
		case strings.HasSuffix(strings.ToLower(file), ".exe"):
			app.LogDebug("Skipping extraction for %s (not a supported archive type)", filepath.Base(file))
			continue
		default:
			// Scoop commonly renames ZIP/NuGet/SFX installers to dl.7z so the
			// official 7-Zip can auto-detect them. Use magic detection first;
			// PE/SFX aliases require the external universal 7z decoder.
			if strings.HasSuffix(strings.ToLower(file), ".7z") {
				magicExtractor := extract.DetectByMagic(file)
				switch magicExtractor.(type) {
				case *extract.ZipExtractor, *extract.SevenZipExtractor:
					extractor = magicExtractor
				case *extract.ExeExtractor:
					if _, err := exec.LookPath("7z"); err != nil {
						installHelper("7zip")
					}
					extractor = &extract.ExternalSevenZipExtractor{}
				default:
					extractor = extract.DetectExtractor(file)
				}
			} else {
				extractor = extract.DetectExtractor(file)
			}
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
	fullScript = "try {\n" + fullScript + "\n} catch { Write-Error $_; exit 1 }; exit 0"

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
	if e.Global {
		psGlobal = "$true"
	}
	psCmd := fmt.Sprintf(
		powerShellCompatibilityPreamble()+
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

func powerShellCompatibilityPreamble() string {
	exe, _ := os.Executable()
	exe = strings.ReplaceAll(exe, "'", "''")
	return fmt.Sprintf(
		"function ensure($Path){New-Item -ItemType Directory -Force -Path $Path};"+
			"function shim($Path,$Global,$Name,$ShimArgs){& '%s' shim add $Name $Path $ShimArgs;if($LASTEXITCODE){throw 'shim creation failed'}};"+
			"function Expand-DarkArchive{param($Path,$DestinationPath,[switch]$Removal)try{& dark -nologo -x $DestinationPath $Path}catch{throw}};"+
			"function Expand-InnoArchive{param($Path,$DestinationPath,$ExtractDir,[switch]$Removal)try{& innounp -x -d $DestinationPath $Path -y}catch{throw}};"+
			"function Expand-MsiArchive{param($Path,$DestinationPath,$ExtractDir,[switch]$Removal)try{$d=$DestinationPath;if($ExtractDir){$d=$DestinationPath+'\\_tmp'};msiexec /a $Path /qn 'TARGETDIR='+$d+'\\SourceDir';if($ExtractDir-and(test-path($d+'\\SourceDir\\'+$ExtractDir))){cp -re ($d+'\\SourceDir\\'+$ExtractDir+'\\*') $DestinationPath;ri $d -re -fo}elseif(test-path($d+'\\SourceDir')){gci ($d+'\\SourceDir')|cp -dest $DestinationPath -re -force;ri ($d+'\\SourceDir')-re -fo};if($Removal){ri $Path -fo}}catch{throw}};"+
			"function Invoke-ExternalCommand{$e=$null;$a=@();$i=0;while($i -lt $args.Count){if($args[$i]-eq\"-Path\"-or$args[$i]-eq\"-FilePath\"){$e=$args[++$i]}elseif($args[$i]-eq\"-ArgumentList\"-or$args[$i]-eq\"-Args\"){$a=$args[++$i]};$i++};if(!$e){$e=$args[0]};& $e @a;if($LASTEXITCODE){throw}};"+
			"function versiondir($app,$v,$g){$d=$appsdir;$d=join-path $d $app;$d=join-path $d $v;$d};"+
			"function appdir($app,$g){join-path $appsdir $app};"+
			"function info($msg){write-host $msg -foregroundcolor darkgray};"+
			"function warn($msg){write-host 'WARN: '+$msg -foregroundcolor yellow};"+
			"function get_config($k){$null};"+
			"function versiondir($app,$v,$g){$d=$appsdir;$d=join-path $d $app;$d=join-path $d $v;$d};"+
			"function appdir($app,$g){join-path $appsdir $app};"+
			"function warn($msg){write-host 'WARN: '+$msg -foregroundcolor yellow};"+
			"function get_config($k){$null};"+
			"function Add-Path($p,$g){$env:Path=$p+';'+$env:Path};"+
			"function Remove-Path($p,$g){$env:Path=($env:Path-split';'|where{$_-ne$p})-join';'};",
		exe,
	)
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
		fullScript := strings.Join(inst.Script, "\n")
		for k, v := range substitutions {
			fullScript = strings.ReplaceAll(fullScript, k, v)
		}
		fullScript = "try {\n" + fullScript + "\n} catch { Write-Error $_; exit 1 }; exit 0"
		if strings.Contains(fullScript, "Expand-DarkArchive") {
			if _, err := exec.LookPath("dark"); err != nil {
				installHelper("dark")
			}
		}
		if strings.Contains(fullScript, "Expand-InnoArchive") {
			if _, err := exec.LookPath("innounp"); err != nil {
				installHelper("innounp")
			}
		}
		psCmd := fmt.Sprintf(
			powerShellCompatibilityPreamble() +
			"$appsdir = '%s'; $bucketsdir = '%s'; $scoopdir = '%s'; $cachedir = '%s'; $shimsdir = '%s';" +
			"function Expand-DarkArchive{param($Path,$DestinationPath,[switch]$Removal)try{& dark -nologo -x $DestinationPath $Path}catch{throw}}" +
			"function Expand-InnoArchive{param($Path,$DestinationPath,$ExtractDir,[switch]$Removal)try{& innounp -x -d $DestinationPath $Path -y}catch{throw}}" +
			"function Expand-MsiArchive{param($Path,$DestinationPath,$ExtractDir,[switch]$Removal)try{$sd=join-path $DestinationPath SourceDir;msiexec /a $Path /qn (\"TARGETDIR=\"+$sd);if(test-path $sd){gci $sd|cp -dest $DestinationPath -re -force;ri $sd -re -fo}}catch{throw}}" +
			"function Invoke-ExternalCommand{$e=$null;$a=@();$i=0;while($i -lt $args.Count){if($args[$i]-eq\"-Path\"-or$args[$i]-eq\"-FilePath\"){$e=$args[++$i]}elseif($args[$i]-eq\"-ArgumentList\"-or$args[$i]-eq\"-Args\"){$a=$args[++$i]};$i++};if(!$e){$e=$args[0]};& $e @a;if($LASTEXITCODE){throw}}" +
			fullScript,
			app.Dirs().AppsDir, app.Dirs().BucketsDir, app.Dirs().ScoopDir,
			app.Dirs().CacheDir, app.Dirs().ShimsDir)
		app.LogDebug("Running installer script")
		cmd := exec.Command("powershell.exe", "-NoProfile", "-Ex", "Unrestricted", "-Command", psCmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("installer script failed: %w\nOutput: %s", err, string(output))
		}
		if len(output) > 0 {
			fmt.Print(string(output))
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
			if !noJunction {
				if err := e.createPersistLink(sourcePath, targetPath, true); err != nil {
					return fmt.Errorf("linking empty persist directory: %w", err)
				}
			}
		}
	}

	return nil
}

// saveInstallInfo saves installation metadata to install.json.
func (e *Engine) saveInstallInfo(versionDir string) error {
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
		return fmt.Errorf("marshaling install info: %w", err)
	}

	path := filepath.Join(versionDir, "install.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing install.json: %w", err)
	}
	return nil
}

// saveManifest saves a copy of the manifest to the version directory.
func (e *Engine) saveManifest(versionDir string) error {
	// Serialize manifest back to JSON
	data, err := json.MarshalIndent(e.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}

	path := filepath.Join(versionDir, "manifest.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing manifest.json: %w", err)
	}
	return nil
}

// rollbackIntegrations removes system integration created after current was
// switched. UpdateApp restores the previous version's integration afterward.
func (e *Engine) rollbackIntegrations(m *manifest.Manifest, versionDir string) {
	currentDir := filepath.Join(filepath.Dir(versionDir), "current")
	if err := os.RemoveAll(currentDir); err != nil {
		app.LogWarn("Failed to remove incomplete current link: %v", err)
	}

	for _, bin := range manifest.BinEntries(m.GetBin(e.Arch)) {
		if err := shim.Remove(bin[1], app.ShimDir(e.Global), e.AppName); err != nil {
			app.LogWarn("Failed to remove incomplete shim %s: %v", bin[1], err)
		}
	}
	if err := shortcut.RemoveAll(m.GetShortcuts(e.Arch), e.Global); err != nil {
		app.LogWarn("Failed to remove incomplete shortcuts: %v", err)
	}
	for name := range m.GetEnvSet(e.Arch) {
		if err := env.SetEnv(name, "", e.Global); err != nil {
			app.LogWarn("Failed to remove incomplete environment variable %s: %v", name, err)
		}
	}
	var paths []string
	for _, path := range m.GetEnvAddPath(e.Arch) {
		paths = append(paths, filepath.Join(currentDir, path))
	}
	if len(paths) > 0 {
		if err := env.RemovePath(paths, app.Dirs().PathEnvVar, e.Global); err != nil {
			app.LogWarn("Failed to remove incomplete PATH entries: %v", err)
		}
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

// GenerateVersionManifest applies a manifest's autoupdate templates for a
// requested version, downloads each generated URL, and pins the resulting
// SHA-256 hashes. The normal install pipeline then consumes the verified cache.
func GenerateVersionManifest(ctx context.Context, appName string, source *manifest.Manifest, targetVersion, arch string, useCache bool) (*manifest.Manifest, error) {
	generatedJSON, err := manifest.GenerateUserManifest(source, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("generating manifest for %s@%s: %w", appName, targetVersion, err)
	}
	generated, err := manifest.Parse(generatedJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing generated manifest: %w", err)
	}
	resolvedArch := generated.ResolveArch(arch)
	if resolvedArch == "" {
		return nil, fmt.Errorf("'%s' doesn't support architecture %s", appName, arch)
	}
	urls := generated.GetURL(resolvedArch)
	if len(urls) == 0 {
		return nil, fmt.Errorf("generated manifest has no URLs for architecture %s", resolvedArch)
	}
	if err := os.MkdirAll(app.Dirs().CacheDir, 0755); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}

	var githubToken, proxy string
	var privateHosts []config.PrivateHostRule
	if cfg := app.Config(); cfg != nil {
		githubToken = cfg.Config().GH_TOKEN
		proxy = cfg.Config().Proxy
		privateHosts = cfg.Config().PrivateHosts
	}
	hashes := make(manifest.FlexibleStrings, 0, len(urls))
	for _, url := range urls {
		cacheKey := fmt.Sprintf("%s#%s#%s", appName, targetVersion, shortHash(url))
		cachePath := filepath.Join(app.Dirs().CacheDir, cacheKey)
		downloadPath := cachePath + ".generate"
		dl := download.NewDownloader(&download.Config{
			URL:         url,
			Destination: downloadPath,
			CacheDir:    app.Dirs().CacheDir,
			CacheKey:    cacheKey,
			UseCache:    useCache,
			Cookies:     generated.Cookie,
			GithubToken: githubToken,
			Proxy:       proxy,
			Headers:     matchingPrivateHeaders(privateHosts, url),
		})
		result, err := dl.Download(ctx)
		if err != nil {
			return nil, fmt.Errorf("downloading generated URL %s: %w", url, err)
		}
		hash, err := sha256File(result.Path)
		if result.Path == downloadPath {
			_ = os.Remove(downloadPath)
		}
		if err != nil {
			return nil, fmt.Errorf("hashing generated URL %s: %w", url, err)
		}
		hashes = append(hashes, hash)
	}

	if content := generated.GetArchContent(resolvedArch); content != nil && len(content.URL) > 0 {
		content.Hash = hashes
	} else {
		generated.Hash = hashes
	}
	return generated, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
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
func installHelper(pkg string) {
	interactive := false
	var resp string
	_, err := fmt.Scanln(&resp)
	if err == nil {
		interactive = true
	}
	if interactive && resp != "" && resp != "Y" && resp != "y" && resp != "yes" {
		app.LogWarn("Skipping. Install later: scoop install %s", pkg)
		return
	}
	app.LogInfo("Installing '%s'...", pkg)
	c := exec.Command(os.Args[0], "install", pkg)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		app.LogWarn("Failed to install '%s': %v", pkg, err)
		return
	}
	// Add shims dir to PATH so current process can find the installed tool
	shimDir := app.Dirs().ShimsDir
	curPath := os.Getenv("PATH")
	if !strings.Contains(curPath, shimDir) {
		os.Setenv("PATH", shimDir+string(os.PathListSeparator)+curPath)
	}
}
