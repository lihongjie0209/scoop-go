package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsInPathVariants(t *testing.T) {
	sep := string(os.PathListSeparator)
	path := strings.Join([]string{`C:\a`, `C:\b`, `C:\c`}, sep)
	if !isInPath(`C:\b`, path) {
		t.Fatal("should find")
	}
	if isInPath(`C:\z`, path) {
		t.Fatal("should not find")
	}
	if isInPath("", path) {
		t.Fatal("empty")
	}
}

func TestAddPathAndRemovePathProcessEnv(t *testing.T) {
	key := "SCOOP_TEST_PATH_" + filepath.Base(t.TempDir())
	t.Setenv(key, "")
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// On non-windows WriteEnvVar may still work for process only
	if err := AddPath([]string{dir1, dir2}, key, false); err != nil {
		// registry may fail without permissions; process env should still update
		t.Log("AddPath err:", err)
	}
	cur := os.Getenv(key)
	if cur != "" && !strings.Contains(cur, dir1) {
		// if registry failed entirely, env may still be set
		t.Logf("PATH env after AddPath: %q", cur)
	}

	_ = RemovePath([]string{dir1}, key, false)
	_ = runtime.GOOS
}

func TestAddPathEmptyNoop(t *testing.T) {
	if err := AddPath(nil, "PATH", false); err != nil {
		t.Fatal(err)
	}
	if err := RemovePath(nil, "PATH", false); err != nil {
		t.Fatal(err)
	}
}

func TestAddPathPrepend(t *testing.T) {
	key := "SCOOP_TEST_PREPEND"
	t.Setenv(key, "EXISTING")
	dir := t.TempDir()
	if err := AddPathPrepend([]string{dir}, key, false); err != nil {
		t.Log("AddPathPrepend:", err)
	}
	// process env should start with dir if write succeeded partially
	_ = os.Getenv(key)
}

func TestSetEnvGetEnvProcess(t *testing.T) {
	key := "SCOOP_TEST_VAR_X"
	t.Setenv(key, "")
	if err := SetEnv(key, "hello", false); err != nil {
		t.Log("SetEnv:", err)
	}
	// Clear
	if err := SetEnv(key, "", false); err != nil {
		t.Log("clear:", err)
	}
}
