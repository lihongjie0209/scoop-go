package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func TestSearchAndToFTS5(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	for _, d := range []string{"apps", "buckets", "cache", "shims", "persist"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0755)
	}
	_ = Close()
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })

	mainBucket := filepath.Join(root, "buckets", "main", "bucket")
	_ = os.MkdirAll(mainBucket, 0755)
	_ = os.WriteFile(filepath.Join(mainBucket, "ripgrep.json"), []byte(`{
		"version":"14.0.0",
		"homepage":"https://ex",
		"license":"MIT",
		"description":"line oriented search",
		"url":"https://ex/rg.zip",
		"bin":"rg.exe"
	}`), 0644)

	if err := RebuildAll(); err != nil {
		t.Fatal(err)
	}

	results, err := Search("rip")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search hits for rip")
	}
	found := false
	for _, r := range results {
		if r.Name == "ripgrep" {
			found = true
			if r.Binary == "" {
				t.Fatal("binary should be indexed")
			}
		}
	}
	if !found {
		t.Fatalf("results = %+v", results)
	}

	// binary search
	results, err = Search("rg")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected binary match for rg")
	}

	q, ok := toFTS5Query("hello world")
	if !ok || q == "" {
		t.Fatalf("fts query = %q ok=%v", q, ok)
	}
	if _, ok := toFTS5Query("%"); ok {
		t.Fatal("% should not convert to fts")
	}
}

func TestIsEnabled(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "apps"), 0755)
	_ = Close()
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	// default false
	if IsEnabled() {
		t.Fatal("expected disabled by default")
	}
}
