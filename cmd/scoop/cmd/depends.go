package cmd

import (
	"fmt"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/dependency"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/spf13/cobra"
)

var dependsFlags struct {
	arch string
}

var dependsCmd = &cobra.Command{
	Use:   "depends <app>",
	Short: "List dependencies for an app",
	Long:  "List dependencies for an app, in the order they'll be installed.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := strings.TrimSuffix(args[0], ".json")
		arch := install.GetArchitecture(dependsFlags.arch)

		resolved, err := dependency.Resolve(appName, arch)
		if err != nil {
			return err
		}

		// Resolve includes the root app as the last entry; show deps only when empty tree of deps
		depsOnly := resolved
		if len(resolved) > 0 && dependency.AppName(resolved[len(resolved)-1]) == dependency.AppName(appName) {
			depsOnly = resolved[:len(resolved)-1]
		}
		if len(depsOnly) == 0 {
			app.LogInfo("'%s' has no dependencies.", dependency.AppName(appName))
			return nil
		}

		app.LogInfo("Dependencies for '%s' (install order):", dependency.AppName(appName))
		for _, dep := range depsOnly {
			marker := ""
			if dependency.IsInstalled(dep) {
				marker = " (installed)"
			}
			fmt.Printf("  %s%s\n", dep, marker)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(dependsCmd)
	dependsCmd.Flags().StringVarP(&dependsFlags.arch, "arch", "a", "", "Architecture (32bit, 64bit, arm64)")
}
