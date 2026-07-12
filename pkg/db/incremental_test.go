package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func setupDBTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	for _, d := range []string{"apps", "buckets", "cache", "shims", "persist"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	_ = Close()
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	// Enable sqlite for IsEnabled checks if needed
	t.Cleanup(func() { _ = Close() })
	return root
}

func TestBucketAndAppFromPath(t *testing.T) {
	cases := []struct {
		path, bucket, app string
		ok                bool
	}{
		{`C:\scoop\buckets\main\bucket\git.json`, "main", "git", true},
		{`/home/u/scoop/buckets/extras/bucket/foo.json`, "extras", "foo", true},
		{`C:\scoop\buckets\main\git.json`, "main", "git", true},
		{`C:\scoop\cache\x.json`, "", "", false},
		{`C:\scoop\buckets\main\bucket\nested\app.json`, "main", "app", true},
	}
	for _, tc := range cases {
		b, a, ok := BucketAndAppFromPath(tc.path)
		if ok != tc.ok || b != tc.bucket || a != tc.app {
			t.Errorf("BucketAndAppFromPath(%q)=(%q,%q,%v) want (%q,%q,%v)",
				tc.path, b, a, ok, tc.bucket, tc.app, tc.ok)
		}
	}
}

func TestApplyChangesUpsertAndRemove(t *testing.T) {
	root := setupDBTest(t)
	mainBucket := filepath.Join(root, "buckets", "main", "bucket")
	if err := os.MkdirAll(mainBucket, 0755); err != nil {
		t.Fatal(err)
	}
	gitJSON := `{
		"version":"1.0.0",
		"homepage":"https://ex",
		"license":"MIT",
		"description":"git tool",
		"url":"https://ex/g.zip",
		"bin":"git.exe"
	}`
	gitPath := filepath.Join(mainBucket, "git.json")
	if err := os.WriteFile(gitPath, []byte(gitJSON), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyChanges(ChangeSet{UpsertPaths: []string{gitPath}}); err != nil {
		t.Fatal(err)
	}

	row, err := GetByName("git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if row.Version != "1.0.0" || row.Binary == "" {
		t.Fatalf("row = %+v", row)
	}

	// Update version via upsert
	gitJSON2 := `{
		"version":"2.0.0",
		"homepage":"https://ex",
		"license":"MIT",
		"description":"git tool",
		"url":"https://ex/g2.zip",
		"bin":"git.exe"
	}`
	if err := os.WriteFile(gitPath, []byte(gitJSON2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyChanges(ChangeSet{UpsertPaths: []string{gitPath}}); err != nil {
		t.Fatal(err)
	}
	row, err = GetByName("git", "main", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if row.Version != "2.0.0" {
		t.Fatalf("version = %s", row.Version)
	}

	// Remove
	if err := ApplyChanges(ChangeSet{Removals: []Removal{{Bucket: "main", Name: "git"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetByName("git", "main", ""); err == nil {
		t.Fatal("expected missing after removal")
	}
}

func TestParseNameStatus(t *testing.T) {
	input := "" +
		"A\tbucket/foo.json\n" +
		"M\tbucket/bar.json\n" +
		"D\tbucket/old.json\n" +
		"R100\tbucket/a.json\tbucket/b.json\n"
	changes := ParseNameStatus(input)
	if len(changes) != 4 {
		t.Fatalf("len=%d %+v", len(changes), changes)
	}
	if changes[0].Status != "A" || changes[0].Path != "bucket/foo.json" {
		t.Fatalf("%+v", changes[0])
	}
	if changes[3].Status != "R" || changes[3].Path != "bucket/b.json" || changes[3].OldPath != "bucket/a.json" {
		t.Fatalf("rename %+v", changes[3])
	}
}

func TestChangeSetFromNameStatusFiltersManifests(t *testing.T) {
	root := setupDBTest(t)
	bucketRoot := filepath.Join(root, "buckets", "main")
	manifestDir := filepath.Join(bucketRoot, "bucket")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create files referenced by "A" and "M"
	if err := os.WriteFile(filepath.Join(manifestDir, "new.json"), []byte(`{
		"version":"1","homepage":"h","license":"MIT","url":"http://x/a"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "mod.json"), []byte(`{
		"version":"1","homepage":"h","license":"MIT","url":"http://x/b"
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	changes := []NameStatusChange{
		{Status: "A", Path: "bucket/new.json"},
		{Status: "M", Path: "bucket/mod.json"},
		{Status: "D", Path: "bucket/gone.json"},
		{Status: "A", Path: "README.md"}, // non-manifest ignored for upsert
		{Status: "R", OldPath: "bucket/old.json", Path: "bucket/renamed.json"},
	}
	// renamed file must exist for upsert
	if err := os.WriteFile(filepath.Join(manifestDir, "renamed.json"), []byte(`{
		"version":"1","homepage":"h","license":"MIT","url":"http://x/c"
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	cs := ChangeSetFromNameStatus(bucketRoot, "main", changes)
	if len(cs.UpsertPaths) != 3 {
		t.Fatalf("upsert = %v", cs.UpsertPaths)
	}
	// D gone + R old
	if len(cs.Removals) != 2 {
		t.Fatalf("removals = %+v", cs.Removals)
	}
}
