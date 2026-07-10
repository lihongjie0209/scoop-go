package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/db"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search available apps",
	Long:  "Searches for apps that are available to install from any added bucket. Matches against app names, binaries, and shortcuts.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = strings.ToLower(args[0])
		}

					if db.IsEnabled() {
				return searchWithCache(query)
			}
		localBuckets := bucket.ListLocal()
		if len(localBuckets) == 0 {
			app.LogWarn("No buckets found. Add a bucket first with 'scoop bucket add main'")
			return nil
		}

		found := 0
		for _, b := range localBuckets {
			manifestDir := bucket.ManifestDir(b.Name)
			entries, err := os.ReadDir(manifestDir)
			if err != nil {
				continue
			}

			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}

				name := strings.TrimSuffix(entry.Name(), ".json")
				if query == "" || strings.Contains(strings.ToLower(name), query) {
					// Check if it's installed
					installed := ""
					if isAppInstalledLocally(name) {
						installed = " [installed]"
					}

					// Try to read bin names
					bins := ""
					data, err := os.ReadFile(filepath.Join(manifestDir, entry.Name()))
					if err == nil {
						m, err := manifest.Parse(data)
						if err == nil && m != nil {
							binEntries := manifest.BinEntries(m.GetBin("64bit"))
							var binNames []string
							for _, be := range binEntries {
								binNames = append(binNames, be[1])
							}
							if len(binNames) > 0 {
								bins = " (bin: " + strings.Join(binNames, ", ") + ")"
							}
						}
					}

					fmt.Printf("%-30s %s bucket%s%s\n", name, b.Name, bins, installed)
					found++
				}
			}
		}

		if found == 0 && query != "" {
			app.LogInfo("No apps found matching '%s'", query)
		} else if found > 0 {
			app.LogInfo("%d app(s) found", found)
		}

		return nil
	},
}

func isAppInstalledLocally(name string) bool {
	for _, g := range []bool{false, true} {
		currentPath := app.AppCurrentDir(name, g)
		if _, err := os.Stat(currentPath); err == nil {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(searchCmd)
}

// searchWithCache searches using the SQLite cache.
func searchWithCache(query string) error {
	results, err := db.Search(query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		app.LogInfo("No apps found matching '%s'", query)
		return nil
	}

	for _, r := range results {
		installed := ""
		if isAppInstalledLocally(r.Name) {
			installed = " [installed]"
		}
		bins := ""
		if r.Binary != "" {
			bins = " (bin: " + r.Binary + ")"
		}
		fmt.Printf("%-30s %s bucket%s%s\n", r.Name, r.Bucket, bins, installed)
	}
	app.LogInfo("%d app(s) found", len(results))
	return nil
}
