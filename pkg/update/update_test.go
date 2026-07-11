package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareCurrentRollbackMovesCurrentAside(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current")
	rollback := current + ".rollback"
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "marker"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := prepareCurrentRollback(current, rollback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatalf("current still exists: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(rollback, "marker")); err != nil || string(data) != "old" {
		t.Fatalf("rollback content = %q, %v", data, err)
	}
}
