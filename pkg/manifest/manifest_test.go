package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidManifest(t *testing.T) {
	data := []byte(`{
		"version": "1.0.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"url": "https://example.com/app.zip",
		"bin": "app.exe"
	}`)

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", m.Version)
	}
	if m.Homepage != "https://example.com" {
		t.Errorf("expected homepage https://example.com, got %s", m.Homepage)
	}
	if len(m.URL) != 1 || m.URL[0] != "https://example.com/app.zip" {
		t.Errorf("unexpected URL: %v", m.URL)
	}
}

func TestGenerateUserManifestMergesAutoupdate(t *testing.T) {
	source := MustParse([]byte(`{
		"version":"24.09",
		"homepage":"https://example.test",
		"license":"MIT",
		"url":"https://example.invalid/current.zip",
		"hash":"old-hash",
		"architecture":{"64bit":{"url":"https://example.invalid/current-x64.zip","hash":"old-arch-hash"}},
		"autoupdate":{
			"bin":"tool-$majorVersion.exe",
			"architecture":{"64bit":{"url":"https://example.test/tool-$cleanVersion-x64.zip"}}
		}
	}`))
	data, err := GenerateUserManifest(source, "23.07.1")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Version != "23.07.1" {
		t.Fatalf("version = %q", generated.Version)
	}
	if got := generated.GetURL("64bit"); len(got) != 1 || got[0] != "https://example.test/tool-23071-x64.zip" {
		t.Fatalf("generated URL = %#v", got)
	}
	if got := BinEntries(generated.Bin); len(got) != 1 || got[0][0] != "tool-23.exe" {
		t.Fatalf("generated bin = %#v", got)
	}
	if got := generated.GetHash("64bit"); len(got) != 0 {
		t.Fatalf("stale hash was retained: %#v", got)
	}
}

func TestGenerateUserManifestRequiresAutoupdate(t *testing.T) {
	source := &Manifest{Version: "2.0.0", URL: FlexibleStrings{"https://example.test/current.zip"}}
	if _, err := GenerateUserManifest(source, "1.0.0"); err == nil {
		t.Fatal("expected manifest without autoupdate to be rejected")
	}
}

