// Package status provides app status checking — which apps are installed,
// outdated, held, failed, deprecated, or missing dependencies.
// Mirrors app_status() in lib/core.ps1 and scoop-status.ps1.
package status

import (
	"encoding/json"
	"os"
	"fmt"
	"strings"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/gitutil"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/scoopinstaller/scoop-go/pkg/version"
)

// AppStatus holds the status of a single installed app.
type AppStatus struct {
	Name             string
	Version          string
	LatestVersion    string
	Global           bool
	Installed        bool
	Outdated         bool
	Failed           bool
	Hold             bool
	Deprecated       bool
	Removed          bool
	MissingDeps      []string
}

// ScoopStatus holds the overall scoop/bucket status.
type ScoopStatus struct {
	ScoopOutdated     bool
	BucketOutdated    bool
	NetworkFailure    bool
	NeedsUpdate       bool
	AppStatuses      []AppStatus
}

// CheckScoopUpdate checks if the scoop core repository has updates.
func CheckScoopUpdate() *ScoopStatus {
	s := &ScoopStatus{}

	currentDir := app.AppVersionDir("scoop", "current", false)
	if !gitutil.IsRepo(currentDir) {
		return s
	}

	if err := gitutil.Fetch(currentDir); err != nil {
		s.NetworkFailure = true
		return s
	}

	branch, err := gitutil.CurrentBranch(currentDir)
	if err != nil {
		return s
	}

	// Compare HEAD with origin/<branch>
	headHash, err := gitutil.HeadHash(currentDir)
	if err != nil {
		return s
	}

	// Fetch origin/<branch> ref
	targetRef := "refs/remotes/origin/" + branch
	commitsBehind, err := gitutil.CommitsBehindHead(currentDir, targetRef)
	if err != nil {
		return s
	}
	_ = headHash
	s.ScoopOutdated = commitsBehind > 0

	return s
}

// CheckBucketUpdates checks whether any local bucket has updates.
func CheckBucketUpdates() *ScoopStatus {
	s := &ScoopStatus{}

	for _, b := range bucket.ListLocal() {
		bucketDir := bucket.Dir(b.Name)
		if !gitutil.IsRepo(bucketDir) {
			continue
		}

		if err := gitutil.Fetch(bucketDir); err != nil {
			s.NetworkFailure = true
			continue
		}

		branch, err := gitutil.CurrentBranch(bucketDir)
		if err != nil {
			continue
		}

		targetRef := "refs/remotes/origin/" + branch
		commitsBehind, err := gitutil.CommitsBehindHead(bucketDir, targetRef)
		if err != nil {
			continue
		}
		if commitsBehind > 0 {
			s.BucketOutdated = true
			break
		}
	}

	return s
}

// CheckAppStatuses checks the status of all installed apps.
func CheckAppStatuses() []AppStatus {
	var statuses []AppStatus

	// Check both local and global installs
	for _, global := range []bool{false, true} {
		appsDir := app.AppDir(global)
		entries, err := os.ReadDir(appsDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "scoop" {
				continue
			}

			appName := entry.Name()
			s := checkSingleAppStatus(appName, global)
			statuses = append(statuses, s)
		}
	}

	return statuses
}

// checkSingleAppStatus mirrors app_status() from lib/core.ps1 L546-L588.
func checkSingleAppStatus(appName string, global bool) AppStatus {
	s := AppStatus{
		Name:      appName,
		Global:    global,
		Installed: false,
	}

	// Find current version
	versionDir, err := findCurrentVersion(appName, global)
	if err != nil {
		s.Failed = true
		return s
	}

	s.Version = filepath.Base(versionDir)
	s.Installed = true

	// Read install.json
	installInfo := readInstallInfo(versionDir)
	s.Hold = installInfo.Hold
	bucketName := installInfo.Bucket

	// When "current" is a real directory (not a junction), resolve version from manifest.
	if s.Version == "current" {
		if data, err := os.ReadFile(filepath.Join(versionDir, "manifest.json")); err == nil {
			if m, err := manifest.Parse(data); err == nil && m.Version != "" {
				s.Version = m.Version
			}
		}
	}

	// Check deprecated
	deprecatedPath := filepath.Join(bucket.Dir(bucketName), "deprecated", appName+".json")
	if _, err := os.Stat(deprecatedPath); err == nil {
		s.Deprecated = true
	}

	// Get latest manifest
	var m *manifest.Manifest
	if bucketName != "" {
		_, mp := bucket.AppManifestPath(appName)
		if mp != "" {
			m, _ = manifest.ParseFile(mp)
		}
	}

	if m == nil {
		// Try in deprecated
		if bucketName != "" {
			dp := filepath.Join(bucket.Dir(bucketName), "deprecated", appName+".json")
			m, _ = manifest.ParseFile(dp)
		}
		if m == nil {
			s.Removed = true
		}
	}

	if m != nil {
		s.LatestVersion = m.Version

		// Check outdated
		if s.Version != "" && s.LatestVersion != "" {
			if version.Compare(s.Version, s.LatestVersion) > 0 {
				s.Outdated = true
			}
		}

		// Check missing deps (bucket/app -> app name)
		for _, dep := range m.Depends {
			depApp := depAppName(dep)
			if !isAppInstalled(depApp) {
				s.MissingDeps = append(s.MissingDeps, dep)
			}
		}
	}

	return s
}

// findCurrentVersion finds the current version directory for an app.
func findCurrentVersion(appName string, global bool) (string, error) {
	currentPath := app.AppCurrentDir(appName, global)

	// Check for junction/symlink
	if target, err := os.Readlink(currentPath); err == nil {
		return target, nil
	}

	// Check for manifest.json in current
	manifestPath := filepath.Join(currentPath, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return currentPath, nil
	}

	// Check if current dir exists as a regular directory
	if info, err := os.Stat(currentPath); err == nil && info.IsDir() {
		return currentPath, nil
	}

	// Fallback: find latest version directory
	appPath := app.AppDir(global)
	appDir := filepath.Join(appPath, appName)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return "", err
	}

	var latest string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "current" && !strings.HasPrefix(e.Name(), "_") {
			latest = e.Name()
		}
	}

	if latest != "" {
		return filepath.Join(appDir, latest), nil
	}

	return "", fmt.Errorf("no version found")
}

// isAppInstalled checks if an app is installed in either scope.
func isAppInstalled(appName string) bool {
	for _, global := range []bool{false, true} {
		currentPath := app.AppCurrentDir(appName, global)
		if _, err := os.Stat(currentPath); err == nil {
			return true
		}
	}
	return false
}

// InstallInfo mirrors the install.json format.
type InstallInfo struct {
	Architecture string `json:"architecture,omitempty"`
	URL          string `json:"url,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Hold         bool   `json:"hold,omitempty"`
}

func readInstallInfo(versionDir string) InstallInfo {
	var info InstallInfo
	path := filepath.Join(versionDir, "install.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return info
	}
	json.Unmarshal(data, &info)
	return info
}

// depAppName extracts the app name from "bucket/app" or a plain name.
func depAppName(dep string) string {
	dep = strings.TrimSpace(dep)
	dep = strings.TrimSuffix(dep, ".json")
	if i := strings.LastIndex(dep, "/"); i >= 0 {
		return dep[i+1:]
	}
	return dep
}
