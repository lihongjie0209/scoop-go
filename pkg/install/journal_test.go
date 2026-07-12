package install

import (
	"path/filepath"
	"testing"
)

func TestIntegrationJournalRecordsAndLists(t *testing.T) {
	j := NewIntegrationJournal()
	j.RecordShim("git")
	j.RecordShim("git") // dedupe
	j.RecordShortcut("Git GUI")
	j.RecordEnv("GIT_INSTALL_ROOT")
	j.RecordPath(filepath.Join("C:", "scoop", "apps", "git", "current", "cmd"))
	j.MarkCurrentLinked()

	if len(j.Shims) != 1 || j.Shims[0] != "git" {
		t.Fatalf("shims = %v", j.Shims)
	}
	if len(j.Shortcuts) != 1 {
		t.Fatal(j.Shortcuts)
	}
	if len(j.EnvVars) != 1 || j.EnvVars[0] != "GIT_INSTALL_ROOT" {
		t.Fatal(j.EnvVars)
	}
	if len(j.PathEntries) != 1 {
		t.Fatal(j.PathEntries)
	}
	if !j.CurrentLinked {
		t.Fatal("expected current linked")
	}
}

func TestIntegrationJournalEmptyRollbackNoop(t *testing.T) {
	j := NewIntegrationJournal()
	// Should not panic when dirs are uninitialized
	j.Rollback("", false, "")
}
