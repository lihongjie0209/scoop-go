package proc

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAnyRunningUnderPath_UsesEnumerator(t *testing.T) {
	root := filepath.Join("C:", "scoop", "apps", "git")
	paths := []string{
		filepath.Join(root, "current", "cmd", "git.exe"),
		filepath.Join("C:", "Windows", "System32", "notepad.exe"),
	}
	running, names := AnyRunningUnderPath(root, func() ([]string, error) {
		return paths, nil
	})
	if !running {
		t.Fatal("expected running process under app path")
	}
	if len(names) != 1 || !strings.Contains(strings.ToLower(names[0]), "git") {
		t.Fatalf("names = %v", names)
	}
}

func TestAnyRunningUnderPath_NoMatch(t *testing.T) {
	root := filepath.Join("C:", "scoop", "apps", "git")
	running, names := AnyRunningUnderPath(root, func() ([]string, error) {
		return []string{filepath.Join("C:", "Windows", "notepad.exe")}, nil
	})
	if running || len(names) != 0 {
		t.Fatalf("running=%v names=%v", running, names)
	}
}

func TestAnyRunningUnderPath_EnumeratorErrorIsSafe(t *testing.T) {
	running, _ := AnyRunningUnderPath(`C:\x`, func() ([]string, error) {
		return nil, errFake
	})
	if running {
		t.Fatal("on error, treat as not running (safe proceed)")
	}
}

var errFake = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

func TestAnyRunningByImageName(t *testing.T) {
	images := []string{"git.exe", "GIT.EXE", "code.exe"}
	if !AnyRunningByImageName([]string{"git", "other"}, func() ([]string, error) {
		return images, nil
	}) {
		t.Fatal("expected git image match")
	}
	if AnyRunningByImageName([]string{"missing"}, func() ([]string, error) {
		return images, nil
	}) {
		t.Fatal("expected no match")
	}
}

func TestNormalizeImage(t *testing.T) {
	cases := map[string]string{
		"git":     "git.exe",
		"git.exe": "git.exe",
		"GIT.EXE": "git.exe",
		"":        "",
	}
	for in, want := range cases {
		if got := normalizeImage(in); got != want {
			t.Errorf("normalizeImage(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPathPrefixMatch(t *testing.T) {
	root := filepath.Clean(`C:\scoop\apps\git`)
	if !pathIsUnder(filepath.Join(root, "current", "git.exe"), root) {
		t.Fatal("child should match")
	}
	if pathIsUnder(filepath.Join(`C:\scoop\apps\github`, "x.exe"), root) {
		t.Fatal("sibling prefix must not match")
	}
}

func TestListProcessPaths_DoesNotPanic(t *testing.T) {
	// Smoke: real implementation should return without panic on all platforms.
	paths, err := ListProcessPaths()
	if err != nil {
		// On non-windows we may return nil,nil or empty
		if runtime.GOOS == "windows" {
			t.Logf("ListProcessPaths err (allowed if restricted): %v", err)
		}
	}
	_ = paths
}
