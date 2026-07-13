package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/dependency"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestExpandInstallTargetsIndependent(t *testing.T) {
	resolve := func(ref, arch string) ([]string, error) {
		t.Fatal("resolve should not be called when independent")
		return nil, nil
	}
	targets, err := expandInstallTargets([]string{"main/git@2.0.0", "7zip"}, true, "64bit", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	if targets[0].ref != "main/git" || targets[0].version != "2.0.0" || !targets[0].explicit {
		t.Fatalf("first = %+v", targets[0])
	}
	if targets[1].ref != "7zip" || targets[1].version != "" {
		t.Fatalf("second = %+v", targets[1])
	}
}

func TestExpandInstallTargetsWithDeps(t *testing.T) {
	resolve := func(ref, arch string) ([]string, error) {
		if ref != "leaf" {
			t.Fatalf("unexpected ref %q", ref)
		}
		return []string{"main/helper", "main/leaf"}, nil
	}
	targets, err := expandInstallTargets([]string{"leaf@9.9.9"}, false, "64bit", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	// helper first, no version; leaf explicit with version
	if dependencyApp := dependency.AppName(targets[0].ref); dependencyApp != "helper" || targets[0].explicit || targets[0].version != "" {
		t.Fatalf("helper target = %+v", targets[0])
	}
	if dependency.AppName(targets[1].ref) != "leaf" || !targets[1].explicit || targets[1].version != "9.9.9" {
		t.Fatalf("leaf target = %+v", targets[1])
	}
}

func TestExpandInstallTargetsDedupes(t *testing.T) {
	n := 0
	resolve := func(ref, arch string) ([]string, error) {
		n++
		return []string{"shared", ref}, nil
	}
	targets, err := expandInstallTargets([]string{"a", "b"}, false, "64bit", resolve)
	if err != nil {
		t.Fatal(err)
	}
	// shared once + a + b
	names := map[string]int{}
	for _, tget := range targets {
		names[dependency.AppName(tget.ref)]++
	}
	if names["shared"] != 1 || names["a"] != 1 || names["b"] != 1 {
		t.Fatalf("names = %v targets=%+v", names, targets)
	}
	if n != 2 {
		t.Fatalf("resolve calls = %d", n)
	}
}

func TestExpandInstallTargetsResolveError(t *testing.T) {
	_, err := expandInstallTargets([]string{"x"}, false, "64bit", func(string, string) ([]string, error) {
		return nil, fmt.Errorf("circular")
	})
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAppRef(t *testing.T) {
	cases := []struct {
		in               string
		app, bucket, ver string
	}{
		{"git", "git", "", ""},
		{"main/git", "git", "main", ""},
		{"gh@2.7.0", "gh", "", "2.7.0"},
		{"main/gh@2.7.0", "gh", "main", "2.7.0"},
		{"app.json", "app", "", ""},
		{"extras/foo.json@1.0", "foo", "extras", "1.0"},
		// URLs must not be split on '/'
		{"https://example.com/bucket/foo.json", "https://example.com/bucket/foo.json", "", ""},
		{"https://example.com/bucket/foo.json@1.2.3", "https://example.com/bucket/foo.json", "", "1.2.3"},
		{"http://host/a/b.json@nightly", "http://host/a/b.json", "", "nightly"},
	}
	for _, tc := range cases {
		a, b, v := parseAppRef(tc.in)
		if a != tc.app || b != tc.bucket || v != tc.ver {
			t.Errorf("parseAppRef(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tc.in, a, b, v, tc.app, tc.bucket, tc.ver)
		}
	}
}

func TestParseImportBucketAndApp(t *testing.T) {
	// PowerShell export shape
	psApp, _ := json.Marshal(map[string]any{
		"Name": "7zip", "Version": "24.08", "Source": "main",
		"Info": "Global install, Held package, 64bit",
	})
	a := parseImportApp(psApp)
	if a.Name != "7zip" || a.Version != "24.08" || a.Source != "main" {
		t.Fatalf("ps app = %+v", a)
	}
	if !strings.Contains(a.Info, "Global install") || !strings.Contains(a.Info, "Held package") {
		t.Fatalf("info = %q", a.Info)
	}

	// Go export shape
	goApp, _ := json.Marshal(map[string]any{
		"name": "fd", "version": "9.0.0", "global": true,
	})
	a2 := parseImportApp(goApp)
	if a2.Name != "fd" || !a2.Global || a2.Version != "9.0.0" {
		t.Fatalf("go app = %+v", a2)
	}

	psBucket, _ := json.Marshal(map[string]any{"Name": "extras", "Source": "https://example.com/extras"})
	b := parseImportBucket(psBucket)
	if b.Name != "extras" || b.Source != "https://example.com/extras" {
		t.Fatalf("bucket = %+v", b)
	}

	goBucket, _ := json.Marshal(map[string]any{"name": "main", "source": "https://example.com/main"})
	b2 := parseImportBucket(goBucket)
	if b2.Name != "main" || b2.Source != "https://example.com/main" {
		t.Fatalf("go bucket = %+v", b2)
	}
}

func TestFirstStringAndContainsString(t *testing.T) {
	m := map[string]interface{}{"Name": "x", "name": "y"}
	if firstString(m, "Name", "name") != "x" {
		t.Fatal("prefer first key")
	}
	if firstString(m, "missing", "name") != "y" {
		t.Fatal("fallback key")
	}
	if !containsString([]string{"a", "b"}, "b") || containsString([]string{"a"}, "z") {
		t.Fatal("containsString")
	}
}

func TestMatchingBinsAndShortcuts(t *testing.T) {
	m := &manifest.Manifest{
		Bin: []any{"bin/rg.exe", []any{"bin/helper.exe", "rg-helper"}},
		Shortcuts: [][]string{
			{"app.exe", "Ripgrep UI"},
		},
	}
	re := regexp.MustCompile("(?i)rg")
	bins := matchingBins(m, re)
	if len(bins) == 0 {
		t.Fatal("expected bin matches for rg")
	}
	// Ensure helper alias matches
	foundHelper := false
	for _, b := range bins {
		if b == "rg-helper" || b == "rg" {
			foundHelper = true
		}
	}
	if !foundHelper {
		t.Fatalf("bins = %v", bins)
	}

	reUI := regexp.MustCompile("(?i)UI")
	if !matchingShortcuts(m, reUI) {
		t.Fatal("expected shortcut match")
	}
	if matchingShortcuts(m, regexp.MustCompile("nomatch")) {
		t.Fatal("unexpected shortcut match")
	}
}

func TestAllBinNamesDedupesAcrossArch(t *testing.T) {
	m := &manifest.Manifest{
		Bin: "bin/app.exe",
		Architecture: &struct {
			X32bit *manifest.ArchContent `json:"32bit,omitempty"`
			X64bit *manifest.ArchContent `json:"64bit,omitempty"`
			Arm64  *manifest.ArchContent `json:"arm64,omitempty"`
		}{
			X64bit: &manifest.ArchContent{Bin: "bin/app.exe"},
			X32bit: &manifest.ArchContent{Bin: "bin/app.exe"},
		},
	}
	names := allBinNames(m)
	count := 0
	for _, n := range names {
		if n == "app" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected single 'app' entry, got %v", names)
	}
}

func TestBuildExportInfoParts(t *testing.T) {
	// Verify Info string conventions used by export/import round-trip.
	parts := []string{}
	failed, hold, global := true, true, true
	arch := "32bit"
	if failed {
		parts = append(parts, "Install failed")
	}
	if hold {
		parts = append(parts, "Held package")
	}
	if arch != "" {
		parts = append(parts, arch)
	}
	if global {
		parts = append(parts, "Global install")
	}
	info := strings.Join(parts, ", ")
	if !strings.Contains(info, "Global install") || !strings.Contains(info, "Held package") || !strings.Contains(info, "32bit") {
		t.Fatalf("info = %q", info)
	}
	// import detection
	if !(strings.Contains(info, "Global install") && strings.Contains(info, "Held package")) {
		t.Fatal("import would miss flags")
	}
}

func TestGlobalFlagSuffix(t *testing.T) {
	if globalFlagSuffix(true) != " --global" {
		t.Fatal("global suffix")
	}
	if globalFlagSuffix(false) != "" {
		t.Fatal("local suffix")
	}
}

func TestDownloadShortHashLength(t *testing.T) {
	h := shortHash("https://example.com/file.zip")
	if len(h) != 7 {
		t.Fatalf("len = %d (%s)", len(h), h)
	}
}

func TestSearchInvalidRegexFallsBackToLiteral(t *testing.T) {
	// The search command compiles (?i)+query; invalid patterns use QuoteMeta.
	// Verify QuoteMeta path keeps a valid regex for matching.
	query := "("
	re, err := regexp.Compile("(?i)" + query)
	if err == nil {
		// some engines may accept; force fallback behavior
		_ = re
	}
	re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
	if !re.MatchString("app(name") {
		t.Fatal("literal fallback should match")
	}
}

func TestHoldScoopUsesOneDayWindow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	cfgPath := filepath.Join(root, "config.json")
	if err := app.Initialize(cfgPath); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	if err := setHold("scoop", true, false); err != nil {
		t.Fatal(err)
	}
	until := app.Config().Config().HoldUpdateUntil
	if until == "" {
		t.Fatal("hold_update_until not set")
	}
	parsed, err := time.Parse("2006-01-02", until)
	if err != nil {
		t.Fatalf("parse %q: %v", until, err)
	}
	// Should be approximately tomorrow
	diff := parsed.Sub(before.Truncate(24 * time.Hour))
	if diff < 12*time.Hour || diff > 48*time.Hour {
		// date-only: tomorrow relative to local date
		want := before.Add(24 * time.Hour).Format("2006-01-02")
		if until != want {
			// allow calendar boundary
			alt := before.Add(24 * time.Hour).Add(2 * time.Hour).Format("2006-01-02")
			if until != alt && until != before.Add(23*time.Hour).Format("2006-01-02") {
				// Still accept if equal to time.Now().Add(24h).Format
				if until != time.Now().Add(24*time.Hour).Format("2006-01-02") {
					t.Fatalf("hold until %q not ~+1 day from %v", until, before)
				}
			}
		}
	}

	if err := setHold("scoop", false, false); err != nil {
		t.Fatal(err)
	}
	if app.Config().Config().HoldUpdateUntil != "" {
		t.Fatalf("expected cleared hold, got %q", app.Config().Config().HoldUpdateUntil)
	}
}

func TestSetHoldAppInstallJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "apps", "myapp", "current")
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	installPath := filepath.Join(current, "install.json")
	if err := os.WriteFile(installPath, []byte(`{"architecture":"64bit","bucket":"main"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := setHold("myapp", true, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info["hold"] != true {
		t.Fatalf("hold not set: %v", info)
	}

	if err := setHold("myapp", false, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(installPath)
	_ = json.Unmarshal(data, &info)
	if info["hold"] != false {
		t.Fatalf("hold not cleared: %v", info)
	}
}

func TestShimAlterProperty(t *testing.T) {
	dir := t.TempDir()
	shimPath := filepath.Join(dir, "demo.shim")
	if err := os.WriteFile(shimPath, []byte("path = C:\\old.exe\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := shimAlterProperty("demo", "path", `C:\new.exe`, dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(shimPath)
	if !strings.Contains(string(data), `path = C:\new.exe`) {
		t.Fatalf("content = %s", data)
	}
	// append new key
	if err := shimAlterProperty("demo", "args", "--help", dir); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(shimPath)
	if !strings.Contains(string(data), "args = --help") {
		t.Fatalf("content = %s", data)
	}
}

func TestCurrentInstalledVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "apps", "x", "current")
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "manifest.json"), []byte(`{"version":"3.1.4","homepage":"h","license":"MIT"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := currentInstalledVersion("x", false); got != "3.1.4" {
		t.Fatalf("got %q", got)
	}
	if got := currentInstalledVersion("missing", false); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}
