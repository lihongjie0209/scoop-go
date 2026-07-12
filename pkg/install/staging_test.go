package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagingDirFor(t *testing.T) {
	got := StagingDirFor(`C:\scoop\apps\git\2.40.0`)
	want := `C:\scoop\apps\git\2.40.0.scoop-staging`
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPrepareStagingCreatesEmptyDir(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "1.0.0")
	stage, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if stage != StagingDirFor(versionDir) {
		t.Fatalf("stage path = %q", stage)
	}
	info, err := os.Stat(stage)
	if err != nil || !info.IsDir() {
		t.Fatalf("stage dir missing: %v", err)
	}
	// Re-prepare must wipe previous content
	marker := filepath.Join(stage, "old.txt")
	if err := os.WriteFile(marker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	stage2, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage2, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("expected old staging content removed")
	}
}

func TestPromoteStagingRenamesIntoPlace(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "1.0.0")
	stage, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "payload.bin"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := PromoteStaging(stage, versionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("staging dir should be gone after promote")
	}
	data, err := os.ReadFile(filepath.Join(versionDir, "payload.bin"))
	if err != nil || string(data) != "data" {
		t.Fatalf("promoted content = %q %v", data, err)
	}
}

func TestPromoteStagingFailsIfTargetOccupied(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "1.0.0")
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "existing"), []byte("e"), 0644); err != nil {
		t.Fatal(err)
	}
	stage, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "new"), []byte("n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Occupied target: promote should replace only when force=true
	if err := PromoteStaging(stage, versionDir); err == nil {
		t.Fatal("expected error when target exists without force")
	}
	if err := PromoteStagingForce(stage, versionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "existing")); !os.IsNotExist(err) {
		t.Fatal("force promote should replace old version dir contents")
	}
}

func TestDiscardStaging(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "1.0.0")
	stage, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := DiscardStaging(stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("discard should remove stage")
	}
	// discard missing is ok
	if err := DiscardStaging(stage); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFailureBeforePromoteLeavesNoVersionDir(t *testing.T) {
	// Behavioral contract documented by helpers: if promote never runs,
	// only staging exists and DiscardStaging cleans it — final version dir absent.
	root := t.TempDir()
	versionDir := filepath.Join(root, "apps", "x", "9.0.0")
	stage, err := PrepareStaging(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(stage, "partial"), []byte("x"), 0644)
	// simulate failure path
	_ = DiscardStaging(stage)
	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Fatal("version dir must not exist when promote never happened")
	}
}
