package cmd

import (
	"fmt"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
)

var homeCmd = &cobra.Command{
	Use:   "home <app>",
	Short: "Opens the app homepage",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := strings.TrimSuffix(args[0], ".json")

		_, manifestPath := bucket.AppManifestPath(appName)
		if manifestPath == "" {
			return fmt.Errorf("couldn't find manifest for '%s'", appName)
		}

		m, err := manifest.ParseFile(manifestPath)
		if err != nil {
			return fmt.Errorf("parsing manifest: %w", err)
		}

		if m.Homepage == "" {
			return fmt.Errorf("no homepage defined for '%s'", appName)
		}

		app.LogInfo("Opening '%s' homepage: %s", appName, m.Homepage)
		return open.Run(m.Homepage)
	},
}

func init() {
	rootCmd.AddCommand(homeCmd)
}
