// Package shim creates and manages Scoop shims — small wrapper executables/scripts
// that redirect to the actual installed binaries. Mirrors core.ps1 shim() function.
//
// For .exe files: creates shim.exe + .shim config file
// For .bat/.cmd: creates .cmd wrapper + shell wrapper
// For .ps1: creates .ps1 wrapper + .cmd + shell wrapper
// For .jar: creates .cmd + shell wrapper calling java -jar
// For .py: creates .cmd + shell wrapper calling python
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Config holds shim creation configuration.
type Config struct {
	TargetPath string // absolute path to the real executable
	Name       string // shim name (without extension)
	Args       string // extra arguments to pass
	ShimDir    string // directory to place shims in
	Global     bool   // whether this is a global install
}

// Create generates the appropriate shim based on the target file extension.
// Mirrors shim() from lib/core.ps1 L887-1035.
func Create(cfg *Config) error {
	if err := os.MkdirAll(cfg.ShimDir, 0755); err != nil {
		return fmt.Errorf("creating shim directory: %w", err)
	}

	shimPath := filepath.Join(cfg.ShimDir, strings.ToLower(cfg.Name))

	// SHIM-009: warn_on_overwrite - check for existing shims from different apps
	warnOnOverwrite(cfg, shimPath)

	switch {
	case matchExt(cfg.TargetPath, ".exe", ".com"):
		return createExeShim(cfg, shimPath)
	case matchExt(cfg.TargetPath, ".bat", ".cmd"):
		return createBatShim(cfg, shimPath)
	case matchExt(cfg.TargetPath, ".ps1"):
		return createPs1Shim(cfg, shimPath)
	case matchExt(cfg.TargetPath, ".jar"):
		return createJarShim(cfg, shimPath)
	case matchExt(cfg.TargetPath, ".py"):
		return createPyShim(cfg, shimPath)
	default:
		return createBatShim(cfg, shimPath)
	}
}

// SHIM-009: warn_on_overwrite — check if target shim paths already exist from
// a different app and warn the user before overwriting.
func warnOnOverwrite(cfg *Config, shimPath string) {
	// Check existing .shim file (for exe shims)
	existingShim := shimPath + ".shim"
	if _, err := os.Stat(existingShim); err == nil {
		currentTarget := ResolveShimTarget(existingShim)
		if currentTarget != "" && !strings.EqualFold(currentTarget, cfg.TargetPath) {
			fmt.Fprintf(os.Stderr, "WARNING: Shim '%s' already exists and points to '%s'. Overwriting with '%s'.\n",
				cfg.Name, currentTarget, cfg.TargetPath)
		}
		return
	}

	// Check existing .cmd wrapper (for non-exe shims)
	existingCmd := shimPath + ".cmd"
	if _, err := os.Stat(existingCmd); err == nil {
		currentTarget := ResolveWrapperTarget(existingCmd)
		if currentTarget != "" && !strings.EqualFold(currentTarget, cfg.TargetPath) {
			fmt.Fprintf(os.Stderr, "WARNING: Shim '%s' already exists and points to '%s'. Overwriting with '%s'.\n",
				cfg.Name, currentTarget, cfg.TargetPath)
		}
	}
}

// Remove deletes all shim files for a given name.
// Mirrors rm_shim() from lib/install.ps1 L199-220.
func Remove(name, shimDir, appName string) error {
	extensions := []string{"", ".shim", ".cmd", ".ps1"}
	for _, ext := range extensions {
		shimPath := filepath.Join(shimDir, name+ext)
		altPath := filepath.Join(shimDir, name+ext+"."+appName)

		// Try alternative path (with app suffix for conflict resolution)
		if _, err := os.Stat(altPath); err == nil {
			if err := os.Remove(altPath); err != nil {
				return err
			}
		} else if _, err := os.Stat(shimPath); err == nil {
			if err := os.Remove(shimPath); err != nil {
				return err
			}
			// Also remove matching .exe when .shim is removed
			if ext == ".shim" {
				exePath := filepath.Join(shimDir, name+".exe")
				os.Remove(exePath)
			}

			// SHIM-006: Conflict promotion — promote the most recent backup
			// back to the primary name so the next-winner app continues working.
			if err := promoteBackup(shimDir, name, ext); err != nil {
				// Non-fatal: log but don't fail the removal
				fmt.Fprintf(os.Stderr, "WARNING: Failed to promote shim backup for %s%s: %v\n", name, ext, err)
			}
		}
	}
	return nil
}

