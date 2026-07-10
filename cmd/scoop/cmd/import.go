package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <path>",
	Short: "Imports apps, buckets and configs from a Scoopfile in JSON format",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]

		// Read scoopfile data (from URL or local path)
		data, err := readScoopfileData(path)
		if err != nil {
			return fmt.Errorf("reading import file: %w", err)
		}

		var scoopfile struct {
			Buckets []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"buckets"`
			Apps []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Global  bool   `json:"global,omitempty"`
			} `json:"apps"`
			Config map[string]interface{} `json:"config,omitempty"`
		}

		if err := json.Unmarshal(data, &scoopfile); err != nil {
			return fmt.Errorf("parsing scoopfile: %w", err)
		}

		// Import buckets
		for _, b := range scoopfile.Buckets {
			if bucket.IsLocal(b.Name) {
				app.LogInfo("Bucket '%s' already exists, skipping.", b.Name)
				continue
			}
			repo := b.Source
			if repo == "" {
				if r, ok := bucket.Repo(b.Name); ok {
					repo = r
				} else {
					app.LogWarn("Unknown bucket '%s', skipping.", b.Name)
					continue
				}
			}
			if err := bucket.Add(b.Name, repo); err != nil {
				app.LogWarn("Adding bucket '%s': %v", b.Name, err)
			}
		}

		// Import apps
		arch := install.GetArchitecture("")
		for _, a := range scoopfile.Apps {
			app.LogInfo("Installing '%s'...", a.Name)

			m, bucketName, err := install.FindManifest(a.Name)
			if err != nil {
				app.LogWarn("Finding manifest for '%s': %v", a.Name, err)
				continue
			}

			version := a.Version
			if version == "" {
				version = m.Version
			}

			engine := &install.Engine{
				AppName:   a.Name,
				Manifest:  m,
				Bucket:    bucketName,
				Version:   version,
				Arch:      arch,
				Global:    a.Global,
				UseCache:  true,
				CheckHash: true,
			}

			if err := engine.Install(context.Background()); err != nil {
				app.LogWarn("Installing '%s': %v", a.Name, err)
			}
		}

		// Import config
		if len(scoopfile.Config) > 0 {
			cfg := app.Config()
			for k, v := range scoopfile.Config {
				cfg.Set(k, v)
			}
			cfg.Save()
		}

		app.LogSuccess("Import completed.")
		return nil
	},
}

// readScoopfileData reads the scoopfile contents from a URL or local file path.
func readScoopfileData(path string) ([]byte, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// Download from URL
		resp, err := http.Get(path)
		if err != nil {
			return nil, fmt.Errorf("downloading scoopfile: %w", err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading scoopfile response: %w", err)
		}
		return data, nil
	}

	// Read from local file path
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading local file: %w", err)
	}
	return data, nil
}

func init() {
	rootCmd.AddCommand(importCmd)
}
