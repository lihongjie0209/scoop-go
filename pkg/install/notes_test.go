package install

import "testing"

func TestSubstituteNotes(t *testing.T) {
	notes := []string{
		"Config is in $dir\\settings.json",
		"Persist lives in $persist_dir",
		"Version $version global=$global app=$app",
		"No vars here",
	}
	vars := NoteVars{
		Dir:        `C:\scoop\apps\demo\current`,
		OriginalDir: `C:\scoop\apps\demo\1.0.0`,
		PersistDir: `C:\scoop\persist\demo`,
		Version:    "1.0.0",
		App:        "demo",
		Global:     false,
	}
	got := SubstituteNotes(notes, vars)
	if got[0] != `Config is in C:\scoop\apps\demo\current\settings.json` {
		t.Fatalf("dir: %q", got[0])
	}
	if got[1] != `Persist lives in C:\scoop\persist\demo` {
		t.Fatalf("persist: %q", got[1])
	}
	if got[2] != "Version 1.0.0 global=False app=demo" {
		t.Fatalf("multi: %q", got[2])
	}
	if got[3] != "No vars here" {
		t.Fatalf("plain: %q", got[3])
	}
}

func TestSubstituteNotesOriginalDir(t *testing.T) {
	got := SubstituteNotes([]string{"$original_dir"}, NoteVars{
		Dir:         "D",
		OriginalDir: "O",
	})
	if got[0] != "O" {
		t.Fatalf("got %q", got[0])
	}
}
