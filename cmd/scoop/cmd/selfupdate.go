package cmd

import (
	"github.com/scoopinstaller/scoop-go/pkg/update"
	"github.com/spf13/cobra"
)

var selfUpdateReplaceFlags struct {
	pid    int
	target string
	staged string
	helper string
}

var selfUpdateReplaceCmd = &cobra.Command{
	Use:    "__self-update-replace",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return update.RunReplaceHelper(selfUpdateReplaceFlags.pid, selfUpdateReplaceFlags.target, selfUpdateReplaceFlags.staged, selfUpdateReplaceFlags.helper)
	},
}

func init() {
	rootCmd.AddCommand(selfUpdateReplaceCmd)
	selfUpdateReplaceCmd.Flags().IntVar(&selfUpdateReplaceFlags.pid, "pid", 0, "parent process id")
	selfUpdateReplaceCmd.Flags().StringVar(&selfUpdateReplaceFlags.target, "target", "", "target executable")
	selfUpdateReplaceCmd.Flags().StringVar(&selfUpdateReplaceFlags.staged, "staged", "", "staged executable")
	selfUpdateReplaceCmd.Flags().StringVar(&selfUpdateReplaceFlags.helper, "helper", "", "temporary helper executable")
}
