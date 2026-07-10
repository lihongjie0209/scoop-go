package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "scoop-shim-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestCreateExeShim(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "..", "real-app.exe")
	os.WriteFile(target, []byte("fake exe"), 0755)

	shimDir := filepath.Join(dir, "shims")
	err := Create(&Config{
		TargetPath: target,
		Name:       "myapp",
		ShimDir:    shimDir,
	})
	if err != nil {
		t.Fatalf("Create shim failed: %v", err)
	}

	// Check .shim file
	shimFile := filepath.Join(shimDir, "myapp.shim")
	if _, err := os.Stat(shimFile); os.IsNotExist(err) {
		t.Fatal("shim file not created")
	}

	data, _ := os.ReadFile(shimFile)
	if !strings.Contains(string(data), target) {
		t.Errorf("shim file should contain target path")
	}

	// Check that shim.exe binary was extracted (not a .cmd wrapper)
	shimExe := filepath.Join(shimDir, "myapp.exe")
	if _, err := os.Stat(shimExe); os.IsNotExist(err) {
		t.Fatal("shim.exe binary not created — expected embedded binary")
	}

	// Verify it's actually our embedded shim binary (has PE magic or enough bytes)
	exeData, _ := os.ReadFile(shimExe)
	if len(exeData) < 100 {
		t.Fatal("shim.exe too small to be a valid binary")
	}

	// .cmd wrapper should NOT exist for exe shims
	cmdFile := filepath.Join(shimDir, "myapp.cmd")
	if _, err := os.Stat(cmdFile); err == nil {
		t.Error("cmd wrapper should not exist for exe shims")
	}
}

func TestCreatePs1Shim(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "script.ps1")
	os.WriteFile(target, []byte("Write-Host hello"), 0755)

	shimDir := filepath.Join(dir, "shims")
	err := Create(&Config{
		TargetPath: target,
		Name:       "myscript",
		ShimDir:    shimDir,
	})
	if err != nil {
		t.Fatalf("Create ps1 shim failed: %v", err)
	}

	// Check .ps1 file
	ps1File := filepath.Join(shimDir, "myscript.ps1")
	if _, err := os.Stat(ps1File); os.IsNotExist(err) {
		t.Fatal("ps1 shim file not created")
	}

	// Check .cmd file
	cmdFile := filepath.Join(shimDir, "myscript.cmd")
	if _, err := os.Stat(cmdFile); os.IsNotExist(err) {
		t.Fatal("cmd shim file not created")
	}
}

func TestRemoveShim(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "app.exe")
	os.WriteFile(target, []byte("fake"), 0755)

	shimDir := filepath.Join(dir, "shims")
	Create(&Config{TargetPath: target, Name: "testapp", ShimDir: shimDir})

	// Verify created
	if _, err := os.Stat(filepath.Join(shimDir, "testapp.shim")); os.IsNotExist(err) {
		t.Fatal("shim should exist before removal")
	}

	// Remove
	err := Remove("testapp", shimDir, "testapp")
	if err != nil {
		t.Fatalf("Remove shim failed: %v", err)
	}

	// Verify removed
	entries, _ := os.ReadDir(shimDir)
	if len(entries) > 0 {
		t.Errorf("expected no files after removal, got %d", len(entries))
	}
}

func TestCreateBatShim(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "test.bat")
	os.WriteFile(target, []byte("@echo off"), 0755)

	shimDir := filepath.Join(dir, "shims")
	err := Create(&Config{
		TargetPath: target,
		Name:       "testbat",
		ShimDir:    shimDir,
	})
	if err != nil {
		t.Fatalf("Create bat shim failed: %v", err)
	}

	cmdFile := filepath.Join(shimDir, "testbat.cmd")
	if _, err := os.Stat(cmdFile); os.IsNotExist(err) {
		t.Fatal("cmd file not created for bat shim")
	}
}

