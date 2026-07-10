package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache show|rm [app]",
	Short: "Show or clear the download cache",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		subcmd := args[0]
		appName := ""
		if len(args) > 1 {
			appName = args[1]
		}

		cacheDir := app.Dirs().CacheDir

		switch subcmd {
		case "show":
			totalSize := int64(0)
			fileCount := 0

			entries, err := os.ReadDir(cacheDir)
			if err != nil {
				app.LogInfo("Cache is empty.")
				return nil
			}

			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				// Filter by app if specified
				if appName != "" && !stringsHasPrefix(e.Name(), appName+"#") {
					continue
				}
				info, _ := e.Info()
				totalSize += info.Size()
				fileCount++
			}

			if fileCount == 0 {
				app.LogInfo("Cache is empty.")
				return nil
			}

			fmt.Printf("%d file(s) in cache (total %s)\n", fileCount, formatSize(totalSize))
			return nil

		case "rm":
			entries, err := os.ReadDir(cacheDir)
			if err != nil {
				return nil
			}

			removed := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if appName != "" && !stringsHasPrefix(e.Name(), appName+"#") {
					continue
				}
				os.Remove(filepath.Join(cacheDir, e.Name()))
				removed++
			}

			if removed == 0 {
				app.LogInfo("No cached files to remove.")
			} else {
				app.LogSuccess("%d file(s) removed from cache.", removed)
			}
			return nil

		default:
			return fmt.Errorf("scoop cache: unknown subcommand '%s' (use show or rm)", subcmd)
		}
	},
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(cacheCmd)
}
