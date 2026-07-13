package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// StagingDirFor returns the sibling staging path for a final version directory.
// Example: .../apps/git/2.40.0 -> .../apps/git/2.40.0.scoop-staging
func StagingDirFor(versionDir string) string {
	return versionDir + ".scoop-staging"
}

// PrepareStaging removes any previous staging directory and creates a fresh one.
func PrepareStaging(versionDir string) (string, error) {
	stage := StagingDirFor(versionDir)
	if err := os.RemoveAll(stage); err != nil {
		return "", fmt.Errorf("clearing staging dir: %w", err)
	}
	if err := os.MkdirAll(stage, 0755); err != nil {
		return "", fmt.Errorf("creating staging dir: %w", err)
	}
	return stage, nil
}

// DiscardStaging removes a staging directory if present.
func DiscardStaging(stageDir string) error {
	if stageDir == "" {
		return nil
	}
	if err := os.RemoveAll(stageDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PromoteStaging renames stageDir to versionDir.
// Fails if versionDir already exists (use PromoteStagingForce to replace).
func PromoteStaging(stageDir, versionDir string) error {
	if _, err := os.Stat(versionDir); err == nil {
		return fmt.Errorf("version directory already exists: %s", versionDir)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(versionDir), 0755); err != nil {
		return err
	}
	if err := os.Rename(stageDir, versionDir); err != nil {
		return fmt.Errorf("promoting staging to version dir: %w", err)
	}
	return nil
}

// PromoteStagingForce replaces versionDir with stageDir contents.
// Uses rename-aside then promote so a failed rename can restore the old dir.
func PromoteStagingForce(stageDir, versionDir string) error {
	if _, err := os.Stat(versionDir); os.IsNotExist(err) {
		return PromoteStaging(stageDir, versionDir)
	} else if err != nil {
		return err
	}
	backup := versionDir + ".scoop-old"
	_ = os.RemoveAll(backup)
	if err := os.Rename(versionDir, backup); err != nil {
		return fmt.Errorf("moving existing version dir aside: %w", err)
	}
	if err := PromoteStaging(stageDir, versionDir); err != nil {
		// best-effort restore
		_ = os.RemoveAll(versionDir)
		_ = os.Rename(backup, versionDir)
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}
