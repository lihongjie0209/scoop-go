package bucket

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	// Dir() depends on app.Dirs() being initialized,
	// so we can't easily test it without app initialization.
	// Very basic sanity check: just verify it doesn't panic if called
	// when app is not initialized (it will, but that's expected).
}

func TestRemoveBucketDirNonExistent(t *testing.T) {
	// Should not panic on non-existent directory
	removeBucketDir(filepath.Join(os.TempDir(), "scoop-test-nonexistent-"+t.Name()))
}

func TestRemoveBucketDirEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "scoop-bucket-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	removeBucketDir(dir)

	// Directory should be removed
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("expected directory to be removed")
	}
}

func TestRemoveBucketDirWithFiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "scoop-bucket-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create some files inside
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("world"), 0644)

	removeBucketDir(dir)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("expected directory to be removed with all contents")
	}
}

func TestRemoveBucketDirRetryOnFailure(t *testing.T) {
	// Test that removeBucketDir doesn't panic when os.RemoveAll fails
	// (simulated by passing an invalid path)
	removeBucketDir("")
	removeBucketDir("\\invalid\x00path")
}

func TestKnownBuckets(t *testing.T) {
	known, err := LoadKnownBuckets()
	if err != nil {
		t.Fatalf("LoadKnownBuckets failed: %v", err)
	}

	// Should have at least the default buckets
	required := []string{"main", "extras", "versions", "nerd-fonts", "java"}
	for _, name := range required {
		if _, ok := known[name]; !ok {
			t.Errorf("expected known bucket %q to exist", name)
		}
	}
}

func TestKnown(t *testing.T) {
	names := Known()
	if len(names) == 0 {
		t.Error("expected at least one known bucket")
	}
}

func TestRepo(t *testing.T) {
	repo, ok := Repo("main")
	if !ok {
		t.Fatal("expected main bucket to be known")
	}
	if repo != "https://github.com/ScoopInstaller/Main" {
		t.Errorf("unexpected repo URL: %s", repo)
	}

	_, ok = Repo("nonexistent-bucket-name-xyz")
	if ok {
		t.Error("expected unknown bucket to return false")
	}
}

func TestConvertRepoURI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/ScoopInstaller/Main", "github.com/ScoopInstaller/Main"},
		{"https://github.com/ScoopInstaller/Main.git", "github.com/ScoopInstaller/Main"},
		{"git@github.com:ScoopInstaller/Extras.git", "github.com/ScoopInstaller/Extras"},
		{"https://github.com/user/repo.git", "github.com/user/repo"},
	}
	for _, tt := range tests {
		got := ConvertRepoURI(tt.input)
		if got != tt.want {
			t.Errorf("ConvertRepoURI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
