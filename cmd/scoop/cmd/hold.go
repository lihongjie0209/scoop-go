package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var holdFlags struct {
	global bool
}

var holdCmd = &cobra.Command{
	Use:   "hold <app>",
	Short: "Hold an app to disable updates",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setHold(args[0], true, holdFlags.global)
	},
}

var unholdCmd = &cobra.Command{
	Use:   "unhold <app>",
	Short: "Unhold an app to enable updates",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setHold(args[0], false, holdFlags.global)
	},
}

func setHold(appName string, hold, global bool) error {
	// Special case: 'scoop' app uses the hold_update_until config key
	if appName == "scoop" {
		cfg := app.Config()
		if hold {
			cfg.Set("hold_update_until", "2100-01-01")
		} else {
			cfg.Set("hold_update_until", "")
		}
		cfg.Save()
		if hold {
			app.LogSuccess("'scoop' has been held (updates disabled until 2100).")
		} else {
			app.LogSuccess("'scoop' has been unheld (updates enabled).")
		}
		return nil
	}

	// Find the install.json for the app
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

	// Check current hold state
	currentHold, _ := info["hold"].(bool)
	if currentHold == hold {
		if hold {
			app.LogInfo("'%s' is already held.", appName)
		} else {
			app.LogInfo("'%s' is not held.", appName)
		}
		return nil
	}

	// Set the hold state
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
