package cmd

import (
	"context"
	"fmt"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/status"
	"github.com/scoopinstaller/scoop-go/pkg/update"
	"github.com/spf13/cobra"
)

var updateFlags struct {
	force     bool
	global    bool
	quiet     bool
	all       bool
	noCache   bool
	skipHash  bool
	independent bool
}

var updateCmd = &cobra.Command{
	Use:   "update [app]",
	Short: "Update apps, or Scoop itself",
	Long: `Update Scoop to the latest version, or update specific apps.

'scoop update' updates Scoop to the latest version.
'scoop update <app>' installs a new version of that app, if there is one.

You can use '*' or '--all' in place of <app> to update all apps.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hasApps := len(args) > 0 || updateFlags.all

		if !hasApps {
			// Update Scoop itself and all buckets
			if updateFlags.global {
				return fmt.Errorf("--global is invalid when <app> is not specified")
			}

			if err := update.SyncScoop(); err != nil {
				return fmt.Errorf("updating scoop: %w", err)
			}

			if err := update.SyncBuckets(); err != nil {
				return fmt.Errorf("updating buckets: %w", err)
			}

			app.LogSuccess("Scoop was updated successfully!")
			return nil
		}

		// Update specific apps
		if updateFlags.all || (len(args) == 1 && args[0] == "*") {
			return updateAllApps()
		}

		for _, appName := range args {
			if appName == "scoop" {
				if err := update.SyncScoop(); err != nil {
					return err
				}
				app.LogSuccess("Scoop was updated successfully!")
				continue
			}

			if err := update.UpdateApp(context.Background(), appName,
				updateFlags.global, updateFlags.force, updateFlags.quiet,
				!updateFlags.noCache, !updateFlags.skipHash); err != nil {
				app.LogError("Updating '%s': %v", appName, err)
			}
		}

		return nil
	},
}

// updateAllApps updates all installed apps that have newer versions available.
func updateAllApps() error {
	app.LogInfo("Updating all apps...")

	statuses := status.CheckAppStatuses()

	var outdated []status.AppStatus
	for _, s := range statuses {
		if s.Outdated {
			outdated = append(outdated, s)
		}
	}

	if len(outdated) == 0 {
		app.LogSuccess("All apps are up to date!")
		return nil
	}

	updated := 0
	skipped := 0
	failed := 0

	for _, s := range outdated {
		if s.Hold {
			app.LogWarn("Skipping '%s' (%s): app is on hold", s.Name, s.Version)
			skipped++
			continue
		}

		app.LogInfo("Updating '%s' (%s -> %s)...", s.Name, s.Version, s.LatestVersion)

		if err := update.UpdateApp(context.Background(), s.Name,
			s.Global, false, false,
			!updateFlags.noCache, !updateFlags.skipHash); err != nil {
			app.LogError("Failed to update '%s': %v", s.Name, err)
			failed++
		} else {
			app.LogSuccess("'%s' updated successfully!", s.Name)
			updated++
		}
	}

	app.LogInfo("Summary: %d updated, %d skipped (on hold), %d failed", updated, skipped, failed)
	return nil
}

func init() {
	rootCmd.AddCommand(updateCmd)

	updateCmd.Flags().BoolVarP(&updateFlags.force, "force", "f", false, "Force update even when up to date")
	updateCmd.Flags().BoolVarP(&updateFlags.global, "global", "g", false, "Update a globally installed app")
	updateCmd.Flags().BoolVarP(&updateFlags.quiet, "quiet", "q", false, "Hide extraneous messages")
	updateCmd.Flags().BoolVarP(&updateFlags.all, "all", "a", false, "Update all apps")
	updateCmd.Flags().BoolVarP(&updateFlags.noCache, "no-cache", "k", false, "Don't use cache")
	updateCmd.Flags().BoolVarP(&updateFlags.skipHash, "skip-hash-check", "s", false, "Skip hash check")
	updateCmd.Flags().BoolVarP(&updateFlags.independent, "independent", "i", false, "Skip dependencies")
}
