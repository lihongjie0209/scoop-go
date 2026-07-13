package dependency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestAppName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"git", "git"},
		{"main/git", "git"},
		{"extras/everything", "everything"},
		{"main/git.json", "git"},
		{"https://example.com/bucket/foo.json", "foo"},
		{"https://example.com/bucket/foo.json?raw=1", "foo"},
		{`\\share\bucket\bar.json`, "bar"},
		{"  spaced  ", "spaced"},
	}
	for _, tc := range cases {
		if got := AppName(tc.in); got != tc.want {
			t.Errorf("AppName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveBucketAppDepOrderAndNaming(t *testing.T) {
	root := setupScoop(t)

	mainBucket := filepath.Join(root, "buckets", "main", "bucket")
	mustMkdir(t, mainBucket)
	writeManifest(t, filepath.Join(mainBucket, "helper.json"), minimalManifest("1.0.0", nil, nil))
	writeManifest(t, filepath.Join(mainBucket, "leaf.json"), minimalManifest("2.0.0", []string{"main/helper"}, nil))
	writeManifest(t, filepath.Join(mainBucket, "a.json"), minimalManifest("1.0.0", []string{"main/b"}, nil))
	writeManifest(t, filepath.Join(mainBucket, "b.json"), minimalManifest("1.0.0", []string{"main/a"}, nil))

	resolved, err := Resolve("leaf", "64bit")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	helperIdx, leafIdx := indexOfApp(resolved, "helper"), indexOfApp(resolved, "leaf")
	if helperIdx < 0 || leafIdx < 0 {
		t.Fatalf("resolved = %v, missing helper or leaf", resolved)
	}
	if helperIdx > leafIdx {
		t.Fatalf("helper must install before leaf: %v", resolved)
	}
	for _, r := range resolved {
		if AppName(r) == "main" {
			t.Fatalf("bucket name treated as app: %v", resolved)
		}
	}
	// Prefer bucket/app form when known
	if !strings.Contains(resolved[helperIdx], "helper") {
		t.Fatalf("helper entry = %q", resolved[helperIdx])
	}

	if _, err := Resolve("a", "64bit"); err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestResolveMultiLevelDependencies(t *testing.T) {
	root := setupScoop(t)
	mainBucket := filepath.Join(root, "buckets", "main", "bucket")
	mustMkdir(t, mainBucket)
	writeManifest(t, filepath.Join(mainBucket, "base.json"), minimalManifest("1.0.0", nil, nil))
	writeManifest(t, filepath.Join(mainBucket, "mid.json"), minimalManifest("1.0.0", []string{"base"}, nil))
	writeManifest(t, filepath.Join(mainBucket, "top.json"), minimalManifest("1.0.0", []string{"mid"}, nil))

	resolved, err := Resolve("top", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	if indexOfApp(resolved, "base") > indexOfApp(resolved, "mid") ||
		indexOfApp(resolved, "mid") > indexOfApp(resolved, "top") {
		t.Fatalf("bad order: %v", resolved)
	}
}

func TestMissingSkipsInstalled(t *testing.T) {
	root := setupScoop(t)
	mustMkdir(t, filepath.Join(root, "apps", "helper", "current"))

	resolved := []string{"main/helper", "main/leaf"}
	missing := Missing(resolved, "leaf", true)
	if len(missing) != 1 || AppName(missing[0]) != "leaf" {
		t.Fatalf("Missing = %v, want only leaf", missing)
	}
	if got := Missing(resolved, "leaf", false); len(got) != 0 {
		t.Fatalf("Missing(includeRoot=false) = %v, want empty", got)
	}
}

func TestIsInstalled(t *testing.T) {
	root := setupScoop(t)
	if IsInstalled("missing-app") {
		t.Fatal("expected not installed")
	}
	mustMkdir(t, filepath.Join(root, "apps", "present", "current"))
	if !IsInstalled("present") {
		t.Fatal("expected installed")
	}
	if !IsInstalled("main/present") {
		t.Fatal("expected installed via bucket/app ref")
	}
}

func TestInstallationHelpersInnoAndDark(t *testing.T) {
	setupScoop(t)

	m := &manifest.Manifest{
		Version:   "1.0.0",
		Homepage:  "https://example.com",
		License:   "MIT",
		InnoSetup: true,
		URL:       manifest.FlexibleStrings{"https://example.com/setup.exe"},
		PreInstall: manifest.FlexibleStrings{
			"Expand-DarkArchive $dir\\bundle.exe $dir\\extracted",
		},
	}
	helpers := getInstallationHelpers(m, "64bit")
	if !contains(helpers, "innounp") {
		t.Fatalf("expected innounp, got %v", helpers)
	}
	if !contains(helpers, "dark") {
		t.Fatalf("expected dark, got %v", helpers)
	}
}

func TestInstallationHelpersSkipWhenInstalled(t *testing.T) {
	root := setupScoop(t)
	mustMkdir(t, filepath.Join(root, "apps", "innounp", "current"))

	m := &manifest.Manifest{
		Version:   "1.0.0",
		Homepage:  "https://example.com",
		License:   "MIT",
		InnoSetup: true,
		URL:       manifest.FlexibleStrings{"https://example.com/setup.exe"},
	}
	helpers := getInstallationHelpers(m, "64bit")
	if contains(helpers, "innounp") {
		t.Fatalf("installed helper should be omitted, got %v", helpers)
	}
}

func TestInstallationHelpersLessmsiForMSI(t *testing.T) {
	setupScoop(t)
	// PS: lessmsi helper only when use_lessmsi is true
	cfg := app.Config()
	if cfg == nil {
		t.Fatal("config not initialized")
	}
	if err := cfg.Set("use_lessmsi", true); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Version:  "1.0.0",
		Homepage: "https://example.com",
		License:  "MIT",
		URL:      manifest.FlexibleStrings{"https://example.com/pkg.msi"},
	}
	helpers := getInstallationHelpers(m, "64bit")
	if !contains(helpers, "lessmsi") {
		t.Fatalf("expected lessmsi for .msi URL with use_lessmsi, got %v", helpers)
	}

	// Without use_lessmsi: no lessmsi (native msiexec path)
	if err := cfg.Set("use_lessmsi", false); err != nil {
		t.Fatal(err)
	}
	helpers = getInstallationHelpers(m, "64bit")
	if contains(helpers, "lessmsi") {
		t.Fatalf("lessmsi must not be pulled when use_lessmsi=false, got %v", helpers)
	}

	// Expand-MsiArchive also gated by use_lessmsi
	if err := cfg.Set("use_lessmsi", true); err != nil {
		t.Fatal(err)
	}
	m2 := &manifest.Manifest{
		Version:  "1.0.0",
		Homepage: "https://example.com",
		License:  "MIT",
		URL:      manifest.FlexibleStrings{"https://example.com/pkg.zip"},
		PreInstall: manifest.FlexibleStrings{
			"Expand-MsiArchive $dir\\pkg.msi $dir",
		},
	}
	helpers = getInstallationHelpers(m2, "64bit")
	if !contains(helpers, "lessmsi") {
		t.Fatalf("expected lessmsi for Expand-MsiArchive with use_lessmsi, got %v", helpers)
	}
}

func TestResolveIncludesHelpersBeforeApp(t *testing.T) {
	root := setupScoop(t)
	mainBucket := filepath.Join(root, "buckets", "main", "bucket")
	mustMkdir(t, mainBucket)
	writeManifest(t, filepath.Join(mainBucket, "innounp.json"), minimalManifest("1.0.0", nil, nil))
	writeManifest(t, filepath.Join(mainBucket, "myapp.json"), `{
		"version": "1.0.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"url": "https://example.com/setup.exe",
		"innosetup": true
	}`)

	resolved, err := Resolve("myapp", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	if indexOfApp(resolved, "innounp") < 0 {
		t.Fatalf("expected innounp helper in tree: %v", resolved)
	}
	if indexOfApp(resolved, "innounp") > indexOfApp(resolved, "myapp") {
		t.Fatalf("helper should come before app: %v", resolved)
	}
}

// --- helpers ---

func setupScoop(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	for _, d := range []string{"apps", "buckets", "cache", "shims", "persist"} {
		mustMkdir(t, filepath.Join(root, d))
	}
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func minimalManifest(version string, depends []string, extra map[string]any) string {
	depJSON := "[]"
	if len(depends) > 0 {
		parts := make([]string, len(depends))
		for i, d := range depends {
			parts[i] = `"` + d + `"`
		}
		depJSON = "[" + strings.Join(parts, ",") + "]"
	}
	return `{
		"version": "` + version + `",
		"homepage": "https://example.com",
		"license": "MIT",
		"url": "https://example.com/f.zip",
		"depends": ` + depJSON + `
	}`
}

func indexOfApp(resolved []string, name string) int {
	for i, r := range resolved {
		if AppName(r) == name {
			return i
		}
	}
	return -1
}
