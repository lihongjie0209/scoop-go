package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var holdFlags struct {
	global bool
}

var holdCmd = &cobra.Command{
	Use:   "hold <app> [app...]",
	Short: "Hold an app to disable updates",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var firstErr error
		for _, name := range args {
			if err := setHold(name, true, holdFlags.global); err != nil {
				app.LogError("%v", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	},
}

var unholdCmd = &cobra.Command{
	Use:   "unhold <app> [app...]",
	Short: "Unhold an app to enable updates",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var firstErr error
		for _, name := range args {
			if err := setHold(name, false, holdFlags.global); err != nil {
				app.LogError("%v", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	},
}

func setHold(appName string, hold, global bool) error {
	// Special case: 'scoop' app uses the hold_update_until config key.
	// PowerShell Scoop holds self-updates for one day.
	if appName == "scoop" {
		cfg := app.Config()
		if hold {
			until := time.Now().Add(24 * time.Hour).Format("2006-01-02")
			cfg.Set("hold_update_until", until)
		} else {
			cfg.Set("hold_update_until", "")
		}
		cfg.Save()
		if hold {
			app.LogSuccess("'scoop' has been held (updates disabled until %s).",
				time.Now().Add(24*time.Hour).Format("2006-01-02"))
		} else {
			app.LogSuccess("'scoop' has been unheld (updates enabled).")
		}
		return nil
	}

	var installPath string
	scopes := []bool{global}
	if !global {
		scopes = []bool{false, true}
	}

	found := false
	for _, g := range scopes {
		path := filepath.Join(app.AppCurrentDir(appName, g), "install.json")
		if _, err := os.Stat(path); err == nil {
			installPath = path
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("'%s' isn't installed", appName)
	}

	data, err := os.ReadFile(installPath)
	if err != nil {
		return fmt.Errorf("reading install info: %w", err)
	}

	var info map[string]interface{}
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("parsing install info: %w", err)
	}

	currentHold, _ := info["hold"].(bool)
	if currentHold == hold {
		if hold {
			app.LogInfo("'%s' is already held.", appName)
		} else {
			app.LogInfo("'%s' is not held.", appName)
		}
		return nil
	}

	info["hold"] = hold
	newData, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling install info: %w", err)
	}
	if err := os.WriteFile(installPath, newData, 0644); err != nil {
		return fmt.Errorf("writing install info: %w", err)
	}

	if hold {
		app.LogSuccess("'%s' has been held.", appName)
	} else {
		app.LogSuccess("'%s' has been unheld.", appName)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(holdCmd)
	rootCmd.AddCommand(unholdCmd)
	holdCmd.Flags().BoolVarP(&holdFlags.global, "global", "g", false, "Hold globally installed app")
	unholdCmd.Flags().BoolVarP(&holdFlags.global, "global", "g", false, "Unhold globally installed app")
}
