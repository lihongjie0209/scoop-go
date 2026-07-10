package cmd

import (
	"github.com/scoopinstaller/scoop-go/pkg/diagnostic"
	"github.com/spf13/cobra"
)

var checkupCmd = &cobra.Command{
	Use:   "checkup",
	Short: "Check for potential problems",
	Long:  "Performs diagnostic tests to identify issues with your Scoop installation.",
	RunE: func(cmd *cobra.Command, args []string) error {
		diagnostic.RunAllAndPrint()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkupCmd)
}
