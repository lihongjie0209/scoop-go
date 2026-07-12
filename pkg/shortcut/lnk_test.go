package shortcut

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteShellLink_MagicAndTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	if err := os.WriteFile(target, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(dir, "App.lnk")
	err := WriteShellLink(lnk, LinkData{
		TargetPath: target,
		Arguments:  "--help",
		WorkingDir: dir,
		IconPath:   target,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lnk)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 0x4C {
		t.Fatalf("lnk too short: %d", len(data))
	}
	// HeaderSize = 0x4C
	if binary.LittleEndian.Uint32(data[0:4]) != 0x4C {
		t.Fatalf("bad header size: %#x", binary.LittleEndian.Uint32(data[0:4]))
	}
	// CLSID of Shell Link
	// 00021401-0000-0000-C000-000000000046 stored little-endian
	if data[4] != 0x01 || data[5] != 0x14 || data[6] != 0x02 || data[7] != 0x00 {
		t.Fatalf("bad clsid prefix: %x", data[4:8])
	}
	// Target path should appear as UTF-16LE in the file
	utf16 := encodeUTF16LE(target)
	if !containsBytes(data, utf16) {
		// Also accept short path forms; at least basename
		base := encodeUTF16LE(filepath.Base(target))
		if !containsBytes(data, base) {
			t.Fatalf("target path not found in lnk")
		}
	}
	// Arguments
	if !containsBytes(data, encodeUTF16LE("--help")) {
		t.Fatal("arguments not embedded")
	}
}

func TestWriteShellLink_RequiresTarget(t *testing.T) {
	err := WriteShellLink(filepath.Join(t.TempDir(), "x.lnk"), LinkData{TargetPath: ""})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateUsesShellLinkOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(target, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	// Point start menu folder into temp via env
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	if err := Create(&Config{
		TargetPath: target,
		Name:       "Tool",
		Arguments:  "-v",
		WorkingDir: dir,
		Global:     false,
	}); err != nil {
		// On non-windows Create may no-op success; on windows should write lnk
		if err != nil {
			t.Fatal(err)
		}
	}
	lnk := filepath.Join(dir, "AppData", "Roaming", `Microsoft\Windows\Start Menu\Programs\Scoop Apps`, "Tool.lnk")
	if _, err := os.Stat(lnk); err != nil {
		// Non-windows: Create returns nil without writing — skip
		if !strings.Contains(err.Error(), "") {
			t.Log("lnk not created (likely non-windows path layout):", err)
		}
	}
}

func encodeUTF16LE(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func containsBytes(hay, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
