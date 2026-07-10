package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:   "cat <app>",
	Short: "Show content of specified manifest",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := strings.TrimSuffix(args[0], ".json")

		// Look for manifest in buckets
		_, manifestPath := bucket.AppManifestPath(appName)
		if manifestPath == "" {
			return fmt.Errorf("couldn't find manifest for '%s'", appName)
		}

		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return err
		}

		fmt.Print(string(data))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(catCmd)
}