func TestParseManifestMissingRequired(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"no version", []byte(`{"homepage": "https://example.com", "license": "MIT"}`)},
		{"no homepage", []byte(`{"version": "1.0", "license": "MIT"}`)},
		{"no license", []byte(`{"version": "1.0", "homepage": "https://example.com"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.data)
			if err == nil {
				t.Error("expected error for missing required field, got nil")
			}
		})
	}
}

func TestParseManifestWithArchitecture(t *testing.T) {
	data := []byte(`{
		"version": "2.0",
		"homepage": "https://example.com",
		"license": "Apache-2.0",
		"architecture": {
			"64bit": {
				"url": "https://example.com/app-x64.zip",
				"hash": "sha256:abc123"
			},
			"32bit": {
				"url": "https://example.com/app-x86.zip",
				"hash": "md5:def456"
			}
		}
	}`)

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test arch-specific URL
	urls64 := m.GetURL("64bit")
	if len(urls64) != 1 || urls64[0] != "https://example.com/app-x64.zip" {
		t.Errorf("unexpected 64bit URL: %v", urls64)
	}

	urls32 := m.GetURL("32bit")
	if len(urls32) != 1 || urls32[0] != "https://example.com/app-x86.zip" {
		t.Errorf("unexpected 32bit URL: %v", urls32)
	}

	// Test arch resolution
	if arch := m.ResolveArch("64bit"); arch != "64bit" {
		t.Errorf("expected 64bit, got %s", arch)
	}
	if arch := m.ResolveArch("32bit"); arch != "32bit" {
		t.Errorf("expected 32bit, got %s", arch)
	}
}

func TestBinEntries(t *testing.T) {
	tests := []struct {
		name string
		bin  interface{}
		want int
	}{
		{"single string", "app.exe", 1},
		{"array of strings", []interface{}{"app.exe", "tool.exe"}, 2},
		{"array of arrays", []interface{}{
			[]interface{}{"app.exe", "myapp"},
			[]interface{}{"tool.exe", "mytool", "--arg"},
		}, 2},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := BinEntries(tt.bin)
			if len(entries) != tt.want {
				t.Errorf("expected %d entries, got %d", tt.want, len(entries))
			}
		})
	}
}

func TestHashForURL(t *testing.T) {
	data := []byte(`{
		"version": "1.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"url": ["https://example.com/app.zip", "https://example.com/tool.zip"],
		"hash": ["sha256:abc", "sha256:def"]
	}`)

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hash := m.HashForURL("https://example.com/app.zip", "64bit")
	if hash != "sha256:abc" {
		t.Errorf("expected sha256:abc, got %s", hash)
	}

	hash = m.HashForURL("https://example.com/tool.zip", "64bit")
	if hash != "sha256:def" {
		t.Errorf("expected sha256:def, got %s", hash)
	}
}

func TestURLFilename(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/file.zip", "file.zip"},
		{"https://example.com/path/to/app.exe?download=1", "app.exe"},
	}

	for _, tt := range tests {
		got := URLFilename(tt.url)
		if got != tt.want {
			t.Errorf("URLFilename(%s) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestNotesAsString(t *testing.T) {
	data := []byte(`{
		"version": "1.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"notes": "A single note"
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Notes) != 1 || m.Notes[0] != "A single note" {
		t.Errorf("unexpected Notes: %v", m.Notes)
	}
}

func TestNotesAsArray(t *testing.T) {
	data := []byte(`{
		"version": "1.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"notes": ["Note 1", "Note 2", "Note 3"]
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Notes) != 3 {
		t.Errorf("expected 3 notes, got %d: %v", len(m.Notes), m.Notes)
	}
	if m.Notes[0] != "Note 1" || m.Notes[2] != "Note 3" {
		t.Errorf("unexpected Notes content: %v", m.Notes)
	}
}

func TestNotesOmitted(t *testing.T) {
	data := []byte(`{
		"version": "1.0",
		"homepage": "https://example.com",
		"license": "MIT"
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Notes) != 0 {
		t.Errorf("expected empty Notes, got %v", m.Notes)
	}
}

func TestNotesEmptyArray(t *testing.T) {
	data := []byte(`{
		"version": "1.0",
		"homepage": "https://example.com",
		"license": "MIT",
		"notes": []
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Notes) != 0 {
		t.Errorf("expected empty Notes array, got %v", m.Notes)
	}
}

func TestFlexibleStringsUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		{"single string", `"hello"`, []string{"hello"}},
		{"array", `["a", "b"]`, []string{"a", "b"}},
		{"empty string", `""`, []string{""}},
		{"empty array", `[]`, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fs FlexibleStrings
			if err := fs.UnmarshalJSON([]byte(tt.json)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fs) != len(tt.want) {
				t.Errorf("expected len %d, got %d: %v", len(tt.want), len(fs), fs)
				return
			}
			for i := range fs {
				if fs[i] != tt.want[i] {
					t.Errorf("index %d: expected %q, got %q", i, tt.want[i], fs[i])
				}
			}
		})
	}
}

// TestParseAllBucketManifests parses ALL manifests from local buckets.
// This ensures the parser handles every real-world manifest format.
func TestParseAllBucketManifests(t *testing.T) {
	bucketDirs := []string{
		os.ExpandEnv("$HOME/scoop/buckets/main/bucket"),
		os.ExpandEnv("$HOME/scoop/buckets/extras/bucket"),
	}

	parsed := 0
	failed := 0
	var failures []string

	for _, dir := range bucketDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				t.Skipf("Bucket directory not found: %s (run 'scoop init' first)", dir)
			}
			t.Fatalf("reading bucket dir %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				failed++
				failures = append(failures, fmt.Sprintf("%s: read error: %v", entry.Name(), err))
				continue
			}

			_, err = Parse(data)
			if err != nil {
				failed++
				failures = append(failures, fmt.Sprintf("%s: %v", entry.Name(), err))
				continue
			}
			parsed++
		}
	}

	if len(failures) > 0 {
		t.Errorf("\n=== %d manifests FAILED to parse (out of %d) ===\n", failed, parsed+failed)
		for _, f := range failures {
			t.Errorf("  FAIL: %s", f)
		}
	}
	t.Logf("Parsed %d / %d manifests successfully", parsed, parsed+failed)
}
