package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var aliasFlags struct {
	verbose bool
}

var aliasCmd = &cobra.Command{
	Use:   "alias add|rm|list [name] [command]",
	Short: "Manage scoop aliases",
	Long: `Available subcommands: add, rm, list.

  alias add <name> <command> [description]   Add an alias
  alias rm <name>                            Remove an alias
  alias list                                 List all aliases

Options:
  -v, --verbose   Show command body when listing`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		subcmd := args[0]
		switch subcmd {
		case "add":
			return aliasAdd(args[1:])
		case "rm":
			return aliasRemove(args[1:])
		case "list":
			return aliasList()
		default:
			return fmt.Errorf("scoop alias: unknown subcommand '%s'", subcmd)
		}
	},
}

func aliasAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: scoop alias add <name> <command> [description]")
	}
	name := args[0]
	command := args[1]
	description := ""
	if len(args) > 2 {
		description = args[2]
	}

	cfg := app.Config()
	aliases := cfg.Config().Alias
	if aliases == nil {
		aliases = make(map[string]string)
	}
	if _, exists := aliases[name]; exists {
		return fmt.Errorf("alias '%s' already exists", name)
	}

	// Create shim file
	shimFile := filepath.Join(app.ShimDir(false), "scoop-"+name+".ps1")
	content := fmt.Sprintf("# %s\n%s\n", description, command)
	if err := os.WriteFile(shimFile, []byte(content), 0755); err != nil {
		return fmt.Errorf("creating alias shim: %w", err)
	}

	aliases[name] = "scoop-" + name
	cfg.Config().Alias = aliases
	return cfg.Save()
}

func aliasRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop alias rm <name>")
	}
	name := args[0]

	cfg := app.Config()
	if cfg.Config().Alias == nil {
		return fmt.Errorf("alias '%s' doesn't exist", name)
	}
	if _, exists := cfg.Config().Alias[name]; !exists {
		return fmt.Errorf("alias '%s' doesn't exist", name)
	}

	// Remove shim file
	shimFile := filepath.Join(app.ShimDir(false), "scoop-"+name+".ps1")
	os.Remove(shimFile)

	delete(cfg.Config().Alias, name)
	return cfg.Save()
}

func aliasList() error {
	cfg := app.Config()
	if cfg.Config().Alias == nil || len(cfg.Config().Alias) == 0 {
		app.LogInfo("No alias found.")
		return nil
	}
	for name, alias := range cfg.Config().Alias {
		shimFile := filepath.Join(app.ShimDir(false), alias+".ps1")
		data, err := os.ReadFile(shimFile)
		desc := ""
		body := ""
		if err == nil {
			content := string(data)
			lines := strings.Split(content, "\n")
			if len(lines) > 0 && strings.HasPrefix(lines[0], "#") {
				desc = strings.TrimSpace(strings.TrimPrefix(lines[0], "#"))
				if len(lines) > 1 {
					body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
				}
			} else {
				body = strings.TrimSpace(content)
			}
		}
		if aliasFlags.verbose {
			fmt.Printf("%-20s %s\n  %s\n", name, desc, body)
		} else {
			fmt.Printf("%-20s %s\n", name, desc)
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(aliasCmd)
	aliasCmd.Flags().BoolVarP(&aliasFlags.verbose, "verbose", "v", false, "Show alias command body when listing")
}
