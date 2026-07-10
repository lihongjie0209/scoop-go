// Package bucket manages Scoop buckets — Git repositories that contain app manifests.
// It mirrors lib/buckets.ps1 from the original PowerShell Scoop.
package bucket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/gitutil"
)

// Bucket represents a Scoop bucket.
type Bucket struct {
	Name      string `json:"Name"`
	Source    string `json:"Source,omitempty"`
	Updated   string `json:"Updated,omitempty"`
	Manifests int    `json:"Manifests,omitempty"`
}

// Known bucket repositories from buckets.json
type KnownBuckets map[string]string

func LoadKnownBuckets() (KnownBuckets, error) {
	known := KnownBuckets{
		"main":         "https://github.com/ScoopInstaller/Main",
		"extras":       "https://github.com/ScoopInstaller/Extras",
		"versions":     "https://github.com/ScoopInstaller/Versions",
		"nirsoft":      "https://github.com/ScoopInstaller/Nirsoft",
		"sysinternals": "https://github.com/niheaven/scoop-sysinternals",
		"php":          "https://github.com/ScoopInstaller/PHP",
		"nerd-fonts":   "https://github.com/matthewjberger/scoop-nerd-fonts",
		"nonportable":  "https://github.com/ScoopInstaller/Nonportable",
		"java":         "https://github.com/ScoopInstaller/Java",
		"games":        "https://github.com/Calinou/scoop-games",
	}
	data, err := os.ReadFile("buckets.json")
	if err == nil {
		var extra KnownBuckets
		if err := json.Unmarshal(data, &extra); err == nil {
			for k, v := range extra {
				known[k] = v
			}
		}
	}
	return known, nil
}

func Known() []string {
	known, err := LoadKnownBuckets()
	if err != nil {
		return nil
	}
	var names []string
	for name := range known {
		names = append(names, name)
	}
	return names
}

func Repo(name string) (string, bool) {
	known, err := LoadKnownBuckets()
	if err != nil {
		return "", false
	}
	repo, ok := known[name]
	return repo, ok
}

func Dir(name string) string {
	return filepath.Join(app.Dirs().BucketsDir, name)
}

func ManifestDir(name string) string {
	d := Dir(name)
	sub := filepath.Join(d, "bucket")
	if info, err := os.Stat(sub); err == nil && info.IsDir() {
		return sub
	}
	return d
}

func IsLocal(name string) bool {
	_, err := os.Stat(Dir(name))
	return err == nil
}

func ListLocal() []Bucket {
	entries, err := os.ReadDir(app.Dirs().BucketsDir)
	if err != nil {
		return nil
	}

	var buckets []Bucket
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		b := Bucket{Name: name}
		bucketDir := Dir(name)
		b.Manifests, _ = countManifests(ManifestDir(name))

		if gitutil.IsRepo(bucketDir) {
			b.Source, _ = gitutil.RemoteURL(bucketDir)
		}

		buckets = append(buckets, b)
	}
	return buckets
}

func Add(name, repoURL string) error {
	bucketsDir := app.Dirs().BucketsDir
	dest := Dir(name)

	if IsLocal(name) {
		return fmt.Errorf("the '%s' bucket already exists", name)
	}

	app.LogInfo("Checking repo...")
	if err := gitutil.LsRemote(repoURL); err != nil {
		return fmt.Errorf("'%s' doesn't look like a valid git repository", repoURL)
	}

	if err := os.MkdirAll(bucketsDir, 0755); err != nil {
		return fmt.Errorf("creating buckets directory: %w", err)
	}

	app.LogInfo("Cloning...")
	if err := gitutil.Clone(gitutil.CloneOptions{
		URL:   repoURL,
		Dest:  dest,
		Quiet: true,
		Depth: 1,
	}); err != nil {
		removeBucketDir(dest)
		return fmt.Errorf("failed to clone '%s': %w", repoURL, err)
	}

	app.LogSuccess("The '%s' bucket was added successfully.", name)
	return nil
}

func Remove(name string) error {
	dest := Dir(name)
	if !IsLocal(name) {
		return fmt.Errorf("'%s' bucket not found", name)
	}
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("removing bucket '%s': %w", name, err)
	}
	app.LogSuccess("The '%s' bucket was removed successfully.", name)
	return nil
}

func Sync(name string) error {
	bucketDir := Dir(name)
	if !gitutil.IsRepo(bucketDir) {
		return fmt.Errorf("'%s' is not a git repository", name)
	}

	// Get the remote URL before removing
	remoteURL, err := gitutil.RemoteURL(bucketDir)
	if err != nil {
		return fmt.Errorf("getting remote URL for '%s': %w", name, err)
	}

	// Re-clone: remove and re-fetch
	// For shallow-cloned buckets this is more reliable than trying to pull,
	// since go-git has known issues with shallow pull operations.
	app.LogDebug("Removing old '%s' bucket...", name)
	removeBucketDir(bucketDir)

	app.LogDebug("Re-cloning '%s' bucket...", name)
	if err := gitutil.Clone(gitutil.CloneOptions{
		URL:   remoteURL,
		Dest:  bucketDir,
		Quiet: true,
		Depth: 1,
	}); err != nil {
		return fmt.Errorf("re-cloning '%s': %w", name, err)
	}

	return nil
}

// removeBucketDir removes a bucket directory with retry logic to handle
// temporary file locks on Windows (e.g., from failed git clones).
func removeBucketDir(dest string) {
	// First attempt
	if err := os.RemoveAll(dest); err == nil {
		return
	}
	// Retry with delay to release file handles
	for range 3 {
		time.Sleep(100 * time.Millisecond)
		if err := os.RemoveAll(dest); err == nil {
			return
		}
	}
	// Final attempt - ignore errors, just try
	os.RemoveAll(dest)
}

func AppManifestPath(appName string) (bucket string, path string) {
	for _, b := range ListLocal() {
		p := filepath.Join(ManifestDir(b.Name), appName+".json")
		if _, err := os.Stat(p); err == nil {
			return b.Name, p
		}
		deprecatedPath := filepath.Join(Dir(b.Name), "deprecated", appName+".json")
		if _, err := os.Stat(deprecatedPath); err == nil {
			return b.Name, deprecatedPath
		}
	}
	return "", ""
}

func AppExistsInAnyBucket(appName string) bool {
	_, path := AppManifestPath(appName)
	return path != ""
}

func countManifests(dir string) (int, error) {
	count := 0
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			count++
		}
		return nil
	})
	return count, nil
}

func ConvertRepoURI(uri string) string {
	uri = strings.TrimSuffix(uri, ".git")
	s := strings.TrimPrefix(uri, "https://")
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimPrefix(s, "ssh://git@")
	if strings.Contains(s, ":") && !strings.Contains(s, "://") {
		s = strings.Replace(s, ":", "/", 1)
	}
	s = strings.TrimPrefix(s, "www.")
	parts := strings.Split(s, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:], "/")
	}
	return uri
}
