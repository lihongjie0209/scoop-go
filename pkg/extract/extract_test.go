package extract

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Test Helpers ---

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "scoop-extract-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func createTestZip(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte(content))
	}
	w.Close()
	return path
}

func createTestTar(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := tar.NewWriter(f)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := w.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	return path
}

func createTestTarGz(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	w := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
		w.WriteHeader(hdr)
		w.Write([]byte(content))
	}
	w.Close()
	gw.Close()
	return path
}

// --- Tests ---

func TestDetectExtractorByExtension(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"file.zip", "*extract.ZipExtractor"},
		{"file.7z", "*extract.SevenZipExtractor"},
		{"file.tar", "*extract.TarExtractor"},
		{"file.tar.gz", "*extract.TarGzipExtractor"},
		{"file.tgz", "*extract.TarGzipExtractor"},
		{"file.tar.xz", "*extract.TarXzExtractor"},
		{"file.txz", "*extract.TarXzExtractor"},
		{"file.tar.bz2", "*extract.TarBzip2Extractor"},
		{"file.tbz2", "*extract.TarBzip2Extractor"},
		{"file.gz", "*extract.GzipExtractor"},
		{"file.xz", "*extract.XzExtractor"},
		{"file.bz2", "*extract.Bzip2Extractor"},
		{"file.msi", "*extract.MsiExtractor"},
		{"file.exe", "*extract.ExeExtractor"},
		{"file.unknown", "*extract.UnknownExtractor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := DetectExtractor(tt.name)
			got := fmt.Sprintf("%T", e)
			if got != tt.want {
				t.Errorf("DetectExtractor(%q) = %s, want %s", tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectByMagicZip(t *testing.T) {
	dir := tempDir(t)
	path := createTestZip(t, dir, "test.zip", map[string]string{
		"hello.txt": "world",
	})

	e := DetectByMagic(path)
	if _, ok := e.(*ZipExtractor); !ok {
		t.Errorf("expected ZipExtractor, got %T", e)
	}
}

func TestDetectByMagicGzip(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "test.bin")
	f, _ := os.Create(path)
	// Write gzip magic bytes
	f.Write([]byte{0x1F, 0x8B, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0B,
		0x01, 0x00, 0x00, 0x00, 0xFF, 0xFF})
	f.Close()

	e := DetectByMagic(path)
	if _, ok := e.(*TarGzipExtractor); !ok {
		t.Errorf("expected TarGzipExtractor, got %T", e)
	}
}

func TestZipExtract(t *testing.T) {
	dir := tempDir(t)
	zipPath := createTestZip(t, dir, "app.zip", map[string]string{
		"bin/app.exe":    "binary content",
		"config/set.ini": "config",
	})

	dest := filepath.Join(dir, "out")
	ext := &ZipExtractor{}
	result, err := ext.Extract(&Config{Source: zipPath, Destination: dest})
	if err != nil {
		t.Fatalf("ZipExtract failed: %v", err)
	}
	if result.FilesExtracted != 2 {
		t.Errorf("expected 2 files extracted, got %d", result.FilesExtracted)
	}

	// Verify files
	data, err := os.ReadFile(filepath.Join(dest, "bin", "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary content" {
		t.Errorf("content = %q, want %q", string(data), "binary content")
	}
}

func TestZipExtractWithDir(t *testing.T) {
	dir := tempDir(t)
	zipPath := createTestZip(t, dir, "app.zip", map[string]string{
		"release-v1.2/bin/app.exe":    "binary",
		"release-v1.2/docs/readme.md": "readme",
		"other/file.txt":              "other",
	})

	dest := filepath.Join(dir, "out")
	ext := &ZipExtractor{}
	result, err := ext.Extract(&Config{
		Source:      zipPath,
		Destination: dest,
		ExtractDir:  "release-v1.2",
	})
	if err != nil {
		t.Fatalf("ZipExtract with dir failed: %v", err)
	}
	if result.FilesExtracted != 3 {
		t.Errorf("expected 3 entries, got %d", result.FilesExtracted)
	}

	// Check that only files under release-v1.2 are extracted
	if _, err := os.Stat(filepath.Join(dest, "bin", "app.exe")); os.IsNotExist(err) {
		t.Error("expected app.exe to exist under dest/bin")
	}
	if _, err := os.Stat(filepath.Join(dest, "other", "file.txt")); err == nil {
		t.Error("expected other/ to NOT be extracted")
	}
}

func TestTarExtract(t *testing.T) {
	dir := tempDir(t)
	tarPath := createTestTar(t, dir, "app.tar", map[string]string{
		"program.exe": "binary content",
		"config.ini":  "config",
	})

	dest := filepath.Join(dir, "out")
	ext := &TarExtractor{}
	result, err := ext.Extract(&Config{Source: tarPath, Destination: dest})
	if err != nil {
		t.Fatalf("TarExtract failed: %v", err)
	}
	if result.FilesExtracted != 2 {
		t.Errorf("expected 2 files extracted, got %d", result.FilesExtracted)
	}

	data, err := os.ReadFile(filepath.Join(dest, "program.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary content" {
		t.Errorf("content = %q, want %q", string(data), "binary content")
	}
}

func TestTarGzExtract(t *testing.T) {
	dir := tempDir(t)
	tgzPath := createTestTarGz(t, dir, "app.tar.gz", map[string]string{
		"hello.txt": "hello gz",
	})

	dest := filepath.Join(dir, "out")
	ext := &TarGzipExtractor{}
	result, err := ext.Extract(&Config{Source: tgzPath, Destination: dest})
	if err != nil {
		t.Fatalf("TarGzipExtract failed: %v", err)
	}
	if result.FilesExtracted != 1 {
		t.Errorf("expected 1 file, got %d", result.FilesExtracted)
	}
}

func TestTarRejectsSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "malicious.tar")
	outDir := filepath.Join(dir, "out")
	outside := filepath.Join(dir, "outside")

	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	if err := tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "escape/pwned.txt", Mode: 0644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("owned")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := (&TarExtractor{}).Extract(&Config{Source: tarPath, Destination: outDir}); err == nil {
		t.Fatal("expected archive symlink to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside destination: %v", err)
	}
}

func TestTarRejectsPreexistingSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(outDir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tarPath := filepath.Join(dir, "preexisting-link.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	if err := tw.WriteHeader(&tar.Header{Name: "escape/pwned.txt", Mode: 0644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("owned")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := (&TarExtractor{}).Extract(&Config{Source: tarPath, Destination: outDir}); err == nil {
		t.Fatal("expected write through preexisting symlink to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside destination: %v", err)
	}
}

func TestZipSlipProtection(t *testing.T) {
	dir := tempDir(t)
	// Create a zip with path traversal
	zipPath := createTestZip(t, dir, "malicious.zip", map[string]string{
		"../../../etc/passwd":                     "root:x:0:0:",
		"..\\..\\..\\windows\\system32\\evil.exe": "evil",
	})

	dest := filepath.Join(dir, "safe")
	ext := &ZipExtractor{}
	_, err := ext.Extract(&Config{Source: zipPath, Destination: dest})
	if err != nil {
		t.Fatalf("ZipExtract should not fail on malicious entries: %v", err)
	}

	// Verify no files escaped the dest directory
	entries, _ := os.ReadDir(dest)
	if len(entries) > 0 {
		t.Errorf("expected no files extracted from malicious zip, got %d", len(entries))
	}
}

func TestRemoveSource(t *testing.T) {
	dir := tempDir(t)
	zipPath := createTestZip(t, dir, "delete-me.zip", map[string]string{
		"file.txt": "content",
	})

	dest := filepath.Join(dir, "out")
	ext := &ZipExtractor{}
	_, err := ext.Extract(&Config{
		Source:      zipPath,
		Destination: dest,
		RemoveSrc:   true,
	})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Error("expected source file to be removed")
	}
}

func TestExtractSingleFileGzip(t *testing.T) {
	dir := tempDir(t)

	// Create a simple gzip file
	src := filepath.Join(dir, "data.bin.gz")
	f, _ := os.Create(src)
	gw := gzip.NewWriter(f)
	gw.Write([]byte("single file content"))
	gw.Close()
	f.Close()

	outDir := filepath.Join(dir, "out")
	os.MkdirAll(outDir, 0755)
	ext := &GzipExtractor{}
	_, err := ext.Extract(&Config{Source: src, Destination: outDir})
	if err != nil {
		t.Fatalf("GzipExtract failed: %v", err)
	}

	// Should create data.bin (without .gz)
	data, err := os.ReadFile(filepath.Join(outDir, "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "single file content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestUnknownExtractor(t *testing.T) {
	ext := &UnknownExtractor{}
	_, err := ext.Extract(&Config{Source: "file.xyz"})
	if err == nil {
		t.Error("expected error from UnknownExtractor")
	}
	if !strings.Contains(err.Error(), "unsupported archive format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExeExtractor(t *testing.T) {
	ext := &ExeExtractor{}
	_, err := ext.Extract(&Config{Source: "setup.exe"})
	if err == nil {
		t.Error("expected error from ExeExtractor (not an archive)")
	}
}

func TestIsWixInstaller(t *testing.T) {
	dir := tempDir(t)

	// Create a file with OLE2 magic bytes (D0CF11E0) — the WiX bundle signature.
	ole2Path := filepath.Join(dir, "wix_bundle.exe")
	f, _ := os.Create(ole2Path)
	f.Write([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0xC1, 0xD1})
	f.Close()

	// Create a file without OLE2 magic — a regular PE executable.
	pePath := filepath.Join(dir, "regular.exe")
	f2, _ := os.Create(pePath)
	f2.Write([]byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00})
	f2.Close()

	// IsWixInstaller checks both: (1) dark.exe on PATH, (2) OLE2 magic bytes.
	// When dark.exe IS available, OLE2 files should be detected.
	darkAvailable := func() bool {
		_, err := exec.LookPath("dark")
		return err == nil
	}()

	if darkAvailable {
		if !IsWixInstaller(ole2Path) {
			t.Error("IsWixInstaller(OLE2 file) = false, want true (dark.exe available)")
		}
		if IsWixInstaller(pePath) {
			t.Error("IsWixInstaller(PE file) = true, want false (no OLE2 magic)")
		}
	} else {
		if IsWixInstaller(ole2Path) {
			t.Error("IsWixInstaller(OLE2 file) = true, want false (dark.exe not available)")
		}
		if IsWixInstaller(pePath) {
			t.Error("IsWixInstaller(PE file) = true, want false)")
		}
	}
	// Non-existent file must not panic and must return false.
	if got := IsWixInstaller(filepath.Join(dir, "nonexistent.exe")); got {
		t.Error("IsWixInstaller(nonexistent) = true, want false")
	}
}

func TestWixExtractorFailsGracefully(t *testing.T) {
	// WixExtractor should return a clear error on failure (either dark.exe
	// not found, or the file is not a valid WiX bundle).
	dir := tempDir(t)
	dummyPath := filepath.Join(dir, "dummy.exe")
	os.WriteFile(dummyPath, []byte{0xD0, 0xCF, 0x11, 0xE0}, 0644)

	ext := &WixExtractor{}
	_, err := ext.Extract(&Config{Source: dummyPath, Destination: filepath.Join(dir, "out")})
	if err == nil {
		t.Fatal("expected error from WixExtractor, got nil")
	}
	// When dark.exe is not installed: "dark.exe not found"
	// When dark.exe is installed but file is not a valid bundle: "dark extraction failed"
	if !strings.Contains(err.Error(), "dark") {
		t.Errorf("expected error about dark, got: %v", err)
	}
}

func TestDetectRpm(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "package.rpm")
	f, _ := os.Create(path)
	f.Write([]byte{0xED, 0xAB, 0xEE, 0xDB, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Close()

	e := DetectByMagic(path)
	if _, ok := e.(*RpmExtractor); !ok {
		t.Errorf("expected RpmExtractor, got %T", e)
	}
}

func TestSanitizeZipEntry(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HyperSpec:", "HyperSpec_"},
		{"normal/pa??th/file.txt", "normal/pa??th/file.txt"},
		{"C:drive/file.txt", "C_drive/file.txt"},
		{"already_clean", "already_clean"},
		{"mixed:path:with:colons", "mixed_path_with_colons"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeZipEntry(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeZipEntry(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
