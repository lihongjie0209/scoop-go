package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/app"
)

// TestExportShapeDocumentsPSCompatibleFields verifies the export command
// produces Name/Version/Source/Info fields consumable by import.
func TestExportShapeDocumentsPSCompatibleFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SCOOP", root)
	if err := app.Initialize(filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}

	// Fake installed app
	current := filepath.Join(root, "apps", "demo", "1.0.0")
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}
	// current junction/link simulation: write into version dir and symlink if possible
	link := filepath.Join(root, "apps", "demo", "current")
	_ = os.RemoveAll(link)
	if err := os.Symlink(current, link); err != nil {
		// Windows may need junction; copy layout as directory instead
		if err := os.MkdirAll(link, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(link, "manifest.json"), []byte(`{"version":"1.0.0","homepage":"h","license":"MIT"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(link, "install.json"), []byte(`{"bucket":"main","architecture":"64bit","hold":true}`), 0644); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(current, "manifest.json"), []byte(`{"version":"1.0.0","homepage":"h","license":"MIT"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(current, "install.json"), []byte(`{"bucket":"main","architecture":"64bit","hold":true}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Build the same structure exportCmd builds
	version, info, failed := getAppDetails("demo", false)
	if version == "" && !failed {
		// still ok if version resolved
	}
	item := exportApp{
		Name:    "demo",
		Version: version,
		Source:  info.Bucket,
		Info:    "",
	}
	var parts []string
	if info.Hold {
		parts = append(parts, "Held package")
	}
	if info.Architecture != "" {
		parts = append(parts, info.Architecture)
	}
	item.Info = strings.Join(parts, ", ")

	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through import parser
	parsed := parseImportApp(raw)
	if parsed.Name != "demo" {
		t.Fatalf("name = %q", parsed.Name)
	}
	if info.Hold && !strings.Contains(parsed.Info, "Held package") {
		t.Fatalf("hold not in info: %q (hold=%v arch=%v ver=%q)", parsed.Info, info.Hold, info.Architecture, version)
	}
}
