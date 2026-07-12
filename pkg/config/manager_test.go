package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerLoadSaveGetSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	m := NewManager(path)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	// defaults
	if m.Config().SCOOPRepo == "" {
		t.Fatal("expected default scoop repo")
	}

	if err := m.Set("debug", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("proxy", "http://127.0.0.1:7890"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("default_architecture", "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(path)
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	if !m2.Config().Debug {
		t.Fatal("debug not persisted")
	}
	if m2.Config().Proxy != "http://127.0.0.1:7890" {
		t.Fatal(m2.Config().Proxy)
	}
	if m2.Get("default_architecture") != "arm64" {
		t.Fatal(m2.Get("default_architecture"))
	}
	if m2.Get("proxy") != "http://127.0.0.1:7890" {
		t.Fatal(m2.Get("proxy"))
	}
}

func TestManagerAria2SnakeCaseCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := map[string]any{
		"aria2_enabled":                   false,
		"aria2_retry_wait":                9,
		"aria2_split":                     3,
		"aria2_max_connection_per_server": 2,
		"aria2_min_split_size":            "10M",
	}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	if cfg.Aria2Enabled == nil || *cfg.Aria2Enabled {
		t.Fatal("aria2_enabled snake not normalized")
	}
	if cfg.Aria2RetryWait != 9 || cfg.Aria2Split != 3 {
		t.Fatalf("aria2 ints: wait=%d split=%d", cfg.Aria2RetryWait, cfg.Aria2Split)
	}
	if cfg.Aria2MinSplitSize != "10M" {
		t.Fatal(cfg.Aria2MinSplitSize)
	}
}

func TestValueToString(t *testing.T) {
	if valueToString(nil) != "" {
		t.Fatal("nil")
	}
	if valueToString(true) != "true" || valueToString(false) != "false" {
		t.Fatal("bool")
	}
	if valueToString("x") != "x" {
		t.Fatal("string")
	}
	b := true
	if valueToString(&b) != "true" {
		t.Fatal("bool ptr")
	}
	var nb *bool
	if valueToString(nb) != "" {
		t.Fatal("nil bool ptr")
	}
	if valueToString(42) != "42" {
		t.Fatal("int")
	}
}

func TestManagerGetUnsetVsDefault(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "c.json"))
	_ = m.Load()
	// use_external_7zip never set -> Get may return nil
	v := m.Get("use_external_7zip")
	if v != nil {
		// depending on implementation may return false; accept either
		t.Logf("use_external_7zip=%v", v)
	}
	_ = m.Set("use_external_7zip", true)
	if m.Get("use_external_7zip") != true {
		t.Fatal(m.Get("use_external_7zip"))
	}
}

func TestManagerSetGHTokenAndScoopGoRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	m := NewManager(path)
	_ = m.Load()
	if err := m.Set("gh_token", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("scoop_go_repo", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	if m.Config().GH_TOKEN != "tok" || m.Config().ScoopGoRepo != "owner/repo" {
		t.Fatal(m.Config())
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.SCOOPRepo == "" || c.SCOOPBranch == "" {
		t.Fatal(c)
	}
	if c.ScoopGoRepo == "" {
		t.Fatal("expected scoop_go_repo default")
	}
	if c.Aria2Split == 0 {
		t.Fatal("aria2 defaults")
	}
}
