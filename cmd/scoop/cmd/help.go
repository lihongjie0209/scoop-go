package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var helpCmd = &cobra.Command{
	Use:   "help [command]",
	Short: "Show help for a command",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return rootCmd.Help()
		}

		// Look for the command
		cmdName := args[0]
		for _, c := range rootCmd.Commands() {
			if c.Name() == cmdName || strings.Contains(c.Name(), cmdName) {
				return c.Help()
			}
		}

		return fmt.Errorf("unknown command: %s", cmdName)
	},
}

func init() {
	rootCmd.AddCommand(helpCmd)
}
