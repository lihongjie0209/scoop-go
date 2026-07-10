package cmd

import (
	"fmt"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/db"
	"github.com/spf13/cobra"
)

// bucketCmd represents the `scoop bucket` command
var bucketCmd = &cobra.Command{
	Use:   "bucket [add|list|known|rm] [name] [repo]",
	Short: "Manage Scoop buckets",
	Long:  `Add, list, or remove buckets. Buckets are repositories of apps available to install.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}

		subcmd := args[0]
		switch subcmd {
		case "add":
			if len(args) < 2 {
				return fmt.Errorf("usage: scoop bucket add <name> [<repo>]")
			}
			name := args[1]
			repo := ""
			if len(args) >= 3 {
				repo = args[2]
			} else {
				var ok bool
				repo, ok = bucket.Repo(name)
				if !ok {
					return fmt.Errorf("unknown bucket '%s'. Try specifying <repo>", name)
				}
			}
			if err := bucket.Add(name, repo); err != nil { return err }
				if db.IsEnabled() { db.RebuildAll() }
				return nil

		case "rm":
			if len(args) < 2 {
				return fmt.Errorf("usage: scoop bucket rm <name>")
			}
			if err := bucket.Remove(args[1]); err != nil { return err }
				if db.IsEnabled() { db.RebuildAll() }
				return nil

		case "list":
			buckets := bucket.ListLocal()
			if len(buckets) == 0 {
				app.LogWarn("No bucket found. Please run 'scoop bucket add main' to add the default 'main' bucket.")
				return nil
			}
			for _, b := range buckets {
				fmt.Printf("%-15s %-50s %-20s %d manifests\n",
					b.Name, b.Source, b.Updated, b.Manifests)
			}
			return nil

		case "known":
			for _, name := range bucket.Known() {
				fmt.Println(name)
			}
			return nil

		default:
			return fmt.Errorf("scoop bucket: cmd '%s' not supported", subcmd)
		}
	},
}

func init() {
	rootCmd.AddCommand(bucketCmd)
}
