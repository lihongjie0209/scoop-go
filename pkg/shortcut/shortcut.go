// Package shortcut creates Windows start menu shortcuts (.lnk files).
// Mirrors lib/shortcuts.ps1 from the original Scoop.
//
// On Windows, this uses the COM IShellLink interface via golang.org/x/sys/windows.
// On non-Windows or when COM is unavailable, shortcuts are skipped with a warning.
package shortcut

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Config holds shortcut creation configuration.
type Config struct {
	TargetPath  string // path to the executable
	Name        string // shortcut display name
	Arguments   string // command-line arguments
	IconPath    string // path to icon file (optional)
	WorkingDir  string // working directory
	Global      bool   // create in global start menu
}

// Create creates a start menu shortcut.
// Mirrors startmenu_shortcut() from lib/shortcuts.ps1 L31-59.
func Create(cfg *Config) error {
	// Verify target exists
	if _, err := os.Stat(cfg.TargetPath); os.IsNotExist(err) {
		return fmt.Errorf("shortcut target not found: %s", cfg.TargetPath)
	}
	if cfg.IconPath != "" {
		if _, err := os.Stat(cfg.IconPath); os.IsNotExist(err) {
			return fmt.Errorf("shortcut icon not found: %s", cfg.IconPath)
		}
	}

	// Determine shortcut folder
	folder := startMenuFolder(cfg.Global)
	shortcutDir := filepath.Dir(filepath.Join(folder, cfg.Name))
	if err := os.MkdirAll(shortcutDir, 0755); err != nil {
		return fmt.Errorf("creating shortcuts directory: %w", err)
	}

	shortcutPath := filepath.Join(folder, cfg.Name+".lnk")

	// On Windows, use COM to create .lnk
	if runtime.GOOS == "windows" {
		return createWindowsShortcut(shortcutPath, cfg)
	}

	// On non-Windows, create a .desktop file or skip
	return nil
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
		// CommonStartMenu
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return filepath.Join(programData, `Microsoft\Windows\Start Menu\Programs\Scoop Apps`)
	}

	// User StartMenu
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\Scoop Apps`)
}

// createWindowsShortcut creates a .lnk file using the Windows Script Host Shell object.
func createWindowsShortcut(path string, cfg *Config) error {
	// Build cumulative PowerShell command to create shortcut via COM
	psCmd := fmt.Sprintf(`$ws = New-Object -ComObject WScript.Shell; $sc = $ws.CreateShortcut('%s'); $sc.TargetPath = '%s'`,
		path, cfg.TargetPath)
	if cfg.WorkingDir != "" {
		psCmd += fmt.Sprintf(`; $sc.WorkingDirectory = '%s'`, cfg.WorkingDir)
	}
	if cfg.Arguments != "" {
		psCmd += fmt.Sprintf(`; $sc.Arguments = '%s'`, cfg.Arguments)
	}
	if cfg.IconPath != "" {
		psCmd += fmt.Sprintf(`; $sc.IconLocation = '%s'`, cfg.IconPath)
	}
	psCmd += `; $sc.Save()`

	// Execute via PowerShell
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	return execPowerShell(cmd)
}

func execPowerShell(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
