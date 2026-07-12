package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestPrefetchAppDownloadsToCache(t *testing.T) {
	content := []byte("new-version-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()

	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	url := server.URL + "/app-2.0.0.zip"
	sum := fmt.Sprintf("%x", sha256.Sum256(content))
	m := manifest.MustParse([]byte(fmt.Sprintf(`{
		"version":"2.0.0",
		"homepage":"https://ex",
		"license":"MIT",
		"url":%q,
		"hash":%q
	}`, url, sum)))

	if err := PrefetchApp(context.Background(), "demo", m, "64bit", true, true); err != nil {
		t.Fatal(err)
	}
	// Cache key style matches install: app#ver#shortHash(url)
	entries, err := os.ReadDir(app.Dirs().CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected cache file after prefetch")
	}
}

func TestPrefetchAppHashMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()

	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	m := manifest.MustParse([]byte(fmt.Sprintf(`{
		"version":"1.0.0",
		"homepage":"https://ex",
		"license":"MIT",
		"url":%q,
		"hash":"0000000000000000000000000000000000000000000000000000000000000000"
	}`, server.URL+"/x.zip")))

	if err := PrefetchApp(context.Background(), "demo", m, "64bit", true, true); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}
