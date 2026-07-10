package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var prefixCmd = &cobra.Command{
	Use:   "prefix <app>",
	Short: "Returns the path to the specified app",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := strings.TrimSuffix(args[0], ".json")

		for _, global := range []bool{false, true} {
			currentPath := app.AppCurrentDir(appName, global)
			if info, err := os.Stat(currentPath); err == nil && info.IsDir() {
				// Try to resolve junction/symlink
				if target, err := os.Readlink(currentPath); err == nil {
					fmt.Println(target)
				} else {
					fmt.Println(currentPath)
				}
				return nil
			}
		}

		return fmt.Errorf("'%s' isn't installed", appName)
	},
}

func init() {
	rootCmd.AddCommand(prefixCmd)
}
