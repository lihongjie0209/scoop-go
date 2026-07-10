package extract

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// MsiExtractor extracts .msi files using Windows' built-in msiexec
// (admin install mode: msiexec /a) or the optional lessmsi tool.
//
// MSI files use OLE Structured Storage; msiexec /a is the most reliable
// approach on Windows.
//
// Reference: lib/decompress.ps1 Expand-MsiArchive
type MsiExtractor struct {
	UseLessMSI bool
}

func (e *MsiExtractor) Extract(cfg *Config) (*Result, error) {
	dest := cfg.Destination
	if err := os.MkdirAll(dest, 0755); err != nil {
		return nil, err
	}

	// Apply ExtractDir: extract to temp, then move desired subdir
	var actualDest string
	if cfg.ExtractDir != "" {
		actualDest = dest
		dest = filepath.Join(dest, "_tmp")
		if err := os.MkdirAll(dest, 0755); err != nil {
			return nil, err
		}
	}

	var cmd *exec.Cmd
	if e.UseLessMSI {
		cmd = exec.Command("lessmsi", "x", cfg.Source, dest+string(filepath.Separator))
	} else {
		sourceDir := filepath.Join(dest, "SourceDir")
		if err := os.MkdirAll(sourceDir, 0755); err != nil {
			return nil, err
		}
		cmd = exec.Command("msiexec.exe", "/a", cfg.Source, "/qn",
			"TARGETDIR="+sourceDir+string(filepath.Separator))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("msi extraction failed: %w\nOutput: %s", err, string(output))
	}

	// Handle ExtractDir
	if cfg.ExtractDir != "" {
		srcDir := filepath.Join(dest, "SourceDir")
		srcExtract := filepath.Join(srcDir, cfg.ExtractDir)
		if _, err := os.Stat(srcExtract); err == nil {
			if err := moveDirContents(srcExtract, actualDest); err != nil {
				return nil, err
			}
		}
		os.RemoveAll(dest)
	} else {
		srcDir := filepath.Join(dest, "SourceDir")
		if _, err := os.Stat(srcDir); err == nil {
			if err := moveDirContents(srcDir, dest); err != nil {
				return nil, err
			}
			os.RemoveAll(srcDir)
		}
	}

	if cfg.RemoveSrc {
		os.Remove(cfg.Source)
	}

	return &Result{}, nil
}

// moveDirContents moves all contents of srcDir into destDir.
func moveDirContents(srcDir, destDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(destDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			// Cross-device: copy then delete
			if err := copyPath(src, dst); err != nil {
				return err
			}
			os.RemoveAll(src)
		}
	}
	return nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
