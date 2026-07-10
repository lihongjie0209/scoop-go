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

var shimCmd = &cobra.Command{
	Use:   "shim add|rm|list|info|alter [name]",
	Short: "Manipulate Scoop shims",
	Long: `Available subcommands: add, rm, list, info, alter.

  add <name> <path> [args]   Add a shim
  rm <name>                  Remove a shim
  list                       List all shims
  info <name>                Show shim info
  alter <name> <key> <val>   Modify a shim property`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}

		subcmd := args[0]
		switch subcmd {
		case "add":
			return shimAdd(args[1:])
		case "rm":
			return shimRemove(args[1:])
		case "list":
			return shimList()
		case "info":
			return shimInfo(args[1:])
		case "alter":
			return shimAlter(args[1:])
		default:
			return fmt.Errorf("scoop shim: unknown subcommand '%s'", subcmd)
		}
	},
}

func shimAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: scoop shim add <name> <path> [args]")
	}
	name := args[0]
	path := args[1]
	extraArgs := ""
	if len(args) > 2 {
		extraArgs = args[2]
	}

	shimDir := app.ShimDir(false)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", path)
	}

	return shim.Create(&shim.Config{
		TargetPath: path,
		Name:       name,
		Args:       extraArgs,
		ShimDir:    shimDir,
	})
}

func shimRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop shim rm <name>")
	}
	name := args[0]
	shimDir := app.ShimDir(false)
	return shim.Remove(name, shimDir, "")
}

func shimList() error {
	shimDir := app.ShimDir(false)
	entries, err := os.ReadDir(shimDir)
	if err != nil {
		app.LogInfo("No shims found.")
		return nil
	}

	shims := make(map[string][]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		base := strings.TrimSuffix(e.Name(), ext)
		shims[base] = append(shims[base], ext)
	}

	for name, exts := range shims {
		target := shim.ResolveShimTarget(filepath.Join(shimDir, name+".shim"))
		if target == "" {
			target = "(unknown)"
		}
		fmt.Printf("%-20s %s\n", name, target)
		_ = exts
	}

	if len(shims) == 0 {
		app.LogInfo("No shims found.")
	}

	return nil
}

func shimInfo(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop shim info <name>")
	}
	name := args[0]
	shimDir := app.ShimDir(false)

	// Read .shim file
	shimPath := filepath.Join(shimDir, name+".shim")
	target := shim.ResolveShimTarget(shimPath)
	if target == "" {
		// Try resolving from .cmd wrapper
		cmdPath := filepath.Join(shimDir, name+".cmd")
		target = shim.ResolveWrapperTarget(cmdPath)
	}
	if target == "" {
		return fmt.Errorf("shim '%s' not found", name)
	}

	fmt.Printf("Name:   %s\n", name)
	fmt.Printf("Target: %s\n", target)
	fmt.Printf("Path:   %s\n", filepath.Join(shimDir, name))
	return nil
}

func shimAlter(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: scoop shim alter <name> <key> <value>")
	}
	name := args[0]
	key := args[1]
	val := args[2]

	shimDir := app.ShimDir(false)
	shimPath := filepath.Join(shimDir, name+".shim")

	if _, err := os.Stat(shimPath); os.IsNotExist(err) {
		return fmt.Errorf("shim '%s' not found", name)
	}

	data, err := os.ReadFile(shimPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+" = ") {
			lines[i] = fmt.Sprintf("%s = %s", key, val)
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("%s = %s", key, val))
	}

	if err := os.WriteFile(shimPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return err
	}

	app.LogSuccess("Shim '%s' updated: %s = %s", name, key, val)
	return nil
}

func init() {
	rootCmd.AddCommand(shimCmd)
}
