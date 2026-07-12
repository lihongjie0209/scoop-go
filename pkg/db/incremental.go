package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// Removal identifies an app row to delete from the cache.
type Removal struct {
	Bucket string
	Name   string
}

// ChangeSet is an incremental cache update (mirrors PS Set-ScoopDB + Remove-ScoopDBItem).
type ChangeSet struct {
	UpsertPaths []string
	Removals    []Removal
}

// NameStatusChange is one line from `git diff --name-status`.
type NameStatusChange struct {
	Status  string // A, M, D, R, ...
	Path    string
	OldPath string // set for renames
}

// BucketAndAppFromPath extracts bucket and app name from a manifest filesystem path.
func BucketAndAppFromPath(path string) (bucketName, appName string, ok bool) {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	// find "buckets" segment
	bi := -1
	for i, p := range parts {
		if p == "buckets" {
			bi = i
			break
		}
	}
	if bi < 0 || bi+1 >= len(parts) {
		return "", "", false
	}
	bucketName = parts[bi+1]
	base := parts[len(parts)-1]
	if !strings.HasSuffix(base, ".json") {
		return "", "", false
	}
	appName = strings.TrimSuffix(base, ".json")
	if bucketName == "" || appName == "" {
		return "", "", false
	}
	return bucketName, appName, true
}

// ParseNameStatus parses `git diff --name-status` output.
func ParseNameStatus(output string) []NameStatusChange {
	var out []NameStatusChange
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		// R100, C90 etc.
		if len(status) > 0 && (status[0] == 'R' || status[0] == 'C') {
			status = string(status[0])
			if len(fields) >= 3 {
				out = append(out, NameStatusChange{Status: status, OldPath: fields[1], Path: fields[2]})
				continue
			}
		}
		out = append(out, NameStatusChange{Status: string(status[0]), Path: fields[1]})
	}
	return out
}

// ChangeSetFromNameStatus builds a ChangeSet for one bucket from git name-status changes.
// bucketRoot is the bucket repo root (contains bucket/ or manifests).
func ChangeSetFromNameStatus(bucketRoot, bucketName string, changes []NameStatusChange) ChangeSet {
	var cs ChangeSet
	for _, c := range changes {
		switch c.Status {
		case "A", "M":
			if !strings.HasSuffix(strings.ToLower(c.Path), ".json") {
				continue
			}
			full := filepath.Join(bucketRoot, filepath.FromSlash(c.Path))
			if _, err := os.Stat(full); err == nil {
				cs.UpsertPaths = append(cs.UpsertPaths, full)
			}
		case "D":
			if !strings.HasSuffix(strings.ToLower(c.Path), ".json") {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(c.Path), ".json")
			cs.Removals = append(cs.Removals, Removal{Bucket: bucketName, Name: name})
		case "R":
			if c.OldPath != "" && strings.HasSuffix(strings.ToLower(c.OldPath), ".json") {
				oldName := strings.TrimSuffix(filepath.Base(c.OldPath), ".json")
				cs.Removals = append(cs.Removals, Removal{Bucket: bucketName, Name: oldName})
			}
			if strings.HasSuffix(strings.ToLower(c.Path), ".json") {
				full := filepath.Join(bucketRoot, filepath.FromSlash(c.Path))
				if _, err := os.Stat(full); err == nil {
					cs.UpsertPaths = append(cs.UpsertPaths, full)
				}
			}
		}
	}
	return cs
}

// ApplyChanges applies incremental upserts and removals to the SQLite cache.
func ApplyChanges(cs ChangeSet) error {
	for _, rem := range cs.Removals {
		if err := RemoveByBucketAndName(rem.Bucket, rem.Name); err != nil {
			return fmt.Errorf("removing %s/%s: %w", rem.Bucket, rem.Name, err)
		}
		// FTS cleanup for removed rows
		if err := purgeFTSOrphans(); err != nil {
			return err
		}
	}
	for _, path := range cs.UpsertPaths {
		if err := UpsertManifestFile(path); err != nil {
			return fmt.Errorf("upsert %s: %w", path, err)
		}
	}
	return nil
}

// UpsertManifestFile indexes a single manifest JSON file into the cache.
func UpsertManifestFile(path string) error {
	bucketName, appName, ok := BucketAndAppFromPath(path)
	if !ok {
		return fmt.Errorf("cannot determine bucket/app from %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return err
	}
	row := extractRow(appName, bucketName, string(data), m)
	// Remove any previous versions for same name+bucket to avoid stale rows
	_ = RemoveByBucketAndName(bucketName, appName)
	return Insert(row)
}

func purgeFTSOrphans() error {
	d, err := Open()
	if err != nil {
		return err
	}
	// Rebuild FTS from app table is expensive; delete FTS rows not in app.
	// modernc fts5: delete by rowid not in app
	_, err = d.Exec(`DELETE FROM app_fts WHERE rowid NOT IN (SELECT rowid FROM app)`)
	return err
}