// SHIM-006: Promote the most recent backup shim back to the primary name.
// Backups are named as name.ext.someappname (e.g. foo.shim.brapp).
func promoteBackup(shimDir, name, ext string) error {
	entries, err := os.ReadDir(shimDir)
	if err != nil {
		return nil // directory gone or unreadable, nothing to promote
	}

	// Look for backups named name.ext.something (where something is not empty)
	prefix := name + ext + "."
	var bestName string
	var bestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entryName := entry.Name()
		if strings.HasPrefix(entryName, prefix) && len(entryName) > len(prefix) {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			modTime := info.ModTime()
			if bestName == "" || modTime.After(bestTime) {
				bestName = entryName
				bestTime = modTime
			}
		}
	}

	if bestName == "" {
		return nil
	}

	// Extract the app name suffix from the backup filename
	suffix := strings.TrimPrefix(bestName, prefix)

	// Rename the backup back to the primary name
	backupPath := filepath.Join(shimDir, bestName)
	primaryPath := filepath.Join(shimDir, name+ext)
	if err := os.Rename(backupPath, primaryPath); err != nil {
		return err
	}

	// For .shim backup, also promote the matching .exe backup
	if ext == ".shim" {
		exeBackup := filepath.Join(shimDir, name+".exe."+suffix)
		exePrimary := filepath.Join(shimDir, name+".exe")
		if _, err := os.Stat(exeBackup); err == nil {
			if err := os.Rename(exeBackup, exePrimary); err != nil {
				return err
			}
		}
	}

	return nil
}

// --- .exe shim ---
// Creates shim.exe + .shim config file pointing to the real binary.
// Uses the embedded Go shim binary. Reference: lib/core.ps1 L901-913
func createExeShim(cfg *Config, shimPath string) error {
	// Write .shim config file
	shimContent := fmt.Sprintf("path = \"%s\"\n", cfg.TargetPath)
	if cfg.Args != "" {
		shimContent += fmt.Sprintf("args = %s\n", cfg.Args)
	}
	if err := writeFile(shimPath+".shim", shimContent); err != nil {
		return err
	}

	// Extract embedded shim.exe binary to shim directory
	shimExePath := shimPath + ".exe"
	shimData := ShimExe

	// SHIM-004: PE subsystem patching — if target is a GUI application
	// (subsystem 2), patch the embedded shim.exe to also be GUI to
	// prevent a console window flash before the shim binary runs.
	if runtime.GOOS == "windows" && isGuiTarget(cfg.TargetPath) {
		if patched := patchPeSubsystem(shimData, imageSubsystemGui); patched != nil {
			shimData = patched
		}
	}

	if err := os.WriteFile(shimExePath, shimData, 0755); err != nil {
		return fmt.Errorf("extracting shim binary: %w", err)
	}

	return nil
}

// --- .bat/.cmd shim ---
// Creates a .cmd wrapper and a Unix shell wrapper.
// Reference: lib/core.ps1 L915-928
func createBatShim(cfg *Config, shimPath string) error {
	// .cmd wrapper (Windows) — SHIM-012: Use CRLF line endings
	content := fmt.Sprintf("@rem \"%s\"\r\n@\"%s\" %s %%*\r\n", cfg.TargetPath, cfg.TargetPath, cfg.Args)
	if err := writeFile(shimPath+".cmd", content); err != nil {
		return err
	}

	// SHIM-005: Shell wrapper with MSYS2_ARG_CONV_EXCL and cmd.exe /c.
	// MSYS2_ARG_CONV_EXCL="*" prevents MSYS2/Git Bash from mangling
	// Windows-style arguments (e.g. /D, /S) during POSIX-to-Windows
	// path conversion. cmd.exe /c ensures the batch file runs via
	// cmd.exe even when called from Unix shells.
	shContent := fmt.Sprintf("#!/bin/sh\n# %s\nexport MSYS2_ARG_CONV_EXCL=\"*\"\ncmd.exe /c \"%s\" %s \"$@\"\n",
		cfg.TargetPath, cfg.TargetPath, cfg.Args)
	return writeFile(shimPath, shContent)
}

