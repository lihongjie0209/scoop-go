package bucket

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func TestDirAndManifestDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "buckets"), 0755)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	// without nested bucket/ folder
	if err := os.MkdirAll(filepath.Join(root, "buckets", "main"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := Dir("main"); got != filepath.Join(root, "buckets", "main") {
		t.Fatalf("Dir = %s", got)
	}
	if got := ManifestDir("main"); got != filepath.Join(root, "buckets", "main") {
		t.Fatalf("ManifestDir flat = %s", got)
	}

	// with nested bucket/
	if err := os.MkdirAll(filepath.Join(root, "buckets", "extras", "bucket"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := ManifestDir("extras"); got != filepath.Join(root, "buckets", "extras", "bucket") {
		t.Fatalf("ManifestDir nested = %s", got)
	}
}

func TestIsLocalAndAppManifestPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "buckets", "main", "bucket"), 0755)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	if IsLocal("missing") {
		t.Fatal("expected not local")
	}
	if !IsLocal("main") {
		t.Fatal("expected main local")
	}
	path := filepath.Join(root, "buckets", "main", "bucket", "fd.json")
	if err := os.WriteFile(path, []byte(`{"version":"1","homepage":"h","license":"MIT","url":"http://x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	b, p := AppManifestPath("fd")
	if b != "main" || p == "" {
		t.Fatalf("AppManifestPath = %q %q", b, p)
	}
	if !AppExistsInAnyBucket("fd") {
		t.Fatal("expected exists")
	}
	if AppExistsInAnyBucket("nope") {
		t.Fatal("expected missing")
	}
}

func TestKnownAndRepo(t *testing.T) {
	names := Known()
	if len(names) == 0 {
		t.Fatal("expected known buckets")
	}
	if repo, ok := Repo("main"); !ok || repo == "" {
		t.Fatal("main repo missing")
	}
	if _, ok := Repo("this-bucket-does-not-exist-xyz"); ok {
		t.Fatal("unknown should be false")
	}
}

func TestListLocal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "buckets", "main"), 0755)
	_ = os.MkdirAll(filepath.Join(root, "buckets", "extras"), 0755)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	list := ListLocal()
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
}
