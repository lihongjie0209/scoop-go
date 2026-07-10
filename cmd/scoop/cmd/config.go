package cmd

import (
	"fmt"
	"reflect"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config [rm] name [value]",
	Short: "Get or set configuration values",
	Long: `Get or set Scoop configuration values.

To get all configuration settings:
  scoop config

To get a configuration setting:
  scoop config <name>

To set a configuration setting:
  scoop config <name> <value>

To remove a configuration setting:
  scoop config rm <name>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := app.Config()

		if len(args) == 0 {
			// Print all config
			fmt.Printf("%-30s %v\n", "root_path", cfg.Config().RootPath)
			fmt.Printf("%-30s %v\n", "global_path", cfg.Config().GlobalPath)
			fmt.Printf("%-30s %v\n", "cache_path", cfg.Config().CachePath)
			fmt.Printf("%-30s %v\n", "proxy", cfg.Config().Proxy)
			fmt.Printf("%-30s %v\n", "scoop_repo", cfg.Config().SCOOPRepo)
			fmt.Printf("%-30s %v\n", "scoop_branch", cfg.Config().SCOOPBranch)
			fmt.Printf("%-30s %v\n", "aria2-enabled", boolPtrDisplay(cfg.Config().Aria2Enabled))
			fmt.Printf("%-30s %v\n", "aria2-warning-enabled", boolPtrDisplay(cfg.Config().Aria2WarningEnabled))
			fmt.Printf("%-30s %v\n", "aria2-retry-wait", cfg.Config().Aria2RetryWait)
			fmt.Printf("%-30s %v\n", "aria2-split", cfg.Config().Aria2Split)
			fmt.Printf("%-30s %v\n", "aria2-max-connection-per-server", cfg.Config().Aria2MaxConnPerServer)
			fmt.Printf("%-30s %v\n", "aria2-min-split-size", cfg.Config().Aria2MinSplitSize)
			fmt.Printf("%-30s %v\n", "debug", cfg.Config().Debug)
			fmt.Printf("%-30s %v\n", "force_update", cfg.Config().ForceUpdate)
			fmt.Printf("%-30s %v\n", "show_update_log", boolPtrDisplay(cfg.Config().ShowUpdateLog))
			fmt.Printf("%-30s %v\n", "use_sqlite_cache", cfg.Config().UseSQLiteCache)
			fmt.Printf("%-30s %v\n", "no_junction", cfg.Config().NoJunction)
			fmt.Printf("%-30s %v\n", "use_isolated_path", cfg.Config().UseIsolatedPath)
			fmt.Printf("%-30s %v\n", "ignore_running_processes", cfg.Config().IgnoreRunningProcesses)
			fmt.Printf("%-30s %v\n", "last_update", cfg.Config().LastUpdate)
			return nil
		}

		if args[0] == "rm" {
			if len(args) < 2 {
				return fmt.Errorf("usage: scoop config rm <name>")
			}
			if err := cfg.Unset(args[1]); err != nil {
				return err
			}
			cfg.Save()
			fmt.Printf("'%s' has been removed\n", args[1])
			return nil
		}

		name := args[0]
		if len(args) < 2 {
			// Get single value
			val := cfg.Get(name)
			if val == nil {
				fmt.Printf("'%s' is not set\n", name)
			} else {
				fmt.Println(displayConfigValue(val))
			}
			return nil
		}

		// Set value
		value := args[1]
		if err := cfg.Set(name, value); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("'%s' has been set to '%s'\n", name, value)
		return nil
	},
}

func boolPtrDisplay(b *bool) string {
	if b == nil {
		return "<not set>"
	}
	return fmt.Sprintf("%v", *b)
}

// displayConfigValue formats a config value for display,
// dereferencing pointer types to show their actual values.
func displayConfigValue(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return "<nil>"
		}
		return fmt.Sprintf("%v", rv.Elem().Interface())
	}
	return fmt.Sprintf("%v", v)
}

func init() {
	rootCmd.AddCommand(configCmd)
}
