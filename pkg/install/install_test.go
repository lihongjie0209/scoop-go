package install

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/version"
)

func TestNormalizeExtractDir(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{nil, ""},
		{"subdir", "subdir"},
		{[]any{"subdir"}, "subdir"},
		{[]any{}, ""},
	}

	for _, tt := range tests {
		got := normalizeExtractDir(tt.input)
		if got != tt.want {
			t.Errorf("normalizeExtractDir(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNightlyVersion(t *testing.T) {
	v := nightlyVersion()
	if len(v) < 10 {
		t.Errorf("nightly version too short: %s", v)
	}
	if !strings.HasPrefix(v, "nightly-") {
		t.Errorf("expected nightly- prefix, got %s", v)
	}
}

func TestShortHash(t *testing.T) {
	h := shortHash("https://example.com/file.zip")
	if len(h) != 7 {
		t.Errorf("expected 7 char hash, got %d: %s", len(h), h)
	}
}

func TestEnsureDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "scoop-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	testDir := filepath.Join(dir, "a", "b", "c")
	if err := EnsureDir(testDir); err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}
	if _, err := os.Stat(testDir); os.IsNotExist(err) {
		t.Error("expected directory to exist")
	}
}

func TestGetArchitecture(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "64bit"},
		{"64bit", "64bit"},
		{"32bit", "32bit"},
	}

	for _, tt := range tests {
		got := GetArchitecture(tt.input)
		if got != tt.want {
			t.Errorf("GetArchitecture(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestManifestIsNightly(t *testing.T) {
	if !version.IsNightly("nightly") {
		t.Error("expected IsNightly('nightly') to be true")
	}
	if version.IsNightly("1.0.0") {
		t.Error("expected IsNightly('1.0.0') to be false")
	}
}

func TestSaveInstallInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "scoop-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	engine := &Engine{
		AppName: "testapp",
		Version: "1.0.0",
		Arch:    "64bit",
		Bucket:  "main",
	}

	if err := engine.saveInstallInfo(dir); err != nil {
		t.Fatal(err)
	}

	infoPath := filepath.Join(dir, "install.json")
	if _, err := os.Stat(infoPath); os.IsNotExist(err) {
		t.Fatal("install.json was not created")
	}

	data, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, `"architecture": "64bit"`) {
		t.Errorf("expected architecture in install.json, got: %s", content)
	}
	if !strings.Contains(content, `"bucket": "main"`) {
		t.Errorf("expected bucket in install.json, got: %s", content)
	}
}

func TestSaveInstallInfoReturnsWriteError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{AppName: "testapp", Version: "1.0.0", Arch: "64bit"}
	if err := engine.saveInstallInfo(filePath); err == nil {
		t.Fatal("expected install metadata write error")
	}
}

func TestInstallFailureRemovesNewVersionDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Version: "1.0.0",
		URL:     manifest.FlexibleStrings{"http://127.0.0.1:1/unreachable.zip"},
	}
	e := &Engine{
		AppName:   "failed-app",
		Manifest:  m,
		Version:   m.Version,
		Arch:      "64bit",
		UseCache:  false,
		CheckHash: true,
	}
	if err := e.Install(context.Background()); err == nil {
		t.Fatal("expected install to fail")
	}
	versionDir := app.AppVersionDir(e.AppName, e.Version, false)
	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Fatalf("incomplete version directory still exists: %v", err)
	}
}

func TestGenerateVersionManifestDownloadsAndPinsHash(t *testing.T) {
	content := []byte("target-version-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool-1.2.3.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()

	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	source := manifest.MustParse([]byte(fmt.Sprintf(`{
		"version":"2.0.0",
		"homepage":"https://example.test",
		"license":"MIT",
		"url":%q,
		"hash":"current-hash",
		"autoupdate":{"url":%q}
	}`, server.URL+"/tool-2.0.0.zip", server.URL+"/tool-$version.zip")))

	generated, err := GenerateVersionManifest(context.Background(), "tool", source, "1.2.3", "64bit", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := generated.GetURL("64bit"); len(got) != 1 || got[0] != server.URL+"/tool-1.2.3.zip" {
		t.Fatalf("generated URL = %#v", got)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(content))
	if got := generated.GetHash("64bit"); len(got) != 1 || got[0] != wantHash {
		t.Fatalf("generated hash = %#v, want %s", got, wantHash)
	}
	cachePath := filepath.Join(app.Dirs().CacheDir, fmt.Sprintf("tool#1.2.3#%s", shortHash(server.URL+"/tool-1.2.3.zip")))
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("generated download was not cached: %v", err)
	}
}

func TestPersistDataCreatesLinkForInitiallyMissingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	versionDir := app.AppVersionDir("persist-app", "1.0.0", false)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Persist: "data"}
	if err := PersistData("persist-app", false, m, versionDir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(versionDir, "data")); err != nil || !info.IsDir() {
		t.Fatalf("persist source link missing: %v", err)
	}
	if info, err := os.Stat(filepath.Join(app.PersistDir("persist-app", false), "data")); err != nil || !info.IsDir() {
		t.Fatalf("persist target missing: %v", err)
	}
}

