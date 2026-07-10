package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/spf13/cobra"
)

var whichCmd = &cobra.Command{
	Use:   "which <command>",
	Short: "Locate a shim/executable",
	Long:  "Locate the path to a shim/executable that was installed with Scoop (similar to 'which' on Linux).",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		command := args[0]

		// Check shim directories
		for _, global := range []bool{false, true} {
			shimDir := app.ShimDir(global)
			entries, err := os.ReadDir(shimDir)
			if err != nil {
				continue
			}

			for _, entry := range entries {
				name := entry.Name()
				base := strings.TrimSuffix(name, filepath.Ext(name))

				if !strings.EqualFold(base, command) {
					continue
				}

				ext := filepath.Ext(name)
				switch ext {
				case ".shim":
					target := shim.ResolveShimTarget(filepath.Join(shimDir, name))
					if target != "" {
						fmt.Println(target)
						return nil
					}
				case ".exe", ".cmd", ".ps1":
					// For .shim-less entries, read the wrapper for the target path
					target := shim.ResolveWrapperTarget(filepath.Join(shimDir, name))
					if target != "" {
						fmt.Println(target)
						return nil
					}
					fmt.Println(filepath.Join(shimDir, name))
					return nil
				}
			}
		}

		return fmt.Errorf("couldn't find '%s' in any shim directory", command)
	},
}

func init() {
	rootCmd.AddCommand(whichCmd)
}
