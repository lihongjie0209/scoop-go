package extract

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ZipExtractor extracts .zip archives using Go's native archive/zip.
// Mirrors Expand-ZipArchive and Expand-7zipArchive for zip from lib/decompress.ps1.
type ZipExtractor struct{}

func (e *ZipExtractor) Extract(cfg *Config) (*Result, error) {
	r, err := zip.OpenReader(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}

	dest := cfg.Destination
	if err := os.MkdirAll(dest, 0755); err != nil {
		r.Close()
		return nil, err
	}

	count := 0
	for _, f := range r.File {
		if err := extractZipEntry(f, dest, cfg.ExtractDir); err != nil {
			r.Close()
			return nil, err
		}
		count++
	}

	// Close the reader BEFORE attempting to remove the source file,
	// otherwise Windows holds a lock on the file.
	r.Close()

	if cfg.RemoveSrc {
		if err := os.Remove(cfg.Source); err != nil {
			return &Result{FilesExtracted: count}, nil
		}
	}

	return &Result{FilesExtracted: count}, nil
}

func extractZipEntry(f *zip.File, dest, extractDir string) error {
	// Normalize path
	name := strings.ReplaceAll(f.Name, "\\", "/")

	// Apply extract_dir filter
	if extractDir != "" && !strings.HasPrefix(name, extractDir) {
		return nil // skip
	}
	relPath := strings.TrimPrefix(name, extractDir)
	relPath = strings.TrimPrefix(relPath, "/")

	target, err := cleanPath(dest, relPath)
	if err != nil || target == "" {
		return nil
	}

	// Prevent zip slip
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) {
		return nil
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}

	if err := ensureParentDir(target); err != nil {
		return err
	}

	rc, err := f.Open()
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
// sanitizeZipEntry replaces characters invalid in Windows filenames.
func sanitizeZipEntry(name string) string {
	name = strings.ReplaceAll(name, ":", "_")
	return name
}
