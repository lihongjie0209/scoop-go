package autoupdate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestCheckVersionGitHubLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/test-owner/test-repo/releases/latest" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.2.3"})
	}))
	defer server.Close()

	previous := githubAPIBaseURL
	githubAPIBaseURL = server.URL
	t.Cleanup(func() { githubAPIBaseURL = previous })

	m := &manifest.Manifest{
		Version:  "1.0.0",
		Homepage: "https://github.com/test-owner/test-repo",
		License:  "MIT",
		Checkver: "github",
	}

	version, err := CheckVersion(m, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want %q", version, "1.2.3")
	}
}

func TestCheckVersionHomepageRegex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>Latest release: 2.4.6</body></html>`)
	}))
	defer server.Close()

	m := &manifest.Manifest{
		Version:  "1.0.0",
		Homepage: server.URL,
		License:  "MIT",
		Checkver: `Latest release:\s*([0-9.]+)`,
	}

	version, err := CheckVersion(m, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.4.6" {
		t.Fatalf("version = %q, want %q", version, "2.4.6")
	}
}

func TestUpdateHashesTextFileMode(t *testing.T) {
	payload := []byte("autoupdate payload")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app.zip":
			_, _ = w.Write(payload)
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  app.zip\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := &manifest.Manifest{
		Version:  "1.0.0",
		Homepage: "https://example.com",
		License:  "MIT",
		URL:      manifest.FlexibleStrings{server.URL + "/app.zip"},
		Autoupdate: map[string]any{
			"hash": map[string]any{
				"url": server.URL + "/checksums.txt",
			},
		},
	}

	if err := UpdateHashes(m, "1.0.0", ""); err != nil {
		t.Fatal(err)
	}
	if len(m.Hash) != 1 || m.Hash[0] != sum {
		t.Fatalf("hashes = %#v, want %q", m.Hash, sum)
	}
}

func TestUpdateHashesSupportsVersionSubstitution(t *testing.T) {
	payload := []byte("substitution payload")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tool-2.0.0.zip":
			_, _ = w.Write(payload)
		case "/hashes/2.0.0.txt":
			_, _ = fmt.Fprintf(w, "%s  tool-2.0.0.zip\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := manifest.MustParse([]byte(fmt.Sprintf(`{
		"version":"1.0.0",
		"homepage":"https://example.com",
		"license":"MIT",
		"url":"%s/tool-current.zip",
		"autoupdate":{
			"url":"%s/tool-$version.zip",
			"hash":{"url":"%s/hashes/$version.txt"}
		}
	}`, server.URL, server.URL, server.URL)))

	data, err := manifest.GenerateUserManifest(source, "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	if err := UpdateHashes(generated, "2.0.0", ""); err != nil {
		t.Fatal(err)
	}
	if len(generated.Hash) != 1 || generated.Hash[0] != sum {
		t.Fatalf("hashes = %#v, want %q", generated.Hash, sum)
	}
}
