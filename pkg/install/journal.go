package install

import (
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/env"
	"github.com/scoopinstaller/scoop-go/pkg/shim"
	"github.com/scoopinstaller/scoop-go/pkg/shortcut"
)

// IntegrationJournal records system integrations created during install so
// failures can roll back the exact side effects (especially absolute PATH entries).
type IntegrationJournal struct {
	Shims         []string
	Shortcuts     []string
	EnvVars       []string
	PathEntries   []string
	CurrentLinked bool
	seenShim      map[string]bool
	seenSC        map[string]bool
	seenEnv       map[string]bool
	seenPath      map[string]bool
}

// NewIntegrationJournal creates an empty journal.
func NewIntegrationJournal() *IntegrationJournal {
	return &IntegrationJournal{
		seenShim: map[string]bool{},
		seenSC:   map[string]bool{},
		seenEnv:  map[string]bool{},
		seenPath: map[string]bool{},
	}
}

func (j *IntegrationJournal) RecordShim(name string) {
	if j == nil || name == "" || j.seenShim[name] {
		return
	}
	j.seenShim[name] = true
	j.Shims = append(j.Shims, name)
}

func (j *IntegrationJournal) RecordShortcut(name string) {
	if j == nil || name == "" || j.seenSC[name] {
		return
	}
	j.seenSC[name] = true
	j.Shortcuts = append(j.Shortcuts, name)
}

func (j *IntegrationJournal) RecordEnv(name string) {
	if j == nil || name == "" || j.seenEnv[name] {
		return
	}
	j.seenEnv[name] = true
	j.EnvVars = append(j.EnvVars, name)
}

func (j *IntegrationJournal) RecordPath(absPath string) {
	if j == nil || absPath == "" || j.seenPath[absPath] {
		return
	}
	j.seenPath[absPath] = true
	j.PathEntries = append(j.PathEntries, absPath)
}

func (j *IntegrationJournal) MarkCurrentLinked() {
	if j != nil {
		j.CurrentLinked = true
	}
}

// Rollback undoes recorded integrations. versionDir is the app version path
// (used to locate current junction). global selects shim/env scope.
func (j *IntegrationJournal) Rollback(versionDir string, global bool, appName string) {
	if j == nil {
		return
	}
	if j.CurrentLinked && versionDir != "" {
		currentDir := filepath.Join(filepath.Dir(versionDir), "current")
		_ = os.RemoveAll(currentDir)
	}
	if app.Dirs() == nil {
		return
	}
	shimDir := app.ShimDir(global)
	for _, name := range j.Shims {
		_ = shim.Remove(name, shimDir, appName)
	}
	if len(j.Shortcuts) > 0 {
		var pairs [][]string
		for _, n := range j.Shortcuts {
			pairs = append(pairs, []string{"", n})
		}
		_ = shortcut.RemoveAll(pairs, global)
	}
	for _, name := range j.EnvVars {
		_ = env.SetEnv(name, "", global)
	}
	if len(j.PathEntries) > 0 {
		_ = env.RemovePath(j.PathEntries, app.Dirs().PathEnvVar, global)
	}
}
