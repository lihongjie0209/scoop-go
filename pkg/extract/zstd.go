package extract

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ZstdExtractor extracts single .zst files (not tar archives).
// Mirrors Expand-ZstdArchive from lib/decompress.ps1.
type ZstdExtractor struct{}

func (e *ZstdExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("opening zstd reader: %w", err)
	}
	defer zr.Close()

	return extractSingleFile(cfg, zr, ".zst", func() {
		zr.Close()
		f.Close()
	})
}

// TarZstdExtractor extracts .tar.zst archives.
type TarZstdExtractor struct{}

func (e *TarZstdExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("opening zstd reader: %w", err)
	}
	defer zr.Close()

	return extractTarReader(cfg, zr, func() {
		zr.Close()
		f.Close()
	})
}

// zstdMagic is the 4-byte magic for zstd frames: 0xFD2FB528 (little-endian).
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// isTarZst returns true for .tar.zst / .tzst filenames.
func isTarZst(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar.zst") || strings.HasSuffix(name, ".tzst")
}

// detectZstdContent peeks into a zstd stream to decide whether it contains a
// tar archive (TarZstdExtractor) or a single file (ZstdExtractor).
func detectZstdContent(path string) Extractor {
	f, err := os.Open(path)
	if err != nil {
		return &ZstdExtractor{}
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return &ZstdExtractor{}
	}
	defer zr.Close()

	// Read enough for a tar header (512 bytes). The "ustar" magic sits at offset 257.
	buf := make([]byte, 512)
	n, _ := io.ReadFull(zr, buf)
	if n >= 262 && string(buf[257:262]) == "ustar" {
		return &TarZstdExtractor{}
	}
	return &ZstdExtractor{}
}
