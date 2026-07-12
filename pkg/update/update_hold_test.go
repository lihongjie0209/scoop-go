package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func TestUpdateAppSkipsHeldUnlessForced(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	// Installed held app
	current := filepath.Join(root, "apps", "heldapp", "current")
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "manifest.json"), []byte(`{
		"version":"1.0.0","homepage":"https://ex","license":"MIT","url":"https://ex/a.zip"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	info := InstallInfo{Architecture: "64bit", Bucket: "main", Hold: true}
	raw, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(current, "install.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}

	// Bucket with newer version — should still skip because held
	bucketDir := filepath.Join(root, "buckets", "main", "bucket")
	if err := os.MkdirAll(bucketDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucketDir, "heldapp.json"), []byte(`{
		"version":"2.0.0","homepage":"https://ex","license":"MIT","url":"https://ex/a2.zip"
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Not forced: skip hold
	if err := UpdateApp(context.Background(), "heldapp", false, false, true, true, true, true); err != nil {
		t.Fatalf("held skip should not error: %v", err)
	}
	// Version dir for 2.0.0 should not exist
	if _, err := os.Stat(filepath.Join(root, "apps", "heldapp", "2.0.0")); !os.IsNotExist(err) {
		t.Fatal("held app should not have been updated")
	}
}

func TestPrepareAndRestoreRollback(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current")
	rollback := current + ".scoop-go-rollback"
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "marker"), []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := prepareCurrentRollback(current, rollback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatal("current should be moved")
	}
	// Simulate failed install leaving nothing at current, then restore via rename only path
	if err := os.Rename(rollback, current); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(current, "marker"))
	if err != nil || string(data) != "v1" {
		t.Fatalf("restored = %q %v", data, err)
	}
}
