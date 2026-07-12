package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/spf13/cobra"
)

var exportFlags struct {
	config bool
}

// exportApp matches PowerShell scoop-list / scoop-export app objects.
type exportApp struct {
	Name    string    `json:"Name"`
	Version string    `json:"Version"`
	Source  string    `json:"Source,omitempty"`
	Updated time.Time `json:"Updated,omitempty"`
	Info    string    `json:"Info,omitempty"`
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Exports installed apps, buckets (and optionally configs) in JSON format",
	RunE: func(cmd *cobra.Command, args []string) error {
		export := map[string]interface{}{}

		// Buckets (Name/Source casing matches PS ConvertToPrettyJson objects loosely via map)
		var bucketList []map[string]interface{}
		for _, b := range bucket.ListLocal() {
			bucketList = append(bucketList, map[string]interface{}{
				"Name":   b.Name,
				"Source": b.Source,
			})
		}
		export["buckets"] = bucketList

		// Apps in PowerShell scoop list shape for import compatibility
		var appsList []exportApp
		for _, g := range []bool{false, true} {
			entries, err := os.ReadDir(app.AppDir(g))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() || e.Name() == "scoop" {
					continue
				}
				name := e.Name()
				version, info, failed := getAppDetails(name, g)

				source := info.Bucket
				if source == "" {
					source = info.URL
				}

				var infoParts []string
				if failed {
					infoParts = append(infoParts, "Install failed")
				}
				if info.Hold {
					infoParts = append(infoParts, "Held package")
				}
				if info.Architecture != "" {
					infoParts = append(infoParts, info.Architecture)
				}
				if info.Bucket != "" {
					deprecatedPath := filepath.Join(bucket.Dir(info.Bucket), "deprecated", name+".json")
					if _, err := os.Stat(deprecatedPath); err == nil {
						infoParts = append(infoParts, "Deprecated package")
					}
				}
				if g {
					infoParts = append(infoParts, "Global install")
				}

				updated := time.Time{}
				if st, err := os.Stat(filepath.Join(app.AppDir(g), name)); err == nil {
					updated = st.ModTime()
				}
				installPath := filepath.Join(app.AppCurrentDir(name, g), "install.json")
				if st, err := os.Stat(installPath); err == nil {
					updated = st.ModTime()
				}

				appsList = append(appsList, exportApp{
					Name:    name,
					Version: version,
					Source:  source,
					Updated: updated,
					Info:    strings.Join(infoParts, ", "),
				})
			}
		}
		export["apps"] = appsList

		if exportFlags.config {
			cfg := app.Config().Config()
			// Mirror PS: strip machine-specific properties
			raw, _ := json.Marshal(cfg)
			var cfgMap map[string]interface{}
			_ = json.Unmarshal(raw, &cfgMap)
			for _, k := range []string{"last_update", "root_path", "global_path", "cache_path", "alias"} {
				delete(cfgMap, k)
			}
			export["config"] = cfgMap
		}

		// Keep generated timestamp as extra (harmless for PS import)
		export["generated"] = time.Now().Format(time.RFC3339)

		data, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().BoolVarP(&exportFlags.config, "config", "c", false, "Export config too")
}
