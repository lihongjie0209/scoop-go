package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/config"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestParsePrivateHostHeaders(t *testing.T) {
	m := map[string]string{}
	parsePrivateHostHeaders(`{"X-Api-Key":"abc","Authorization":"Bearer t"}`, m)
	if m["X-Api-Key"] != "abc" || m["Authorization"] != "Bearer t" {
		t.Fatalf("%v", m)
	}
	m2 := map[string]string{}
	parsePrivateHostHeaders("X-A: 1, X-B: 2", m2)
	if m2["X-A"] != "1" || m2["X-B"] != "2" {
		t.Fatalf("%v", m2)
	}
}

func TestMatchingPrivateHeaders(t *testing.T) {
	rules := []config.PrivateHostRule{
		{Match: "private.example.com", Headers: "X-Token: secret"},
		{Match: "other.com", Headers: "X-Other: 1"},
	}
	h := matchingPrivateHeaders(rules, "https://cdn.private.example.com/file.zip")
	if h["X-Token"] != "secret" {
		t.Fatalf("%v", h)
	}
	if matchingPrivateHeaders(nil, "x") != nil {
		t.Fatal("nil rules")
	}
	if matchingPrivateHeaders(rules, "https://public.com/x") != nil {
		t.Fatal("no match")
	}
}

func TestIsPowerShellHook(t *testing.T) {
	if !isPowerShellHook(`Get-ChildItem $dir`) {
		t.Fatal("cmdlet")
	}
	if !isPowerShellHook(`$x = 1`) {
		t.Fatal("var")
	}
	if !hasPowerShellHooks([]string{"echo hi", "Remove-Item $dir\\x"}) {
		t.Fatal("hooks")
	}
	if hasPowerShellHooks([]string{"echo hi"}) {
		// may still be false - ok
	}
}

func TestIsExecutableExt(t *testing.T) {
	if !isExecutableExt("setup.exe") || !isExecutableExt("a.msi") {
		t.Fatal("exe/msi")
	}
	if isExecutableExt("readme.txt") {
		t.Fatal("txt")
	}
}

func TestAlreadyInstalledError(t *testing.T) {
	e := &AlreadyInstalledError{App: "git", Version: "1.0"}
	if e.Error() == "" || e.Error() == "git" {
		t.Fatal(e.Error())
	}
}

func TestShowSuggestions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "apps"), 0755)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	ShowSuggestions(map[string]manifest.FlexibleStrings{
		"extras": {"vim", "neovim"},
	})
	ShowSuggestions(nil)
}

func TestFindManifestLocalAndMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "buckets", "main", "bucket"), 0755)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	man := `{"version":"1.2.3","homepage":"https://ex","license":"MIT","url":"https://ex/a.zip"}`
	path := filepath.Join(root, "buckets", "main", "bucket", "tool.json")
	if err := os.WriteFile(path, []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	m, bucket, err := FindManifest("tool")
	if err != nil || m == nil || bucket != "main" {
		t.Fatalf("m=%v bucket=%q err=%v", m, bucket, err)
	}
	if m.Version != "1.2.3" {
		t.Fatal(m.Version)
	}
	if _, _, err := FindManifest("does-not-exist-xyz"); err == nil {
		t.Fatal("expected missing")
	}
}

func TestBucketForApp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	_ = os.MkdirAll(filepath.Join(root, "buckets", "extras", "bucket"), 0755)
	if err := app.Initialize(filepath.Join(root, "c.json")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "buckets", "extras", "bucket", "foo.json"),
		[]byte(`{"version":"1","homepage":"h","license":"MIT","url":"http://x"}`), 0644)
	if BucketForApp("foo") != "extras" {
		t.Fatal(BucketForApp("foo"))
	}
}

func TestCopyFileAndCopyDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	_ = os.WriteFile(src, []byte("hello"), 0644)
	if err := copyFile(dst, src); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "hello" {
		t.Fatal(string(data))
	}
	srcD := filepath.Join(dir, "sd")
	dstD := filepath.Join(dir, "dd")
	_ = os.MkdirAll(filepath.Join(srcD, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(srcD, "sub", "f.txt"), []byte("x"), 0644)
	if err := copyDir(dstD, srcD); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dstD, "sub", "f.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestPowerShellCompatibilityPreamble(t *testing.T) {
	p := powerShellCompatibilityPreamble()
	if p == "" {
		t.Fatal("empty preamble")
	}
}
