package extract

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

// SevenZipExtractor extracts .7z archives using Go-native library.
// Also handles split archives (.001).
// Mirrors Expand-7zipArchive from lib/decompress.ps1.
type SevenZipExtractor struct{}

// ExternalSevenZipExtractor uses the official 7z CLI for formats supported by
// 7-Zip but not by the native 7z container reader (NSIS/SFX/PE and renamed
// archives such as Scoop's #/dl.7z convention).
type ExternalSevenZipExtractor struct{}

func (e *ExternalSevenZipExtractor) Extract(cfg *Config) (*Result, error) {
	dest := cfg.Destination
	extractDest := dest
	if cfg.ExtractDir != "" {
		extractDest = filepath.Join(dest, ".scoop-7z-extract")
	}
	if err := os.MkdirAll(extractDest, 0755); err != nil {
		return nil, err
	}
	cmd := exec.Command("7z", "x", "-y", "-o"+extractDest, cfg.Source)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("external 7z extraction failed: %w\nOutput: %s", err, string(output))
	}
	if cfg.ExtractDir != "" {
		sourceDir := filepath.Join(extractDest, filepath.FromSlash(strings.ReplaceAll(cfg.ExtractDir, "\\", "/")))
		if _, err := os.Stat(sourceDir); err != nil {
			return nil, fmt.Errorf("extract_dir %q not found in archive", cfg.ExtractDir)
		}
		if err := moveDirContents(sourceDir, dest); err != nil {
			return nil, err
		}
		_ = os.RemoveAll(extractDest)
	}
	if cfg.RemoveSrc {
		_ = os.Remove(cfg.Source)
	}
	return &Result{}, nil
}

func (e *SevenZipExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	sz, err := sevenzip.NewReader(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("reading 7z archive: %w", err)
	}

	dest := cfg.Destination
	if err := os.MkdirAll(dest, 0755); err != nil {
		return nil, err
	}

	count := 0
	for _, file := range sz.File {
		if err := extract7zEntry(file, dest, cfg.ExtractDir); err != nil {
			return nil, err
		}
		count++
	}

	if cfg.RemoveSrc {
		os.Remove(cfg.Source)
	}

	return &Result{FilesExtracted: count}, nil
}

func extract7zEntry(file *sevenzip.File, dest, extractDir string) error {
	name := strings.ReplaceAll(file.Name, "\\", "/")

	// Apply ExtractDir filter
	if extractDir != "" && !strings.HasPrefix(name, extractDir) {
		return nil
	}
	relPath := strings.TrimPrefix(name, extractDir)
	relPath = strings.TrimPrefix(relPath, "/")
	relPath = strings.TrimPrefix(relPath, "\\")

	target, err := cleanPath(dest, relPath)
	if err != nil || target == "" {
		return nil
	}

	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}

	if err := ensureParentDir(target); err != nil {
		return err
	}

	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
