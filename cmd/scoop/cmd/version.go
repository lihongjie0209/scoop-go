package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information (set via ldflags at build time)
var (
	Version   = "0.1.0"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Scoop Go v%s\n", Version)
		fmt.Printf("Build date: %s\n", BuildDate)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
