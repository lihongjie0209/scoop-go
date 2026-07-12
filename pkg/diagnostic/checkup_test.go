package diagnostic

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func setupDiag(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	for _, d := range []string{"apps", "buckets", "cache", "shims", "persist"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCheckGit(t *testing.T) {
	c := checkGit()
	if c.Name == "" {
		t.Fatal("empty name")
	}
	// Result depends on environment; just ensure structure
	if c.Passed {
		if c.Message == "" {
			t.Fatal("passed without message")
		}
	} else if c.Fix == "" {
		t.Fatal("failed without fix")
	}
}

func TestCheckMainBucket(t *testing.T) {
	root := setupDiag(t)
	c := checkMainBucket()
	if c.Passed {
		t.Fatal("main bucket should be missing in empty scoop")
	}
	if c.Fix == "" {
		t.Fatal("expected fix hint")
	}
	// add main bucket dir
	if err := os.MkdirAll(filepath.Join(root, "buckets", "main"), 0755); err != nil {
		t.Fatal(err)
	}
	c = checkMainBucket()
	if !c.Passed {
		t.Fatalf("expected main present: %+v", c)
	}
}

func TestCheckHelperTools(t *testing.T) {
	c := checkHelperTools()
	if c.Name != "Helper tools" {
		t.Fatalf("name = %s", c.Name)
	}
	// Either all present or lists missing packages
	if !c.Passed && c.Fix == "" {
		t.Fatal("missing tools need fix string")
	}
}

func TestCheckLongPathsAndDeveloperMode(t *testing.T) {
	// Should not panic on any platform
	c1 := checkLongPaths()
	c2 := checkDeveloperMode()
	if c1.Name == "" || c2.Name == "" {
		t.Fatal("empty check names")
	}
	if runtime.GOOS != "windows" {
		if !c1.Passed || !c2.Passed {
			t.Fatalf("non-windows should pass: %+v %+v", c1, c2)
		}
	}
}

func TestCheckWindowsDefenderAndNtfs(t *testing.T) {
	setupDiag(t)
	c1 := checkWindowsDefender()
	c2 := checkNtfsVolume()
	if c1.Name == "" || c2.Name == "" {
		t.Fatal("empty names")
	}
	if runtime.GOOS != "windows" {
		if !c1.Passed || !c2.Passed {
			t.Fatalf("non-windows N/A should pass: %+v %+v", c1, c2)
		}
	}
}

func TestRunAll(t *testing.T) {
	setupDiag(t)
	checks := RunAll()
	if len(checks) < 5 {
		t.Fatalf("expected multiple checks, got %d", len(checks))
	}
	names := map[string]bool{}
	for _, c := range checks {
		if c.Name == "" {
			t.Fatal("empty check name")
		}
		names[c.Name] = true
	}
	for _, want := range []string{"Git availability", "Main bucket", "Helper tools"} {
		if !names[want] {
			t.Fatalf("missing check %q in %v", want, names)
		}
	}
}

func TestEnsureCheckupDir(t *testing.T) {
	setupDiag(t)
	if err := EnsureCheckupDir(); err != nil {
		t.Fatal(err)
	}
}
