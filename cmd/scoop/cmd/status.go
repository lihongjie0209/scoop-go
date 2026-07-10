package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/status"
	"github.com/spf13/cobra"
)

var statusFlags struct {
	local bool
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status and check for new app versions",
	RunE: func(cmd *cobra.Command, args []string) error {
		noRemotes := statusFlags.local

		// Check scoop update
		if !noRemotes {
			scoopStatus := status.CheckScoopUpdate()
			if scoopStatus.NetworkFailure {
				app.LogWarn("Network failure when checking scoop update")
			} else if scoopStatus.ScoopOutdated {
				app.LogWarn("Scoop out of date. Run 'scoop update' to get the latest changes.")
			}

			bucketStatus := status.CheckBucketUpdates()
			if bucketStatus.NetworkFailure {
				app.LogWarn("Network failure when checking buckets")
			} else if bucketStatus.BucketOutdated {
				app.LogWarn("Scoop bucket(s) out of date. Run 'scoop update' to get the latest changes.")
			}

			if !scoopStatus.ScoopOutdated && !bucketStatus.BucketOutdated && !scoopStatus.NetworkFailure && !bucketStatus.NetworkFailure {
				color.New(color.FgGreen).Println("Scoop is up to date.")
			}
		}

		// Check app statuses
		statuses := status.CheckAppStatuses()
		hasIssues := false

		for _, s := range statuses {
			if !s.Outdated && !s.Failed && !s.Deprecated && !s.Removed && len(s.MissingDeps) == 0 {
				continue
			}
			hasIssues = true

			scope := ""
			if s.Global {
				scope = " (global)"
			}

			var info []string
			if s.Failed {
				info = append(info, "Install failed")
			}
			if s.Hold {
				info = append(info, "Held package")
			}
			if s.Deprecated {
				info = append(info, "Deprecated")
			}
			if s.Removed {
				info = append(info, "Manifest removed")
			}

			line := fmt.Sprintf("  %s%s", s.Name, scope)
			line += fmt.Sprintf(": installed %s", s.Version)

			if s.Outdated && s.LatestVersion != "" {
				line += fmt.Sprintf(" (latest: %s)", s.LatestVersion)
			}
			if len(s.MissingDeps) > 0 {
				line += fmt.Sprintf(" [missing deps: %s]", strings.Join(s.MissingDeps, ", "))
			}
			if len(info) > 0 {
				line += fmt.Sprintf(" [%s]", strings.Join(info, ", "))
			}

			fmt.Println(line)
		}

		if !hasIssues && !noRemotes {
			color.New(color.FgGreen).Println("Everything is ok!")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVarP(&statusFlags.local, "local", "l", false, "Only check local status, no remote fetching")
}
