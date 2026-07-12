package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/spf13/cobra"
)

var shimFlags struct {
	global bool
}

var shimCmd = &cobra.Command{
	Use:   "shim add|rm|list|info|alter [args]",
	Short: "Manipulate Scoop shims",
	Long: `Available subcommands: add, rm, list, info, alter.

  add <name> <path> [args]   Add a shim
  rm <name> [name...]        Remove shim(s)
  list [pattern...]          List shims (optional regex patterns)
  info <name>                Show shim info
  alter <name>               Cycle alternative shim targets (*.exe.<app>)
  alter <name> <key> <val>   Modify a .shim property (Go extension)

Options:
  -g, --global               Use global shim directory`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		if shimFlags.global {
			if err := checkAdminRights(); err != nil {
				return fmt.Errorf("you need admin rights to manipulate global shims")
			}
		}

		subcmd := args[0]
		switch subcmd {
		case "add":
			return shimAdd(args[1:])
		case "rm":
			return shimRemove(args[1:])
		case "list":
			return shimList(args[1:])
		case "info":
			return shimInfo(args[1:])
		case "alter":
			return shimAlter(args[1:])
		default:
			return fmt.Errorf("scoop shim: unknown subcommand '%s'", subcmd)
		}
	},
}

func shimDir() string {
	return app.ShimDir(shimFlags.global)
}

func shimAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: scoop shim add <name> <path> [args]")
	}
	name := args[0]
	path := args[1]
	extraArgs := ""
	if len(args) > 2 {
		extraArgs = strings.Join(args[2:], " ")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", path)
	}

	return shim.Create(&shim.Config{
		TargetPath: path,
		Name:       name,
		Args:       extraArgs,
		ShimDir:    shimDir(),
		Global:     shimFlags.global,
	})
}

func shimRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop shim rm <name> [name...]")
	}
	var firstErr error
	for _, name := range args {
		if err := shim.Remove(name, shimDir(), ""); err != nil {
			app.LogError("removing '%s': %v", name, err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			app.LogSuccess("Removed shim '%s'", name)
		}
	}
	return firstErr
}

func shimList(patterns []string) error {
	dir := shimDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		app.LogInfo("No shims found.")
		return nil
	}

	var res []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(p))
		}
		res = append(res, re)
	}

	shims := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		base := strings.TrimSuffix(e.Name(), ext)
		// Skip alternate targets like name.exe.appname
		if strings.Contains(base, ".") {
			continue
		}
		if len(res) > 0 {
			ok := false
			for _, re := range res {
				if re.MatchString(base) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		shims[base] = true
	}

	if len(shims) == 0 {
		app.LogInfo("No shims found.")
		return nil
	}

	for name := range shims {
		target := shim.ResolveShimTarget(filepath.Join(dir, name+".shim"))
		if target == "" {
			target = shim.ResolveWrapperTarget(filepath.Join(dir, name+".cmd"))
		}
		if target == "" {
			target = "(unknown)"
		}
		scope := "local"
		if shimFlags.global {
			scope = "global"
		}
		fmt.Printf("%-20s [%s] %s\n", name, scope, target)
	}
	return nil
}

func shimInfo(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop shim info <name>")
	}
	name := args[0]
	dir := shimDir()

	shimPath := filepath.Join(dir, name+".shim")
	target := shim.ResolveShimTarget(shimPath)
	if target == "" {
		target = shim.ResolveWrapperTarget(filepath.Join(dir, name+".cmd"))
	}
	if target == "" {
		return fmt.Errorf("shim '%s' not found", name)
	}

	fmt.Printf("Name:   %s\n", name)
	fmt.Printf("Target: %s\n", target)
	fmt.Printf("Path:   %s\n", filepath.Join(dir, name))
	fmt.Printf("Global: %v\n", shimFlags.global)

	// List alternate targets (name.exe.<app>)
	alts, _ := filepath.Glob(filepath.Join(dir, name+".exe.*"))
	if len(alts) > 0 {
		var names []string
		for _, a := range alts {
			names = append(names, strings.TrimPrefix(filepath.Ext(a), "."))
			// For multi-dot: name.exe.app -> take last component after .exe.
			base := filepath.Base(a)
			if i := strings.Index(strings.ToLower(base), ".exe."); i >= 0 {
				names[len(names)-1] = base[i+5:]
			}
		}
		fmt.Printf("Alts:   %s\n", strings.Join(names, ", "))
	}
	return nil
}

func shimAlter(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: scoop shim alter <name>  OR  scoop shim alter <name> <key> <value>")
	}
	name := args[0]
	dir := shimDir()

	// Key/value edit mode (Go extension)
	if len(args) >= 3 {
		return shimAlterProperty(name, args[1], args[2], dir)
	}

	// PowerShell-compatible: cycle among alternate targets name.exe / name.exe.<app>
	primary := filepath.Join(dir, name+".exe")
	if _, err := os.Stat(primary); err != nil {
		// Fall back to editing path if no multi-target layout
		return fmt.Errorf("shim '%s' has no alternating targets (no %s)", name, primary)
	}

	alts, err := filepath.Glob(filepath.Join(dir, name+".exe.*"))
	if err != nil || len(alts) == 0 {
		return fmt.Errorf("shim '%s' has no alternative targets", name)
	}

	// Current active is name.exe; rotate: move current to .exe.<cur>, promote next alt to .exe
	// Discover current source app from .shim path if possible
	currentTarget := shim.ResolveShimTarget(filepath.Join(dir, name+".shim"))
	currentApp := "current"
	if currentTarget != "" {
		// apps/<app>/...
		parts := strings.Split(filepath.ToSlash(currentTarget), "/")
		for i, p := range parts {
			if p == "apps" && i+1 < len(parts) {
				currentApp = parts[i+1]
				break
			}
		}
	}

	// Pick first alternative to promote
	next := alts[0]
	nextApp := filepath.Base(next)
	if i := strings.Index(strings.ToLower(nextApp), ".exe."); i >= 0 {
		nextApp = nextApp[i+5:]
	}

	// Park current primary under .exe.<currentApp>
	parked := filepath.Join(dir, name+".exe."+currentApp)
	if err := os.Rename(primary, parked); err != nil {
		return fmt.Errorf("parking current target: %w", err)
	}
	if err := os.Rename(next, primary); err != nil {
		_ = os.Rename(parked, primary)
		return fmt.Errorf("promoting alternative: %w", err)
	}

	// Refresh .shim path line if present
	shimPath := filepath.Join(dir, name+".shim")
	if data, err := os.ReadFile(shimPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "path") {
				// Keep same path key pointing at primary exe
				key := "path"
				if strings.Contains(line, " = ") {
					key = strings.TrimSpace(strings.SplitN(line, " = ", 2)[0])
				}
				lines[i] = fmt.Sprintf("%s = %s", key, primary)
			}
		}
		_ = os.WriteFile(shimPath, []byte(strings.Join(lines, "\n")), 0644)
	}

	app.LogSuccess("Shim '%s' now uses alternative '%s'", name, nextApp)
	return nil
}

func shimAlterProperty(name, key, val, dir string) error {
	shimPath := filepath.Join(dir, name+".shim")
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
	shimCmd.Flags().BoolVarP(&shimFlags.global, "global", "g", false, "Manipulate global shim(s)")
}
