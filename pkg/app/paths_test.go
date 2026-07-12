package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeAndPaths(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	t.Setenv("SCOOP", root)
	t.Setenv("SCOOP_GLOBAL", global)
	t.Setenv("SCOOP_CACHE", filepath.Join(root, "mycache"))

	cfgPath := filepath.Join(root, "config.json")
	if err := Initialize(cfgPath); err != nil {
		t.Fatal(err)
	}
	if Config() == nil {
		t.Fatal("config nil")
	}
	d := Dirs()
	if d.ScoopDir != root {
		t.Fatalf("ScoopDir=%s", d.ScoopDir)
	}
	if d.GlobalDir != global {
		t.Fatalf("GlobalDir=%s", d.GlobalDir)
	}
	if d.CacheDir != filepath.Join(root, "mycache") {
		t.Fatalf("CacheDir=%s", d.CacheDir)
	}
	if AppDir(false) != filepath.Join(root, "apps") {
		t.Fatal(AppDir(false))
	}
	if AppDir(true) != filepath.Join(global, "apps") {
		t.Fatal(AppDir(true))
	}
	if AppVersionDir("git", "1.0", false) != filepath.Join(root, "apps", "git", "1.0") {
		t.Fatal(AppVersionDir("git", "1.0", false))
	}
	if AppCurrentDir("git", false) != filepath.Join(root, "apps", "git", "current") {
		t.Fatal(AppCurrentDir("git", false))
	}
	if PersistDir("git", false) != filepath.Join(root, "persist", "git") {
		t.Fatal(PersistDir("git", false))
	}
	if PersistDir("git", true) != filepath.Join(global, "persist", "git") {
		t.Fatal(PersistDir("git", true))
	}
	if ShimDir(false) != filepath.Join(root, "shims") {
		t.Fatal(ShimDir(false))
	}
	if ShimDir(true) != filepath.Join(global, "shims") {
		t.Fatal(ShimDir(true))
	}
	cp := CachePath("app", "1.0", "https://ex.com/f.zip")
	if filepath.Dir(cp) != d.CacheDir {
		t.Fatal(cp)
	}
	if !strings.Contains(cp, "app#1.0#") {
		t.Fatalf("cache name = %s", filepath.Base(cp))
	}
	if GetLogger() == nil {
		t.Fatal("logger nil")
	}
}

func TestResolveDirsFromConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", "")
	t.Setenv("SCOOP_GLOBAL", "")
	t.Setenv("SCOOP_CACHE", "")

	cfgPath := filepath.Join(root, "cfg.json")
	payload := map[string]any{
		"root_path":         filepath.Join(root, "scoop-root"),
		"global_path":       filepath.Join(root, "scoop-global"),
		"cache_path":        filepath.Join(root, "scoop-cache"),
		"use_isolated_path": true,
	}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(cfgPath); err != nil {
		t.Fatal(err)
	}
	d := Dirs()
	if filepath.Base(d.ScoopDir) != "scoop-root" {
		t.Fatalf("ScoopDir=%s", d.ScoopDir)
	}
	if filepath.Base(d.GlobalDir) != "scoop-global" {
		t.Fatalf("GlobalDir=%s", d.GlobalDir)
	}
	if filepath.Base(d.CacheDir) != "scoop-cache" {
		t.Fatalf("CacheDir=%s", d.CacheDir)
	}
	if d.PathEnvVar != "SCOOP_PATH" {
		t.Fatalf("PathEnvVar=%s", d.PathEnvVar)
	}
}

func TestIsolatedPathCustomName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	t.Setenv("SCOOP_GLOBAL", "")
	t.Setenv("SCOOP_CACHE", "")
	cfgPath := filepath.Join(root, "c.json")
	data, _ := json.Marshal(map[string]any{"use_isolated_path": "MY_SCOOP_PATH"})
	_ = os.WriteFile(cfgPath, data, 0644)
	if err := Initialize(cfgPath); err != nil {
		t.Fatal(err)
	}
	if Dirs().PathEnvVar != "MY_SCOOP_PATH" {
		t.Fatalf("got %s", Dirs().PathEnvVar)
	}
}

func TestNeedsMainBucket(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	t.Setenv("SCOOP_GLOBAL", filepath.Join(root, "g"))
	t.Setenv("SCOOP_CACHE", "")
	if err := Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(Dirs().BucketsDir, 0755)
	if !NeedsMainBucket() {
		t.Fatal("empty buckets should need main")
	}
	if err := os.MkdirAll(filepath.Join(Dirs().BucketsDir, "main"), 0755); err != nil {
		t.Fatal(err)
	}
	if NeedsMainBucket() {
		t.Fatal("with main bucket should not need")
	}
}

func TestShortHashStable(t *testing.T) {
	h1 := shortHash("abc")
	h2 := shortHash("abc")
	h3 := shortHash("xyz")
	if h1 != h2 || len(h1) != 7 {
		t.Fatalf("hash %q", h1)
	}
	if h1 == h3 {
		t.Fatal("different inputs same hash")
	}
}

func TestLoggerInfoWarnErrorSuccessPrint(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false)
	l.Info("i %d", 1)
	l.Warn("w")
	l.Error("e")
	l.Success("s")
	l.Print("p%s", "x")
	l.Println("ln")
	out := buf.String()
	for _, want := range []string{"INFO", "WARN", "ERROR", "SUCCESS", "px", "ln"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestPackageLogFunctions(t *testing.T) {
	saved := logger
	defer func() { logger = saved }()
	var buf bytes.Buffer
	logger = NewLogger(&buf, true)
	LogInfo("info")
	LogWarn("warn")
	LogError("err")
	LogSuccess("ok")
	LogDebug("dbg")
	out := buf.String()
	for _, w := range []string{"info", "warn", "err", "ok", "dbg"} {
		if !strings.Contains(out, w) {
			t.Fatalf("missing %s: %s", w, out)
		}
	}
}