// --- .ps1 shim ---
// Creates .ps1, .cmd, and shell wrappers.
// Reference: lib/core.ps1 L929-971
func createPs1Shim(cfg *Config, shimPath string) error {
	// SHIM-019: Compute relative path from shim directory to target,
	// used with $PSScriptRoot for a portable shim that survives
	// relocation of the Scoop directory.
	relPath, err := filepath.Rel(cfg.ShimDir, cfg.TargetPath)
	if err != nil {
		relPath = cfg.TargetPath // fallback to absolute if Rel fails
	}

	// .ps1 wrapper — SHIM-019: Use Join-Path $PSScriptRoot for
	// portable relative-path resolution. SHIM-012: CRLF line endings.
	ps1Content := fmt.Sprintf(
		"# %s\r\n$path = Join-Path $PSScriptRoot \"%s\"\r\n"+
			"if ($MyInvocation.ExpectingInput) { $input | & $path %s @args } else { & $path %s @args }\r\n"+
			"exit $LASTEXITCODE\r\n",
		cfg.TargetPath, relPath, cfg.Args, cfg.Args)
	if err := writeFile(shimPath+".ps1", ps1Content); err != nil {
		return err
	}

	// .cmd wrapper (calls PowerShell) — SHIM-012: CRLF line endings
	cmdContent := fmt.Sprintf("@rem %s\r\n@echo off\r\nwhere /q pwsh.exe\r\nif %%errorlevel%% equ 0 (\r\n    pwsh -noprofile -ex unrestricted -file \"%s\" %s %%*\r\n) else (\r\n    powershell -noprofile -ex unrestricted -file \"%s\" %s %%*\r\n)\r\n",
		cfg.TargetPath, cfg.TargetPath, cfg.Args, cfg.TargetPath, cfg.Args)
	if err := writeFile(shimPath+".cmd", cmdContent); err != nil {
		return err
	}

	// Shell wrapper (Unix) — keep LF line endings for Unix scripts
	shContent := fmt.Sprintf("#!/bin/sh\n# %s\nif command -v pwsh.exe > /dev/null 2>&1; then\n    pwsh.exe -noprofile -ex unrestricted -file \"%s\" %s \"$@\"\nelse\n    powershell.exe -noprofile -ex unrestricted -file \"%s\" %s \"$@\"\nfi\n",
		cfg.TargetPath, cfg.TargetPath, cfg.Args, cfg.TargetPath, cfg.Args)
	return writeFile(shimPath, shContent)
}

// --- .jar shim ---
// Creates .cmd + shell wrapper calling java -jar.
// Reference: lib/core.ps1 L972-992
func createJarShim(cfg *Config, shimPath string) error {
	parent := filepath.Dir(cfg.TargetPath)

	// .cmd wrapper — SHIM-012: CRLF line endings
	cmdContent := fmt.Sprintf("@rem %s\r\n@pushd %s\r\n@java -jar \"%s\" %s %%*\r\n@popd\r\n",
		cfg.TargetPath, parent, cfg.TargetPath, cfg.Args)
	if err := writeFile(shimPath+".cmd", cmdContent); err != nil {
		return err
	}

	// SHIM-011: Shell wrapper with WSL/Cygwin path detection.
	// Checks $WSL_INTEROP for WSL (uses wslpath -u), falls back to
	// cygpath -u for Cygwin/Git Bash, otherwise uses the raw path.
	shContent := fmt.Sprintf("#!/bin/sh\n# %s\n"+
		"if [ -n \"$WSL_INTEROP\" ]; then\n"+
		"    # WSL: convert Windows path to WSL path\n"+
		"    target=\"$(wslpath -u '%s')\"\n"+
		"elif command -v cygpath >/dev/null 2>&1; then\n"+
		"    # Cygwin/Git Bash: convert Windows path to Unix path\n"+
		"    target=\"$(cygpath -u '%s')\"\n"+
		"else\n"+
		"    target=\"%s\"\n"+
		"fi\n"+
		"cd \"$(dirname \"$target\")\"\n"+
		"java.exe -jar \"$(basename \"$target\")\" %s \"$@\"\n",
		cfg.TargetPath, cfg.TargetPath, cfg.TargetPath, cfg.TargetPath, cfg.Args)
	return writeFile(shimPath, shContent)
}

