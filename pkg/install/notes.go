package install

import (
	"fmt"
	"strings"
)

// NoteVars holds Scoop-compatible variables for notes expansion.
type NoteVars struct {
	Dir         string
	OriginalDir string
	PersistDir  string
	Version     string
	App         string
	Global      bool
}

// SubstituteNotes expands $dir, $original_dir, $persist_dir, $version, $app, $global.
// Mirrors show_notes substitutions from PowerShell Scoop.
func SubstituteNotes(notes []string, vars NoteVars) []string {
	global := "False"
	if vars.Global {
		global = "True"
	}
	replacer := strings.NewReplacer(
		"$original_dir", vars.OriginalDir,
		"$persist_dir", vars.PersistDir,
		"$version", vars.Version,
		"$global", global,
		"$app", vars.App,
		"$dir", vars.Dir,
	)
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = replacer.Replace(n)
	}
	return out
}

// FormatGlobalBool is exported for tests of PowerShell-style casing if needed.
func FormatGlobalBool(g bool) string {
	return fmt.Sprintf("%v", map[bool]string{true: "True", false: "False"}[g])
}
