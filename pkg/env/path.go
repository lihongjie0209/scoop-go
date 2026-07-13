// Package env manages Windows environment variables — PATH, PSModulePath,
// and custom env_set variables. Mirrors lib/system.ps1 from the original Scoop.
//
// On Windows: persists via registry (HKCU/HKLM\Environment) + WM_SETTINGCHANGE broadcast.
// On non-Windows: modifies current process environment only.
package env

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

// AddPath prepends directories to the given environment variable (e.g. "PATH").
// The envName parameter specifies which environment variable to modify.
// Uses the current process environment for reads and writes, then persists to
// the Windows registry for global effect.
func AddPath(paths []string, envName string, global bool) error {
	if len(paths) == 0 {
		return nil
	}

	// Read from the scope-specific registry key (not the merged process PATH) to
	// avoid duplicating system entries into the user key on every install.
	currentPath := GetEnv(envName, global)
	changed := false

	var added []string
	for _, p := range paths {
		if !isInPath(p, currentPath) {
			added = append(added, p)
			if currentPath != "" {
				currentPath = p + string(os.PathListSeparator) + currentPath
			} else {
				currentPath = p
			}
			changed = true
		}
	}

	if changed {
		os.Setenv(envName, currentPath)

		if err := WriteEnvVar(envName, currentPath, global); err != nil {
			return fmt.Errorf("persisting %s to registry: %w", envName, err)
		}
		if err := PublishEnvChange(); err != nil {
			return fmt.Errorf("broadcasting env change: %w", err)
		}
	}

	return nil
}

// AddPathPrepend prepends directories to the given environment variable without
// checking for duplicates. Unlike AddPath, this allows the same path to appear
// multiple times in the variable. Equivalent to PowerShell Add-Path -Force.
func AddPathPrepend(paths []string, envName string, global bool) error {
	if len(paths) == 0 {
		return nil
	}

	currentPath := GetEnv(envName, global)
	changed := false

	for _, p := range paths {
		if currentPath != "" {
			currentPath = p + string(os.PathListSeparator) + currentPath
		} else {
			currentPath = p
		}
		changed = true
	}

	if changed {
		os.Setenv(envName, currentPath)

		if err := WriteEnvVar(envName, currentPath, global); err != nil {
			return fmt.Errorf("persisting %s to registry: %w", envName, err)
		}
		if err := PublishEnvChange(); err != nil {
			return fmt.Errorf("broadcasting env change: %w", err)
		}
	}

	return nil
}

// RemovePathAndReturn removes directories from the given environment variable
// and returns the list of values that were actually removed.
// Equivalent to PowerShell Remove-Path -PassThru.
func RemovePathAndReturn(paths []string, envName string, global bool) ([]string, error) {
	currentPath := GetEnv(envName, global)
	if currentPath == "" {
		return nil, nil
	}

	parts := strings.Split(currentPath, string(os.PathListSeparator))
	var newParts []string
	var removed []string

	for _, part := range parts {
		shouldRemove := false
		for _, p := range paths {
			if strings.EqualFold(strings.TrimSpace(part), strings.TrimSpace(p)) {
				shouldRemove = true
				break
			}
		}
		if shouldRemove {
			removed = append(removed, part)
		} else {
			newParts = append(newParts, part)
		}
	}

	if len(removed) == 0 {
		return nil, nil
	}

	newPath := strings.Join(newParts, string(os.PathListSeparator))
	os.Setenv(envName, newPath)

	if err := WriteEnvVar(envName, newPath, global); err != nil {
		return removed, fmt.Errorf("persisting %s to registry: %w", envName, err)
	}
	if err := PublishEnvChange(); err != nil {
		return removed, fmt.Errorf("broadcasting env change: %w", err)
	}

	return removed, nil
}

// RemovePath removes directories from the given environment variable (e.g. "PATH").
// The envName parameter specifies which environment variable to modify.
// Uses the current process environment for reads and writes, then persists to
// the Windows registry for global effect.
func RemovePath(paths []string, envName string, global bool) error {
	currentPath := GetEnv(envName, global)
	if currentPath == "" {
		return nil
	}

	parts := strings.Split(currentPath, string(os.PathListSeparator))
	var newParts []string

	for _, part := range parts {
		shouldRemove := false
		for _, p := range paths {
			if strings.EqualFold(strings.TrimSpace(part), strings.TrimSpace(p)) {
				shouldRemove = true
				break
			}
		}
		if !shouldRemove {
			newParts = append(newParts, part)
		}
	}

	if len(newParts) != len(parts) {
		newPath := strings.Join(newParts, string(os.PathListSeparator))
		os.Setenv(envName, newPath)

		if err := WriteEnvVar(envName, newPath, global); err != nil {
			return fmt.Errorf("persisting %s to registry: %w", envName, err)
		}
		if err := PublishEnvChange(); err != nil {
			return fmt.Errorf("broadcasting env change: %w", err)
		}
	}

	return nil
}

// SetEnv sets an environment variable.
func SetEnv(name, value string, global bool) error {
	if value == "" {
		os.Unsetenv(name)
	} else {
		os.Setenv(name, value)
	}

	if err := WriteEnvVar(name, value, global); err != nil {
		return fmt.Errorf("persisting env var %s to registry: %w", name, err)
	}
	if err := PublishEnvChange(); err != nil {
		return fmt.Errorf("broadcasting env change: %w", err)
	}

	return nil
}

// GetEnv retrieves an environment variable.
// On Windows, reads from the Windows registry for persistent values,
// falling back to the current process environment.
func GetEnv(name string, global bool) string {
	if runtime.GOOS == "windows" {
		val, err := readEnvFromRegistry(name, global)
		if err == nil {
			return val
		}
	}
	return os.Getenv(name)
}

// WriteEnvVar writes an environment variable to the Windows registry.
// On non-Windows, it only modifies the current process environment.
// Uses REG_EXPAND_SZ if the value contains '%', otherwise REG_SZ.
func WriteEnvVar(name, value string, global bool) error {
	if runtime.GOOS != "windows" {
		if value == "" {
			return os.Unsetenv(name)
		}
		return os.Setenv(name, value)
	}
	return writeEnvVarWindows(name, value, global)
}

// PublishEnvChange broadcasts a WM_SETTINGCHANGE message to notify
// running applications that environment variables have changed.
func PublishEnvChange() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return publishEnvChangeWindows()
}

// WriteablePath returns the environment variable name used for PATH management.
// Returns "SCOOP_PATH" when isolated path mode is enabled, otherwise "PATH".
func WriteablePath() string {
	if app.Dirs() != nil && app.Dirs().PathEnvVar != "" {
		return app.Dirs().PathEnvVar
	}
	return "PATH"
}

// EnsurePSModulePath ensures a directory is in PSModulePath.
func EnsurePSModulePath(dir string, global bool) error {
	currentPath := GetEnv("PSModulePath", global)
	if isInPath(dir, currentPath) {
		return nil
	}
	newPath := dir
	if currentPath != "" {
		newPath = dir + string(os.PathListSeparator) + currentPath
	}
	return SetEnv("PSModulePath", newPath, global)
}

// --- internal helpers ---

// isInPath checks if a path is in a PATH-style variable.
func isInPath(path, pathVar string) bool {
	if pathVar == "" {
		return false
	}
	parts := strings.Split(pathVar, string(os.PathListSeparator))
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), strings.TrimSpace(path)) {
			return true
		}
	}
	return false
}

// IsWindows returns true on Windows (used by callers for platform-specific logic).
func IsWindows() bool {
	return runtime.GOOS == "windows"
}
