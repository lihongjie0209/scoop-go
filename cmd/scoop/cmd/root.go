package cmd

import (
	"fmt"
	"os"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	debug   bool
)

var rootCmd = &cobra.Command{
	Use:   "scoop",
	Short: "Scoop - A command-line installer for Windows",
	Long: `Scoop installs programs from the command line with a minimal amount of friction.
	It runs on Windows and is designed to be fast, lightweight, and easy to use.

	Scoop is inspired by Homebrew (macOS) and apt (Linux).
	It installs programs to ~/scoop by default, keeping them isolated from the system.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := app.Initialize(cfgFile); err != nil {
			return fmt.Errorf("failed to initialize scoop: %w", err)
		}
		if debug {
			app.Config().Config().Debug = true
			app.GetLogger().SetDebug(true)
		}
		// Suggest 'scoop init' if no buckets exist
		if app.NeedsMainBucket() && cmd.Name() != "init" {
			app.LogInfo("Tip: Run 'scoop init' to add default buckets.")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
	SilenceErrors: true,
	SilenceUsage:  true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Print to stderr directly in case the logger is not initialized
		// (logger is nil when cobra returns early from Find() error
		// without calling PersistentPreRunE, e.g. unknown commands).
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		app.LogError("%s", err.Error())
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize()
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.config/scoop/config.json)")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "enable debug output")
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
