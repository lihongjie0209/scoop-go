package cmd

import (
	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/spf13/cobra"
)

// DefaultBuckets is the list of buckets added by `scoop init`.
var DefaultBuckets = []string{"main", "extras", "java", "nerd-fonts"}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Scoop with default buckets",
	Long: `Adds all default Scoop buckets so you can start installing apps.

Default buckets:
  main       - Core apps (required)
  extras     - Extra apps (recommended)
  java       - Java-related apps
  nerd-fonts - Nerd Fonts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		added := 0
		for _, name := range DefaultBuckets {
			if bucket.IsLocal(name) {
				app.LogDebug("Bucket '%s' already exists, skipping.", name)
				continue
			}
			repo, ok := bucket.Repo(name)
			if !ok {
				app.LogWarn("Unknown bucket '%s', skipping.", name)
				continue
			}
			app.LogInfo("Adding '%s' bucket...", name)
			if err := bucket.Add(name, repo); err != nil {
				app.LogWarn("Failed to add '%s' bucket: %v", name, err)
				continue
			}
			added++
		}

		if added == 0 {
			app.LogInfo("All default buckets already exist.")
		} else {
			app.LogSuccess("Added %d default bucket(s).", added)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