func TestCreateJarShim(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "app.jar")
	os.WriteFile(target, []byte("fake jar"), 0755)

	shimDir := filepath.Join(dir, "shims")
	err := Create(&Config{
		TargetPath: target,
		Name:       "myjar",
		ShimDir:    shimDir,
	})
	if err != nil {
		t.Fatalf("Create jar shim failed: %v", err)
	}

	cmdFile := filepath.Join(shimDir, "myjar.cmd")
	data, _ := os.ReadFile(cmdFile)
	if !strings.Contains(string(data), "java -jar") {
		t.Errorf("jar shim should invoke java -jar, got: %s", string(data))
	}
}

func TestShimInfoParsing(t *testing.T) {
	// Test .shim file parsing via resolver
	dir := tempDir(t)
	shimFile := filepath.Join(dir, "test.shim")
	shimContent := `path = "C:\tools\app\bin\app.exe"
args = --verbose --config="C:\config.ini"
`
	if err := os.WriteFile(shimFile, []byte(shimContent), 0644); err != nil {
		t.Fatal(err)
	}

	target := ResolveShimTarget(shimFile)
	if target != `C:\tools\app\bin\app.exe` {
		t.Errorf("expected C:\\tools\\app\\bin\\app.exe, got %s", target)
	}
}

func TestShimWrapperTarget(t *testing.T) {
	dir := tempDir(t)

	// Test .cmd wrapper parsing
	cmdFile := filepath.Join(dir, "test.cmd")
	cmdContent := "@rem C:\\tools\\app\\bin\\tool.exe\n@echo off\n\"C:\\tools\\app\\bin\\tool.exe\" %*\n"
	if err := os.WriteFile(cmdFile, []byte(cmdContent), 0644); err != nil {
		t.Fatal(err)
	}

	target := ResolveWrapperTarget(cmdFile)
	expected := `C:\tools\app\bin\tool.exe`
	if target != expected {
		t.Errorf("expected %s, got %s", expected, target)
	}

	// Test .ps1 wrapper
	ps1File := filepath.Join(dir, "script.ps1")
	ps1Content := "# C:\\tools\\scripts\\run.ps1\n$path = \"C:\\tools\\scripts\\run.ps1\"\n& $path @args\n"
	if err := os.WriteFile(ps1File, []byte(ps1Content), 0644); err != nil {
		t.Fatal(err)
	}

	target = ResolveWrapperTarget(ps1File)
	ps1Expected := `C:\tools\scripts\run.ps1`
	if target != ps1Expected {
		t.Errorf("expected %s, got %s", ps1Expected, target)
	}
}

func TestShimBinaryIsEmbedded(t *testing.T) {
	// Verify that the embedded shim binary exists and is a valid size
	if len(ShimExe) == 0 {
		t.Fatal("ShimExe is empty — binary not embedded")
	}
	if len(ShimExe) < 50000 {
		t.Errorf("ShimExe seems too small: %d bytes (expected >= 50KB)", len(ShimExe))
	}
}

func TestParseShimLine(t *testing.T) {
	tests := []struct {
		line    string
		key     string
		value   string
		hasData bool
	}{
		{`path = "C:\app.exe"`, "path", `"C:\app.exe"`, true},
		{`args = --verbose`, "args", `--verbose`, true},
		{`elevate = true`, "elevate", `true`, true},
		{`# comment`, "", "", false},
		{`  `, "", "", false},
	}

	for _, tt := range tests {
		key, value := tryParseLine(tt.line)
		if tt.hasData {
			if key != tt.key || value != tt.value {
				t.Errorf("tryParseLine(%q) = (%q, %q), want (%q, %q)",
					tt.line, key, value, tt.key, tt.value)
			}
		} else {
			if key != "" || value != "" {
				t.Errorf("tryParseLine(%q) = (%q, %q), want empty", tt.line, key, value)
			}
		}
	}
}

// Helpers for test that match the shim binary's internal functions
func tryParseLine(rawLine string) (string, string) {
	line := strings.TrimRight(rawLine, " \r\n\t")
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' || strings.HasPrefix(trimmed, "//") {
		return "", ""
	}
	sepIdx := strings.Index(line, " = ")
	if sepIdx < 0 {
		return "", ""
	}
	key := strings.TrimSpace(line[:sepIdx])
	if key == "" {
		return "", ""
	}
	value := strings.TrimLeft(line[sepIdx+3:], " \t")
	return key, value
}
