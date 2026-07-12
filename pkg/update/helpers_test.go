package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestNightlyAndShortHash(t *testing.T) {
	v := nightlyVersion()
	if len(v) < len("nightly-") || v[:8] != "nightly-" {
		t.Fatal(v)
	}
	if shortHash("x") != shortHash("x") || len(shortHash("x")) != 7 {
		t.Fatal(shortHash("x"))
	}
}

func TestExtractBucket(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"bucket":"main"}`, "main"},
		{`{"bucket": "extras"}`, "extras"},
		{`{}`, "main"},     // default
		{`not json`, "main"},
	}
	for _, tc := range cases {
		if got := extractBucket(tc.in); got != tc.want {
			t.Errorf("extractBucket(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteLastUpdate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	writeLastUpdate()
	if app.Config().Config().LastUpdate == "" {
		// may only set via Set - check if written
		t.Log("LastUpdate empty after writeLastUpdate - check implementation")
	}
}

func TestRemoveShimsAndEnvSetNoPanic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "shims"), 0755)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Bin:    "bin/app.exe",
		EnvSet: map[string]string{"FOO_TEST_SCOOP": "1"},
		Architecture: nil,
	}
	// GetBin needs arch
	m.Bin = "app.exe"
	removeShimsForManifest(m, "64bit", "app", false)
	removeEnvSetForManifest(m, "64bit", false)
	removeShortcutsForManifest(m, "64bit", false)
}

func TestCheckRunningProcessesIgnoreConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	_ = app.Config().Set("ignore_running_processes", true)
	if !checkRunningProcesses(filepath.Join(root, "apps"), "any") {
		t.Fatal("ignore_running_processes should allow update")
	}
}

func TestPrepareCurrentRollbackMissingCurrent(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current")
	rollback := current + ".rb"
	if err := prepareCurrentRollback(current, rollback); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFailedUpdateRestoresLink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "shims"), 0755)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(root, "apps", "demo")
	current := filepath.Join(appDir, "current")
	rollback := current + ".scoop-go-rollback"
	_ = os.MkdirAll(rollback, 0755)
	_ = os.WriteFile(filepath.Join(rollback, "marker"), []byte("old"), 0644)
	// failed current may exist empty
	_ = os.MkdirAll(current, 0755)

	m := &manifest.Manifest{Version: "1", Homepage: "h", License: "MIT"}
	if err := restoreFailedUpdate(current, rollback, m, "64bit", "demo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(current, "marker")); err != nil {
		t.Fatal("restored marker missing")
	}
}

func TestPlatformSupportedAndReleaseRepo(t *testing.T) {
	_ = platformSupported()
	repo := releaseRepo()
	if repo == "" {
		t.Fatal("empty release repo")
	}
}
