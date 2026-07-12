// Package shortcut creates Windows start menu shortcuts (.lnk files).
// Mirrors lib/shortcuts.ps1 from the original Scoop.
//
// Windows implementation writes Shell Link binaries in pure Go (no PowerShell).
// Non-Windows platforms skip shortcut creation.
package shortcut

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds shortcut creation configuration.
type Config struct {
	TargetPath string // path to the executable
	Name       string // shortcut display name
	Arguments  string // command-line arguments
	IconPath   string // path to icon file (optional)
	WorkingDir string // working directory
	Global     bool   // create in global start menu
}

// Create creates a start menu shortcut.
// Mirrors startmenu_shortcut() from lib/shortcuts.ps1 L31-59.
func Create(cfg *Config) error {
	if _, err := os.Stat(cfg.TargetPath); os.IsNotExist(err) {
		return fmt.Errorf("shortcut target not found: %s", cfg.TargetPath)
	}
	if cfg.IconPath != "" {
		if _, err := os.Stat(cfg.IconPath); os.IsNotExist(err) {
			return fmt.Errorf("shortcut icon not found: %s", cfg.IconPath)
		}
	}

	folder := startMenuFolder(cfg.Global)
	shortcutDir := filepath.Dir(filepath.Join(folder, cfg.Name))
	if err := os.MkdirAll(shortcutDir, 0755); err != nil {
		return fmt.Errorf("creating shortcuts directory: %w", err)
	}

	shortcutPath := filepath.Join(folder, cfg.Name+".lnk")

	if runtime.GOOS != "windows" {
		// Degrade gracefully off Windows
		return nil
	}

	workDir := cfg.WorkingDir
	if workDir == "" {
		workDir = filepath.Dir(cfg.TargetPath)
	}
	return WriteShellLink(shortcutPath, LinkData{
		TargetPath: cfg.TargetPath,
		Arguments:  cfg.Arguments,
		WorkingDir: workDir,
		IconPath:   cfg.IconPath,
	})
}

// RemoveAll deletes all shortcuts for a given manifest and architecture.
// Mirrors rm_startmenu_shortcuts().
func RemoveAll(names [][]string, global bool) error {
	folder := startMenuFolder(global)
	for _, s := range names {
		if len(s) < 2 {
			continue
		}
		name := s[1]
		shortcutPath := filepath.Join(folder, name+".lnk")
		if err := os.Remove(shortcutPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// startMenuFolder returns the path to the Scoop Apps start menu folder.
// Mirrors shortcut_folder() from lib/shortcuts.ps1 L22-29.
func startMenuFolder(global bool) string {
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
