// Package extract provides archive extraction with Go-native implementations.
// Supports: .zip, .tar, .tar.gz, .tar.xz, .tar.bz2, .7z, .gz, .xz, .bz2
// External fallbacks via os/exec: .msi (msiexec), InnoSetup (innounp), WiX (dark)
//
// Detection uses both file extension AND magic bytes for reliability.
// Mirrors lib/decompress.ps1 from the original Scoop.
package extract

import (
	"os"
	"path/filepath"
	"strings"
)

// Config holds extraction configuration.
type Config struct {
	Source      string
	Destination string
	ExtractDir  string // subdirectory within archive to extract
	ExtractTo   string // subdirectory within Destination to extract to
	RemoveSrc   bool   // remove source after extraction
}

// Result holds extraction result info.
type Result struct {
	FilesExtracted int
}

// Extractor defines the interface for archive extraction.
type Extractor interface {
	Extract(cfg *Config) (*Result, error)
}

// DetectExtractor selects the right extractor based on file extension first,
// then falls back to magic bytes for ambiguous cases.
func DetectExtractor(source string) Extractor {
	source = strings.ToLower(source)

	// Extension-based detection (fast path)
	switch {
	case strings.HasSuffix(source, ".zip"):
		return &ZipExtractor{}
	case strings.HasSuffix(source, ".7z") || strings.HasSuffix(source, ".001"):
		return &SevenZipExtractor{}
	case strings.HasSuffix(source, ".tar"):
		return &TarExtractor{}
	case strings.HasSuffix(source, ".tar.gz") || strings.HasSuffix(source, ".tgz"):
		return &TarGzipExtractor{}
	case strings.HasSuffix(source, ".tar.xz") || strings.HasSuffix(source, ".txz"):
		return &TarXzExtractor{}
	case strings.HasSuffix(source, ".tar.bz2") || strings.HasSuffix(source, ".tbz2"):
		return &TarBzip2Extractor{}
	case strings.HasSuffix(source, ".gz") && !isTarred(source):
		return &GzipExtractor{}
	case strings.HasSuffix(source, ".xz") && !isTarred(source):
		return &XzExtractor{}
	case strings.HasSuffix(source, ".bz2") && !isTarred(source):
		return &Bzip2Extractor{}
	case strings.HasSuffix(source, ".msi"):
		return &MsiExtractor{}
	case strings.HasSuffix(source, ".exe"):
		return &ExeExtractor{}
	case strings.HasSuffix(source, ".rpm"):
		return &RpmExtractor{}
	case strings.HasSuffix(source, ".iso"):
		return &IsoExtractor{}
	default:
		// Fallback: try magic byte detection
		return DetectByMagic(source)
	}
}

// DetectByMagic reads magic bytes to determine archive format.
func DetectByMagic(source string) Extractor {
	f, err := os.Open(source)
	if err != nil {
		return &UnknownExtractor{}
	}
	defer f.Close()

	magic := make([]byte, 16)
	if n, _ := f.Read(magic); n < 16 {
		return &UnknownExtractor{}
	}

	// Magic byte patterns
	switch {
	case matchMagic(magic, []byte{0x50, 0x4B, 0x03, 0x04}):
		return &ZipExtractor{} // PK\x03\x04
	case matchMagic(magic, []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}):
		return &SevenZipExtractor{} // 7z\xBC\xAF\x27\x1C
	case magic[0] == 0x1F && magic[1] == 0x8B:
		return &TarGzipExtractor{} // gzip
	case matchMagic(magic, []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}):
		return &TarXzExtractor{} // \xFD7zXZ\x00
	case magic[0] == 0x42 && magic[1] == 0x5A:
		return &TarBzip2Extractor{} // BZ
	case matchMagic(magic, []byte{0x75, 0x73, 0x74, 0x61, 0x72}):
		return &TarExtractor{} // ustar (tar)
	case matchMagic(magic, []byte{0x4D, 0x53, 0x43, 0x46}):
		return &MsiExtractor{} // MSCF (MS OLE)
	case matchMagic(magic, []byte{0xD0, 0xCF, 0x11, 0xE0}):
		return &MsiExtractor{} // D0CF11E0 (OLE2)
	case matchMagic(magic, []byte{0x4D, 0x5A}):
		return &ExeExtractor{} // MZ (PE executable)
	case matchMagic(magic, []byte{0xED, 0xAB, 0xEE, 0xDB}):
		return &RpmExtractor{} // rpm
	case matchMagic(magic, []byte{0x43, 0x44, 0x30, 0x30, 0x31}):
		return &IsoExtractor{} // CD001 (ISO)
	}

	return &UnknownExtractor{}
}

// --- Helpers ---

func matchMagic(data, magic []byte) bool {
	if len(data) < len(magic) {
		return false
	}
	for i, b := range magic {
		if data[i] != b {
			return false
		}
	}
	return true
}

func isTarred(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar.gz") ||
		strings.HasSuffix(name, ".tar.xz") ||
		strings.HasSuffix(name, ".tar.bz2") ||
		strings.HasSuffix(name, ".tgz") ||
		strings.HasSuffix(name, ".txz") ||
		strings.HasSuffix(name, ".tbz2")
}

// cleanPath sanitizes a path to prevent path traversal.
func cleanPath(dest, entry string) (string, error) {
	cleaned := filepath.Clean(entry)
	if strings.HasPrefix(cleaned, "..") || strings.HasPrefix(cleaned, "/") {
		return "", nil // skip
	}
	target := filepath.Join(dest, cleaned)
	// Ensure target is inside dest
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) &&
		filepath.Clean(target) != filepath.Clean(dest) {
		return "", nil
	}
	return target, nil
}

// ensureParentDir creates parent directories for a file.
func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}
