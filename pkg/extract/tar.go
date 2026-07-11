package extract

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

// TarExtractor extracts .tar archives.
type TarExtractor struct{}

func (e *TarExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return extractTarReader(cfg, f, func() { f.Close() })
}

// TarGzipExtractor extracts .tar.gz / .tgz archives.
type TarGzipExtractor struct{}

func (e *TarGzipExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	return extractTarReader(cfg, gzr, func() {
		gzr.Close()
		f.Close()
	})
}

// TarXzExtractor extracts .tar.xz / .txz archives.
type TarXzExtractor struct{}

func (e *TarXzExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	xzr, err := xz.NewReader(f)
	if err != nil {
		return nil, err
	}

	return extractTarReader(cfg, xzr, func() {
		f.Close()
	})
}

// TarBzip2Extractor extracts .tar.bz2 / .tbz2 archives.
type TarBzip2Extractor struct{}

func (e *TarBzip2Extractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bz2r := bzip2.NewReader(f)
	return extractTarReader(cfg, bz2r, func() {
		f.Close()
	})
}

// GzipExtractor extracts single .gz files (not tar archives).
type GzipExtractor struct{}

func (e *GzipExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	return extractSingleFile(cfg, gzr, ".gz", func() {
		gzr.Close()
		f.Close()
	})
}

// XzExtractor extracts single .xz files (not tar archives).
type XzExtractor struct{}

func (e *XzExtractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	xzr, err := xz.NewReader(f)
	if err != nil {
		return nil, err
	}

	return extractSingleFile(cfg, xzr, ".xz", func() {
		f.Close()
	})
}

// Bzip2Extractor extracts single .bz2 files (not tar archives).
type Bzip2Extractor struct{}

func (e *Bzip2Extractor) Extract(cfg *Config) (*Result, error) {
	f, err := os.Open(cfg.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bz2r := bzip2.NewReader(f)
	return extractSingleFile(cfg, bz2r, ".bz2", func() {
		f.Close()
	})
}

// extractTarReader is the core tar extraction logic.
// closeFn is called before removing the source file (to release file handles on Windows).
func extractTarReader(cfg *Config, r io.Reader, closeFn func()) (*Result, error) {
	tr := tar.NewReader(r)
	dest := cfg.Destination

	if err := os.MkdirAll(dest, 0755); err != nil {
		return nil, err
	}

	count := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		relPath := filepath.Clean(header.Name)
		if strings.HasPrefix(relPath, "..") {
			continue
		}

		// Apply ExtractDir filter
		if cfg.ExtractDir != "" && !strings.HasPrefix(relPath, cfg.ExtractDir) {
			continue
		}
		relPath = strings.TrimPrefix(relPath, cfg.ExtractDir)

		target := filepath.Join(dest, relPath)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := rejectSymlinkParents(dest, target); err != nil {
				return nil, err
			}
			if err := ensureParentDir(target); err != nil {
				return nil, err
			}
			if err := rejectSymlinkParents(dest, target); err != nil {
				return nil, err
			}
			f, err := os.Create(target)
			if err != nil {
				return nil, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return nil, err
			}
			f.Close()
			count++
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("refusing archive link %q -> %q", header.Name, header.Linkname)
		}
	}

	if cfg.RemoveSrc {
		// Close the reader before removing the source to release file handles on Windows
		if closeFn != nil {
			closeFn()
		}
		os.Remove(cfg.Source)
	}

	return &Result{FilesExtracted: count}, nil
}

// rejectSymlinkParents prevents a regular file from being written through a
// link that existed before this archive was extracted.
func rejectSymlinkParents(dest, target string) error {
	dest = filepath.Clean(dest)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("archive target escapes destination: %q", target)
	}

	current := dest
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive target traverses symlink: %q", current)
		}
	}
	return nil
}

// extractSingleFile extracts a single compressed file (gz/xz/bz2) without tar.
// closeFn is called before removing the source file (to release file handles on Windows).
func extractSingleFile(cfg *Config, r io.Reader, ext string, closeFn func()) (*Result, error) {
	dest := cfg.Destination

	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		name := strings.TrimSuffix(filepath.Base(cfg.Source), ext)
		dest = filepath.Join(dest, name)
	}

	out, err := os.Create(dest)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	if _, err := io.Copy(out, r); err != nil {
		return nil, err
	}

	if cfg.RemoveSrc {
		// Close the reader before removing the source to release file handles on Windows
		if closeFn != nil {
			closeFn()
		}
		os.Remove(cfg.Source)
	}

	return &Result{FilesExtracted: 1}, nil
}