// --- .py shim ---
// Creates .cmd + shell wrapper calling python.
// Reference: lib/core.ps1 L993-1005
func createPyShim(cfg *Config, shimPath string) error {
	// .cmd wrapper — SHIM-012: CRLF line endings
	cmdContent := fmt.Sprintf("@rem %s\r\n@python \"%s\" %s %%*\r\n", cfg.TargetPath, cfg.TargetPath, cfg.Args)
	if err := writeFile(shimPath+".cmd", cmdContent); err != nil {
		return err
	}

	// Shell wrapper (Unix) — keep LF line endings
	shContent := fmt.Sprintf("#!/bin/sh\n# %s\npython.exe \"%s\" %s \"$@\"\n", cfg.TargetPath, cfg.TargetPath, cfg.Args)
	return writeFile(shimPath, shContent)
}

// --- Helpers ---

func matchExt(path string, exts ...string) bool {
	lower := strings.ToLower(path)
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0755)
}

// --- PE Subsystem constants and helpers ---

const (
	imageDosSignature = 0x5A4D      // MZ
	imageNtSignature  = 0x00004550  // PE\0\0
	imageSubsystemGui = 2
	imageSubsystemCui = 3
)

// isGuiTarget reads the PE header of a Windows executable and returns true
// if the subsystem is GUI (IMAGE_SUBSYSTEM_WINDOWS_GUI = 2), meaning the
// binary does not expect a console. Mirrors isGuiSubsystem() in the shim
// binary (internal/shimbinary/main.go).
func isGuiTarget(exePath string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	f, err := os.Open(exePath)
	if err != nil {
		return false
	}
	defer f.Close()

	var dosHeader [0x40]byte
	if _, err := f.ReadAt(dosHeader[:], 0); err != nil {
		return false
	}

	dosMagic := uint16(dosHeader[0]) | uint16(dosHeader[1])<<8
	if dosMagic != imageDosSignature {
		return false
	}

	peOffset := int(dosHeader[0x3C]) | int(dosHeader[0x3D])<<8 |
		int(dosHeader[0x3E])<<16 | int(dosHeader[0x3F])<<24
	if peOffset <= 0 || peOffset > 0x1000 {
		return false
	}

	var peHeader [0x60]byte
	if _, err := f.ReadAt(peHeader[:], int64(peOffset)); err != nil {
		return false
	}

	peSig := uint32(peHeader[0]) | uint32(peHeader[1])<<8 |
		uint32(peHeader[2])<<16 | uint32(peHeader[3])<<24
	if peSig != imageNtSignature {
		return false
	}

	// Subsystem field is at offset 0x5C from start of IMAGE_NT_HEADERS
	// (4-byte signature + 20-byte file header = 24 = 0x18,
	//  then subsystem is at offset 0x44 in the optional header,
	//  total: 0x18 + 0x44 = 0x5C)
	subsystem := uint16(peHeader[0x5C]) | uint16(peHeader[0x5D])<<8
	return subsystem == imageSubsystemGui
}

// patchPeSubsystem modifies the PE subsystem field in a binary's byte slice.
// Returns a new slice with the subsystem changed, or nil on error (invalid
// PE format, out of bounds, etc.).
func patchPeSubsystem(data []byte, newSubsystem uint16) []byte {
	if len(data) < 0x40 {
		return nil
	}

	dosMagic := uint16(data[0]) | uint16(data[1])<<8
	if dosMagic != imageDosSignature {
		return nil
	}

	peOffset := int(data[0x3C]) | int(data[0x3D])<<8 |
		int(data[0x3E])<<16 | int(data[0x3F])<<24
	if peOffset <= 0 || peOffset+0x60 > len(data) {
		return nil
	}

	peSig := uint32(data[peOffset]) | uint32(data[peOffset+1])<<8 |
		uint32(data[peOffset+2])<<16 | uint32(data[peOffset+3])<<24
	if peSig != imageNtSignature {
		return nil
	}

	// Subsystem is at offset 0x5C from start of IMAGE_NT_HEADERS
	subsystemOffset := peOffset + 0x5C
	if subsystemOffset+2 > len(data) {
		return nil
	}

	// Make a copy and patch the subsystem field
	result := make([]byte, len(data))
	copy(result, data)
	result[subsystemOffset] = byte(newSubsystem)
	result[subsystemOffset+1] = byte(newSubsystem >> 8)

	return result
}