func TestCreateJunction(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	sourceDir := filepath.Join(dir, "current")

	// Create target directory with a test file
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(targetDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create junction
	if err := createJunction(sourceDir, targetDir); err != nil {
		t.Fatalf("createJunction failed: %v", err)
	}

	// Verify the junction exists
	if _, err := os.Lstat(sourceDir); err != nil {
		t.Fatalf("junction does not exist: %v", err)
	}

	// Verify we can read files through the junction
	data, err := os.ReadFile(filepath.Join(sourceDir, "test.txt"))
	if err != nil {
		t.Fatalf("cannot read through junction: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("wrong content through junction: got %q, want %q", string(data), "hello")
	}

	// Redirection: writing through the junction should write to target
	testFile2 := filepath.Join(sourceDir, "written.txt")
	if err := os.WriteFile(testFile2, []byte("world"), 0644); err != nil {
		t.Fatalf("cannot write through junction: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(targetDir, "written.txt"))
	if err != nil {
		t.Fatalf("cannot verify file written through junction: %v", err)
	}
	if string(data2) != "world" {
		t.Errorf("wrong content from junction write: got %q, want %q", string(data2), "world")
	}
}

func TestCreateJunctionTargetNotExist(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "current")
	missingTarget := filepath.Join(dir, "nonexistent")

	err := createJunction(sourceDir, missingTarget)
	if err == nil {
		t.Error("expected error for non-existent target, got nil")
	}
}

func TestCreateJunctionOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	sourceDir := filepath.Join(dir, "current")

	// Create target with a file
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "test.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create source as a regular directory first (simulates leftover from failed install)
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Now create the junction, which should remove the old directory and replace it
	if err := createJunction(sourceDir, targetDir); err != nil {
		t.Fatalf("createJunction failed to overwrite existing: %v", err)
	}

	// Verify the junction works
	data, err := os.ReadFile(filepath.Join(sourceDir, "test.txt"))
	if err != nil {
		t.Fatalf("cannot read through recreated junction: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("wrong content: got %q, want %q", string(data), "data")
	}
}

func TestLinkCurrent(t *testing.T) {
	dir := t.TempDir()

	// Simulate the Scoop apps directory structure
	appDir := filepath.Join(dir, "apps", "myapp")
	versionDir := filepath.Join(appDir, "1.0.0")
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{
		AppName: "myapp",
		Version: "1.0.0",
	}

	currentDir, err := engine.linkCurrent(versionDir)
	if err != nil {
		t.Fatalf("linkCurrent failed: %v", err)
	}

	// Should return the current directory path
	expected := filepath.Join(appDir, "current")
	if currentDir != expected {
		t.Errorf("linkCurrent returned %q, want %q", currentDir, expected)
	}

	// Verify the junction exists
	if _, err := os.Lstat(expected); err != nil {
		t.Fatalf("current link does not exist: %v", err)
	}

	// Verify it points to the version directory
	// On non-Windows: Readlink works; on Windows junctions, os.Readlink may vary
	resolved, readlinkErr := os.Readlink(expected)
	if readlinkErr == nil {
		if !strings.HasSuffix(resolved, "1.0.0") {
			t.Errorf("readlink resolved to %q, expected suffix %q", resolved, "1.0.0")
		}
	}
}

func TestJunctionReadOnlyAttribute(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	sourceDir := filepath.Join(dir, "current")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := createJunction(sourceDir, targetDir); err != nil {
		t.Fatal(err)
	}

	// Apply +R /L — should succeed on all platforms
	if err := setJunctionReadOnly(sourceDir); err != nil {
		t.Fatalf("setJunctionReadOnly failed: %v", err)
	}
}
