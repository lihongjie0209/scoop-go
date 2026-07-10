package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPath(t *testing.T) {
	origPath := os.Getenv("PATH")

	err := AddPath([]string{"/test/path1", "/test/path2"}, "PATH", false)
	if err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	newPath := os.Getenv("PATH")
	if !strings.Contains(newPath, "/test/path1") {
		t.Error("expected /test/path1 in PATH")
	}
	if !strings.Contains(newPath, "/test/path2") {
		t.Error("expected /test/path2 in PATH")
	}

	os.Setenv("PATH", origPath)
}

func TestAddPathDuplicate(t *testing.T) {
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/test/unique")

	AddPath([]string{"/test/unique"}, "PATH", false)
	AddPath([]string{"/test/unique"}, "PATH", false)

	newPath := os.Getenv("PATH")
	count := strings.Count(newPath, "/test/unique")
	if count > 1 {
		t.Errorf("expected no duplicates, found %d", count)
	}

	os.Setenv("PATH", origPath)
}

func TestRemovePath(t *testing.T) {
	origPath := os.Getenv("PATH")
	sep := string(filepath.ListSeparator)
	os.Setenv("PATH", "/keep/this"+sep+"/remove/this")

	err := RemovePath([]string{"/remove/this"}, "PATH", false)
	if err != nil {
		t.Fatalf("RemovePath failed: %v", err)
	}

	newPath := os.Getenv("PATH")
	if strings.Contains(newPath, "/remove/this") {
		t.Error("expected /remove/this to be removed")
	}
	if !strings.Contains(newPath, "/keep/this") {
		t.Error("expected /keep/this to remain")
	}

	os.Setenv("PATH", origPath)
}

func TestSetEnv(t *testing.T) {
	err := SetEnv("SCOOP_TEST_VAR", "test-value", false)
	if err != nil {
		t.Fatalf("SetEnv failed: %v", err)
	}

	if os.Getenv("SCOOP_TEST_VAR") != "test-value" {
		t.Errorf("expected test-value, got %s", os.Getenv("SCOOP_TEST_VAR"))
	}

	os.Unsetenv("SCOOP_TEST_VAR")
}

func TestGetEnv(t *testing.T) {
	os.Setenv("SCOOP_GET_TEST", "hello")
	val := GetEnv("SCOOP_GET_TEST", false)
	if val != "hello" {
		t.Errorf("expected hello, got %s", val)
	}
	os.Unsetenv("SCOOP_GET_TEST")
}

func TestIsInPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	pathVar := "/usr/bin" + sep + "/usr/local/bin"
	if !isInPath("/usr/bin", pathVar) {
		t.Error("expected /usr/bin to be found")
	}
	if isInPath("/nonexistent", pathVar) {
		t.Error("expected /nonexistent to not be found")
	}
}

func TestIsInPathWindowsPaths(t *testing.T) {
	sep := string(os.PathListSeparator)
	pathVar := `C:\Program Files\Git` + sep + `D:\tools\scoop\shims`
	if !isInPath(`C:\Program Files\Git`, pathVar) {
		t.Error("expected C:\\Program Files\\Git to be found")
	}
	if isInPath(`C:\Windows\System32`, pathVar) {
		t.Error("expected C:\\Windows\\System32 to NOT be found")
	}
	if !isInPath(`D:\tools\scoop\shims`, pathVar) {
		t.Error("expected D:\\tools\\scoop\\shims to be found")
	}
}
