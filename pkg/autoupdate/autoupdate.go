package autoupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// AutoUpdate checks for a new version, regenerates the manifest, refreshes
// hashes, and writes the updated manifest to disk.
func AutoUpdate(m *manifest.Manifest, appName, bucketPath string) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}

	newVersion, err := CheckVersion(m, appName)
	if err != nil {
		return err
	}
	if newVersion == m.Version {
		app.LogInfo("%s: already up to date", appName)
		return nil
	}

	data, err := manifest.GenerateUserManifest(m, newVersion)
	if err != nil {
		return fmt.Errorf("generating updated manifest: %w", err)
	}
	updated, err := manifest.Parse(data)
	if err != nil {
		return fmt.Errorf("parsing generated manifest: %w", err)
	}

	if len(updated.URL) > 0 {
		if err := UpdateHashes(updated, newVersion, ""); err != nil {
			return fmt.Errorf("updating top-level hashes: %w", err)
		}
	}
	for _, arch := range []string{"32bit", "64bit", "arm64"} {
		if len(updated.GetURL(arch)) == 0 || updated.GetArchContent(arch) == nil {
			continue
		}
		if err := UpdateHashes(updated, newVersion, arch); err != nil {
			return fmt.Errorf("updating %s hashes: %w", arch, err)
		}
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling updated manifest: %w", err)
	}

	target := filepath.Join(bucketPath, appName+".json")
	if err := os.WriteFile(target, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing updated manifest %s: %w", target, err)
	}

	app.LogInfo("%s: v%s -> v%s", appName, m.Version, newVersion)
	return nil
}
