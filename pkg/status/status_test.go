package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

func setupStatusScoop(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	for _, d := range []string{"apps", "buckets", "cache", "shims", "persist"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeInstalledApp(t *testing.T, root, name, version, bucket string, hold bool, manifestExtra string) {
	t.Helper()
	verDir := filepath.Join(root, "apps", name, version)
	if err := os.MkdirAll(verDir, 0755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "apps", name, "current")
	// Prefer real directory "current" with files (portable across platforms)
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	// Also keep version dir for fallbacks
	holdJSON := "false"
	if hold {
		holdJSON = "true"
	}
	install := `{"architecture":"64bit","bucket":"` + bucket + `","hold":` + holdJSON + `}`
	if err := os.WriteFile(filepath.Join(current, "install.json"), []byte(install), 0644); err != nil {
		t.Fatal(err)
	}
	man := `{"version":"` + version + `","homepage":"https://ex","license":"MIT","url":"https://ex/a.zip"` + manifestExtra + `}`
	if err := os.WriteFile(filepath.Join(current, "manifest.json"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(verDir, "install.json"), []byte(install), 0644)
	_ = os.WriteFile(filepath.Join(verDir, "manifest.json"), []byte(man), 0644)
}

func writeBucketManifest(t *testing.T, root, bucket, name, version, extra string) {
	t.Helper()
	dir := filepath.Join(root, "buckets", bucket, "bucket")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":"` + version + `","homepage":"https://ex","license":"MIT","url":"https://ex/a.zip"` + extra + `}`
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSingleAppStatusUpToDate(t *testing.T) {
	root := setupStatusScoop(t)
	writeBucketManifest(t, root, "main", "demo", "1.0.0", "")
	writeInstalledApp(t, root, "demo", "1.0.0", "main", false, "")

	s := checkSingleAppStatus("demo", false)
	if !s.Installed || s.Failed || s.Outdated || s.Hold || s.Removed {
		t.Fatalf("%+v", s)
	}
	if s.Version != "1.0.0" || s.LatestVersion != "1.0.0" {
		t.Fatalf("versions %+v", s)
	}
}

func TestCheckSingleAppStatusOutdatedAndHold(t *testing.T) {
	root := setupStatusScoop(t)
	writeBucketManifest(t, root, "main", "demo", "2.0.0", "")
	writeInstalledApp(t, root, "demo", "1.0.0", "main", true, "")

	s := checkSingleAppStatus("demo", false)
	if !s.Outdated {
		t.Fatalf("expected outdated: %+v", s)
	}
	if !s.Hold {
		t.Fatalf("expected hold: %+v", s)
	}
	if s.LatestVersion != "2.0.0" {
		t.Fatalf("latest = %s", s.LatestVersion)
	}
}

func TestCheckSingleAppStatusMissingDeps(t *testing.T) {
	root := setupStatusScoop(t)
	// dep uses bucket/app form — must resolve app name, not bucket
	writeBucketManifest(t, root, "main", "leaf", "1.0.0", `,"depends":["main/helper"]`)
	writeInstalledApp(t, root, "leaf", "1.0.0", "main", false, `,"depends":["main/helper"]`)
	// helper not installed

	s := checkSingleAppStatus("leaf", false)
	if len(s.MissingDeps) != 1 {
		t.Fatalf("MissingDeps = %v", s.MissingDeps)
	}
	// missing dep entry may be stored as original string
	if s.MissingDeps[0] != "main/helper" && s.MissingDeps[0] != "helper" {
		t.Fatalf("dep = %q", s.MissingDeps[0])
	}
}

func TestCheckSingleAppStatusDepInstalled(t *testing.T) {
	root := setupStatusScoop(t)
	writeBucketManifest(t, root, "main", "helper", "1.0.0", "")
	writeBucketManifest(t, root, "main", "leaf", "1.0.0", `,"depends":["main/helper"]`)
	writeInstalledApp(t, root, "helper", "1.0.0", "main", false, "")
	writeInstalledApp(t, root, "leaf", "1.0.0", "main", false, `,"depends":["main/helper"]`)

	s := checkSingleAppStatus("leaf", false)
	if len(s.MissingDeps) != 0 {
		t.Fatalf("expected no missing deps, got %v", s.MissingDeps)
	}
}

func TestCheckSingleAppStatusFailed(t *testing.T) {
	root := setupStatusScoop(t)
	// app dir without current/version
	if err := os.MkdirAll(filepath.Join(root, "apps", "broken"), 0755); err != nil {
		t.Fatal(err)
	}
	s := checkSingleAppStatus("broken", false)
	if !s.Failed {
		t.Fatalf("expected failed: %+v", s)
	}
}

func TestCheckSingleAppStatusRemoved(t *testing.T) {
	root := setupStatusScoop(t)
	writeInstalledApp(t, root, "gone", "1.0.0", "main", false, "")
	// no bucket manifest
	s := checkSingleAppStatus("gone", false)
	if !s.Removed {
		t.Fatalf("expected removed: %+v", s)
	}
}

func TestCheckSingleAppStatusDeprecated(t *testing.T) {
	root := setupStatusScoop(t)
	writeInstalledApp(t, root, "old", "1.0.0", "main", false, "")
	depDir := filepath.Join(root, "buckets", "main", "deprecated")
	if err := os.MkdirAll(depDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "old.json"), []byte(`{
		"version":"1.0.0","homepage":"h","license":"MIT","url":"http://x"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	s := checkSingleAppStatus("old", false)
	if !s.Deprecated {
		t.Fatalf("expected deprecated: %+v", s)
	}
}

func TestCheckAppStatusesListsInstalled(t *testing.T) {
	root := setupStatusScoop(t)
	writeBucketManifest(t, root, "main", "a", "1.0.0", "")
	writeInstalledApp(t, root, "a", "1.0.0", "main", false, "")
	writeInstalledApp(t, root, "b", "1.0.0", "main", false, "")

	statuses := CheckAppStatuses()
	if len(statuses) < 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	names := map[string]bool{}
	for _, s := range statuses {
		names[s.Name] = true
	}
	if !names["a"] || !names["b"] {
		t.Fatalf("names = %v", names)
	}
}

func TestIsAppInstalled(t *testing.T) {
	root := setupStatusScoop(t)
	if isAppInstalled("nope") {
		t.Fatal("expected not installed")
	}
	writeInstalledApp(t, root, "yes", "1.0.0", "main", false, "")
	if !isAppInstalled("yes") {
		t.Fatal("expected installed")
	}
}

func TestReadInstallInfo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte(`{"bucket":"extras","hold":true,"architecture":"64bit"}`), 0644); err != nil {
		t.Fatal(err)
	}
	info := readInstallInfo(dir)
	if info.Bucket != "extras" || !info.Hold || info.Architecture != "64bit" {
		t.Fatalf("%+v", info)
	}
	empty := readInstallInfo(t.TempDir())
	if empty.Bucket != "" {
		t.Fatal(empty)
	}
}

func TestFindCurrentVersion(t *testing.T) {
	root := setupStatusScoop(t)
	writeInstalledApp(t, root, "x", "3.2.1", "main", false, "")
	ver, err := findCurrentVersion("x", false)
	if err != nil {
		t.Fatal(err)
	}
	// returns current path or version path
	if filepath.Base(ver) != "current" && filepath.Base(ver) != "3.2.1" {
		t.Fatalf("ver = %s", ver)
	}
}
