package extract

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestIsTarredAndMatchMagic(t *testing.T) {
	if !isTarred("a.tar.gz") || !isTarred("a.tgz") || !isTarred("a.tar.xz") {
		t.Fatal("tarred")
	}
	if isTarred("a.gz") {
		t.Fatal("plain gz")
	}
	if !matchMagic([]byte{0x50, 0x4B, 0x03, 0x04, 0}, []byte{0x50, 0x4B, 0x03, 0x04}) {
		t.Fatal("magic match")
	}
	if matchMagic([]byte{1, 2}, []byte{1, 2, 3}) {
		t.Fatal("short data")
	}
}

func TestCleanPath(t *testing.T) {
	dest := t.TempDir()
	ok, err := cleanPath(dest, "sub/file.txt")
	if err != nil || ok == "" {
		t.Fatalf("ok path: %v %q", err, ok)
	}
	if skip, _ := cleanPath(dest, "../escape.txt"); skip != "" {
		t.Fatalf("traversal should skip, got %q", skip)
	}
	// Absolute-looking Windows paths like C:\... should not escape dest
	if skip, _ := cleanPath(dest, `..\..\Windows\System32\x`); skip != "" {
		t.Fatalf("parent escape should skip, got %q", skip)
	}
}

func TestDetectExtractorTypes(t *testing.T) {
	assertType := func(name string, check func(Extractor) bool) {
		t.Helper()
		ex := DetectExtractor(name)
		if !check(ex) {
			t.Errorf("%s -> %T", name, ex)
		}
	}
	assertType("a.7z", func(e Extractor) bool { _, ok := e.(*SevenZipExtractor); return ok })
	assertType("a.001", func(e Extractor) bool { _, ok := e.(*SevenZipExtractor); return ok })
	assertType("a.tar", func(e Extractor) bool { _, ok := e.(*TarExtractor); return ok })
	assertType("a.tar.xz", func(e Extractor) bool { _, ok := e.(*TarXzExtractor); return ok })
	assertType("a.tar.bz2", func(e Extractor) bool { _, ok := e.(*TarBzip2Extractor); return ok })
	assertType("a.msi", func(e Extractor) bool { _, ok := e.(*MsiExtractor); return ok })
	assertType("a.exe", func(e Extractor) bool { _, ok := e.(*ExeExtractor); return ok })
	assertType("a.rpm", func(e Extractor) bool { _, ok := e.(*RpmExtractor); return ok })
	assertType("a.iso", func(e Extractor) bool { _, ok := e.(*IsoExtractor); return ok })
	assertType("a.gz", func(e Extractor) bool { _, ok := e.(*GzipExtractor); return ok })
	assertType("a.xz", func(e Extractor) bool { _, ok := e.(*XzExtractor); return ok })
	assertType("a.bz2", func(e Extractor) bool { _, ok := e.(*Bzip2Extractor); return ok })
}

func TestGzipAndXzExtract(t *testing.T) {
	dir := t.TempDir()

	gzPath := filepath.Join(dir, "hello.gz")
	f, _ := os.Create(gzPath)
	w := gzip.NewWriter(f)
	_, _ = w.Write([]byte("hello-gz"))
	_ = w.Close()
	_ = f.Close()
	out := filepath.Join(dir, "out-gz")
	_ = os.MkdirAll(out, 0755)
	if _, err := (&GzipExtractor{}).Extract(&Config{Source: gzPath, Destination: out}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) == 0 {
		t.Fatal("no gzip output")
	}

	xzPath := filepath.Join(dir, "hello.xz")
	xf, _ := os.Create(xzPath)
	xw, err := xz.NewWriter(xf)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = xw.Write([]byte("hello-xz"))
	_ = xw.Close()
	_ = xf.Close()
	outX := filepath.Join(dir, "out-xz")
	_ = os.MkdirAll(outX, 0755)
	if _, err := (&XzExtractor{}).Extract(&Config{Source: xzPath, Destination: outX}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b", "c.txt")
	if err := ensureParentDir(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
}

func TestTarExtractWithExtractDir(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "a.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	_ = tw.WriteHeader(&tar.Header{Name: "nested/file.txt", Mode: 0644, Size: 4})
	_, _ = tw.Write([]byte("data"))
	_ = tw.WriteHeader(&tar.Header{Name: "other/x.txt", Mode: 0644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = f.Close()

	out := filepath.Join(dir, "out")
	_ = os.MkdirAll(out, 0755)
	if _, err := (&TarExtractor{}).Extract(&Config{
		Source: tarPath, Destination: out, ExtractDir: "nested",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "file.txt")); err != nil {
		if _, err2 := os.Stat(filepath.Join(out, "nested", "file.txt")); err2 != nil {
			t.Fatalf("extract_dir result missing: %v / %v", err, err2)
		}
	}
}

func TestUnknownExtractorErrors(t *testing.T) {
	if _, err := (&UnknownExtractor{}).Extract(&Config{Source: "x", Destination: t.TempDir()}); err == nil {
		t.Fatal("unknown should error")
	}
}
