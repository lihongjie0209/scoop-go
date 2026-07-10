package extract

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// IsWixInstaller checks whether a file is a WiX bundle installer by verifying:
//   - dark.exe is available on PATH (from WiX Toolset)
//   - The file has OLE2 Compound Document magic bytes (D0CF11E0)
//
// Both conditions must be true for WiX extraction to be possible.
func IsWixInstaller(path string) bool {
	// dark.exe must be available on PATH
	if _, err := exec.LookPath("dark"); err != nil {
		return false
	}

	// Read the first 4 bytes to check for OLE2 magic (D0CF11E0)
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	magic := make([]byte, 4)
	if n, _ := f.Read(magic); n < 4 {
		return false
	}

	return magic[0] == 0xD0 && magic[1] == 0xCF && magic[2] == 0x11 && magic[3] == 0xE0
}

// ExeExtractor detects whether a .exe is an InnoSetup installer, WiX bundle,
// or standard NSIS installer, and delegates to the appropriate extractor.
// If none is detected or configured, it returns the file as-is (not an archive).
//
// Reference: lib/decompress.ps1 Expand-InnoArchive, Expand-DarkArchive
type ExeExtractor struct{}

func (e *ExeExtractor) Extract(cfg *Config) (*Result, error) {
	// The manifest's innosetup field determines the extractor.
	// Without it, we assume the .exe is a regular installer (not an archive).
	return nil, fmt.Errorf("not an archive: %s (use installer.file + installer.args instead)", filepath.Base(cfg.Source))
}

// InnoExtractor uses innounp for InnoSetup installers.
// Reference: lib/decompress.ps1 Expand-InnoArchive
type InnoExtractor struct{}

func (e *InnoExtractor) Extract(cfg *Config) (*Result, error) {
	if err := os.MkdirAll(cfg.Destination, 0755); err != nil {
		return nil, err
	}

	args := []string{"-x", "-d" + cfg.Destination, cfg.Source, "-y"}
	if cfg.ExtractDir != "" {
		args = append(args, "-c{app}\\"+cfg.ExtractDir)
	}

	cmd := exec.Command("innounp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("innounp extraction failed: %w\nOutput: %s", err, string(output))
	}

	if cfg.RemoveSrc {
		os.Remove(cfg.Source)
	}

	return &Result{}, nil
}

// WixExtractor uses dark (WiX Toolset) for WiX Bundle installers.
// Reference: lib/decompress.ps1 Expand-DarkArchive
type WixExtractor struct{}

func (e *WixExtractor) Extract(cfg *Config) (*Result, error) {
	// Check dark.exe availability before attempting extraction.
	if _, err := exec.LookPath("dark"); err != nil {
		return nil, fmt.Errorf("dark.exe not found: WiX extraction requires WiX Toolset (dark.exe) to be installed")
	}

	if err := os.MkdirAll(cfg.Destination, 0755); err != nil {
		return nil, err
	}

	args := []string{"-nologo", "-x", cfg.Destination, cfg.Source}
	cmd := exec.Command("dark", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dark extraction failed: %w\nOutput: %s", err, string(output))
	}

	// Post-extraction: promote attached container contents up one level.
	// dark extracts WiX bundles into a structure like:
	//   dest/AttachedContainer/app.msi
	//   dest/AttachedContainer/app.cab
	// We move these contents to the destination root for downstream use.
	for _, containerName := range []string{"AttachedContainer", "WixAttachedContainer"} {
		containerDir := filepath.Join(cfg.Destination, containerName)
		info, err := os.Stat(containerDir)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(containerDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			src := filepath.Join(containerDir, entry.Name())
			dst := filepath.Join(cfg.Destination, entry.Name())
			// If the destination already exists, skip rather than overwrite.
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			os.Rename(src, dst)
		}
		os.RemoveAll(containerDir)
	}

	if cfg.RemoveSrc {
		os.Remove(cfg.Source)
	}

	return &Result{}, nil
}

// RpmExtractor handles .rpm files (rare in Scoop, mostly for Cygwin/MSYS2).
type RpmExtractor struct{}

func (e *RpmExtractor) Extract(cfg *Config) (*Result, error) {
	return nil, fmt.Errorf("rpm extraction not yet implemented: %s", cfg.Source)
}

// IsoExtractor handles .iso files.
type IsoExtractor struct{}

func (e *IsoExtractor) Extract(cfg *Config) (*Result, error) {
	return nil, fmt.Errorf("iso extraction not yet implemented: %s", cfg.Source)
}

// UnknownExtractor is the fallback for unrecognized formats.
type UnknownExtractor struct{}

func (e *UnknownExtractor) Extract(cfg *Config) (*Result, error) {
	return nil, fmt.Errorf("unsupported archive format: %s", filepath.Ext(cfg.Source))
}
