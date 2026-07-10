package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/spf13/cobra"
)

var exportFlags struct {
	config bool
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Exports installed apps, buckets (and optionally configs) in JSON format",
	RunE: func(cmd *cobra.Command, args []string) error {
		export := map[string]interface{}{
			"generated": time.Now().Format(time.RFC3339),
		}

		// Export buckets
		buckets := bucket.ListLocal()
		var bucketList []map[string]interface{}
		for _, b := range buckets {
			bucketList = append(bucketList, map[string]interface{}{
				"name":   b.Name,
				"source": b.Source,
			})
		}
		export["buckets"] = bucketList

		// Export installed apps
		var appsList []map[string]interface{}
		for _, g := range []bool{false, true} {
			entries, _ := os.ReadDir(app.AppDir(g))
			for _, e := range entries {
				if e.IsDir() && e.Name() != "scoop" {
					version := ""
					currentPath := filepath.Join(app.AppDir(g), e.Name(), "current")
					if target, err := os.Readlink(currentPath); err == nil {
						version = filepath.Base(target)
					}
					item := map[string]interface{}{
						"name":   e.Name(),
						"version": version,
					}
					if g {
						item["global"] = true
					}
					appsList = append(appsList, item)
				}
			}
		}
		export["apps"] = appsList

		// Optionally export config
		if exportFlags.config {
			cfg := app.Config().Config()
			export["config"] = cfg
		}

		data, _ := json.MarshalIndent(export, "", "  ")
		fmt.Println(string(data))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().BoolVarP(&exportFlags.config, "config", "c", false, "Export config too")
}
