// Package db provides a SQLite-backed search cache for Scoop manifests.
// When use_sqlite_cache is enabled, `scoop search` uses FTS5 for fast queries.
// Mirrors lib/database.ps1 from the original Scoop.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/config"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// AppRow represents a row in the app cache table.
type AppRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Bucket      string `json:"bucket"`
	Manifest    string `json:"manifest"`
	Binary      string `json:"binary"`
	Shortcut    string `json:"shortcut"`
	Dependency  string `json:"dependency"`
	Suggest     string `json:"suggest"`
}

// Package-level connection singleton for connection pooling (DB-09).
var (
	globalDB   *sql.DB
	globalDBMu sync.Mutex
)

// dbPath returns the path to the SQLite database file.
func dbPath() string {
	return filepath.Join(app.Dirs().ScoopDir, "scoop.db")
}

// Open opens (or returns) the shared Scoop SQLite database connection,
// creating tables and indexes if needed. After the first call, the
// connection is reused for the lifetime of the process (DB-09).
func Open() (*sql.DB, error) {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()

	if globalDB != nil {
		return globalDB, nil
	}

	d, err := sql.Open("sqlite", dbPath())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
		d.Close()
		return nil, fmt.Errorf("enabling WAL: %w", err)
	}

	// Enable foreign keys
	if _, err := d.Exec("PRAGMA foreign_keys=ON"); err != nil {
		d.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	// Schema: main table, indexes, and FTS5 virtual table (DB-01, DB-13)
	schema := `
	CREATE TABLE IF NOT EXISTS app (
		name        TEXT NOT NULL COLLATE NOCASE,
		description TEXT NOT NULL DEFAULT '',
		version     TEXT NOT NULL,
		bucket      TEXT NOT NULL,
		manifest    TEXT NOT NULL DEFAULT '',
		binary      TEXT DEFAULT '',
		shortcut    TEXT DEFAULT '',
		dependency  TEXT DEFAULT '',
		suggest     TEXT DEFAULT '',
		PRIMARY KEY (name, version, bucket)
	);

	CREATE INDEX IF NOT EXISTS idx_app_name        ON app(name);
	CREATE INDEX IF NOT EXISTS idx_app_bucket      ON app(bucket);
	CREATE INDEX IF NOT EXISTS idx_app_binary      ON app(binary);
	CREATE INDEX IF NOT EXISTS idx_app_shortcut    ON app(shortcut);
	CREATE INDEX IF NOT EXISTS idx_app_description ON app(description);

	CREATE VIRTUAL TABLE IF NOT EXISTS app_fts USING fts5(
		name,
		binary,
		shortcut,
		description,
		tokenize='unicode61'
	);`

	if _, err := d.Exec(schema); err != nil {
		d.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	globalDB = d
	return globalDB, nil
}

// Close closes the shared database connection. It is safe to call
// multiple times; subsequent calls are no-ops.
func Close() error {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()

	if globalDB == nil {
		return nil
	}
	err := globalDB.Close()
	globalDB = nil
	return err
}

// Vacuum reclaims unused space in the database file (DB-17).
// Should be called after RebuildAll or large delete operations.
func Vacuum() error {
	db, err := Open()
	if err != nil {
		return err
	}
	_, err = db.Exec("VACUUM")
	return err
}

// RebuildAll rebuilds the entire cache from all local buckets (DB-02).
// Mirrors Set-ScoopDB from lib/database.ps1 L156-218.
func RebuildAll() error {
	logInfo("Rebuilding search cache...")

	db, err := Open()
	if err != nil {
		return err
	}

	// Use a transaction for the bulk operation
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing data
	if _, err := tx.Exec("DELETE FROM app"); err != nil {
		return fmt.Errorf("clearing app table: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM app_fts"); err != nil {
		return fmt.Errorf("clearing FTS table: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO app
		(name, description, version, bucket, manifest, binary, shortcut, dependency, suggest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Process all buckets
	buckets := bucket.ListLocal()
	count := 0
	for _, b := range buckets {
		manifestDir := bucket.ManifestDir(b.Name)

		entries, err := os.ReadDir(manifestDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			appName := strings.TrimSuffix(entry.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(manifestDir, entry.Name()))
			if err != nil {
				continue
			}

			m, err := manifest.Parse(data)
			if err != nil || m == nil {
				continue
			}

			row := extractRow(appName, b.Name, string(data), m)
			if _, err := stmt.Exec(row.Name, row.Description, row.Version,
				row.Bucket, row.Manifest, row.Binary, row.Shortcut,
				row.Dependency, row.Suggest); err != nil {
				continue
			}
			count++
		}
	}

	// Populate FTS5 virtual table from the app table (DB-01)
	if _, err := tx.Exec(`INSERT INTO app_fts(rowid, name, binary, shortcut, description)
		SELECT rowid, name, binary, shortcut, description FROM app`); err != nil {
		return fmt.Errorf("populating FTS index: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// VACUUM after rebuild to reclaim space (DB-17)
	if err := Vacuum(); err != nil {
		logInfo("Warning: VACUUM failed: %v", err)
	}

	logSuccess("Search cache rebuilt: %d apps indexed.", count)
	return nil
}

// Search performs an FTS5 search across name, binary, shortcut, and description
// columns, with a LIKE fallback for pattern matching. Results are deduplicated
// and limited to 100 rows (DB-01, DB-05, DB-10, DB-11).
// Mirrors Select-ScoopDBItem from lib/database.ps1 L238-271.
func Search(pattern string) ([]AppRow, error) {
	db, err := Open()
	if err != nil {
		return nil, err
	}

	if pattern == "" {
		pattern = "%"
	}

	// Try FTS5 first for simple queries (DB-01)
	if ftsQuery, ok := toFTS5Query(pattern); ok {
		results, err := searchFTS5(db, ftsQuery)
		if err == nil && len(results) > 0 {
			return results, nil
		}
	}

	// LIKE fallback for pattern matching (DB-05, DB-10, DB-11)
	likePattern := pattern
	if likePattern != "%" {
		likePattern = "%" + pattern + "%"
	}

	query := `SELECT DISTINCT
		name, description, version, bucket,
		binary, shortcut, dependency, suggest
		FROM app WHERE
		name LIKE ? OR binary LIKE ? OR shortcut LIKE ? OR description LIKE ?
		ORDER BY name ASC
		LIMIT 100`

	rows, err := db.Query(query, likePattern, likePattern, likePattern, likePattern)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []AppRow
	for rows.Next() {
		var r AppRow
		if err := rows.Scan(&r.Name, &r.Description, &r.Version, &r.Bucket,
			&r.Binary, &r.Shortcut, &r.Dependency, &r.Suggest); err != nil {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}

// searchFTS5 performs a search using the FTS5 virtual table (DB-01).
func searchFTS5(db *sql.DB, ftsQuery string) ([]AppRow, error) {
	query := `SELECT DISTINCT
		app.name, app.description, app.version, app.bucket,
		app.binary, app.shortcut, app.dependency, app.suggest
		FROM app
		JOIN app_fts ON app.rowid = app_fts.rowid
		WHERE app_fts MATCH ?
		ORDER BY app.name ASC
		LIMIT 100`

	rows, err := db.Query(query, ftsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AppRow
	for rows.Next() {
		var r AppRow
		if err := rows.Scan(&r.Name, &r.Description, &r.Version, &r.Bucket,
			&r.Binary, &r.Shortcut, &r.Dependency, &r.Suggest); err != nil {
			continue
		}
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// toFTS5Query converts a user search pattern into an FTS5 MATCH query.
// Returns the query and true if conversion succeeded, or ("", false) if
// the pattern should fall back to LIKE.
func toFTS5Query(pattern string) (string, bool) {
	q := strings.TrimSpace(pattern)
	if q == "" || q == "%" {
		return "", false
	}

	// If pattern contains SQL wildcards, fall back to LIKE
	if strings.ContainsAny(q, "%_") {
		return "", false
	}

	// Split into words and create an FTS5 prefix-match query
	words := strings.Fields(q)
	if len(words) == 0 {
		return "", false
	}

	var ftsParts []string
	for _, w := range words {
		// Strip non-alphanumeric characters that could confuse FTS5
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
				return r
			}
			return -1
		}, w)
		if clean != "" {
			ftsParts = append(ftsParts, clean+"*")
		}
	}

	if len(ftsParts) == 0 {
		return "", false
	}

	return strings.Join(ftsParts, " AND "), true
}

// GetByName retrieves a specific app by name from a bucket.
// Mirrors Get-ScoopDBItem from lib/database.ps1 L293-333.
func GetByName(name, bucketName, version string) (*AppRow, error) {
	db, err := Open()
	if err != nil {
		return nil, err
	}

	var r AppRow
	var query string
	var args []interface{}

	if version != "" {
		query = `SELECT * FROM app WHERE name = ? AND bucket = ? AND version = ?`
		args = []interface{}{name, bucketName, version}
	} else {
		query = `SELECT * FROM app WHERE name = ? AND bucket = ? ORDER BY version DESC LIMIT 1`
		args = []interface{}{name, bucketName}
	}

	row := db.QueryRow(query, args...)
	if err := row.Scan(&r.Name, &r.Description, &r.Version, &r.Bucket,
		&r.Manifest, &r.Binary, &r.Shortcut, &r.Dependency, &r.Suggest); err != nil {
		return nil, err
	}

	return &r, nil
}

// RemoveByBucket deletes all entries for a given bucket.
// Mirrors Remove-ScoopDBItem from lib/database.ps1 L351-390.
func RemoveByBucket(bucketName string) error {
	db, err := Open()
	if err != nil {
		return err
	}

	_, err = db.Exec("DELETE FROM app WHERE bucket = ?", bucketName)
	if err != nil {
		return err
	}

	// Also remove from FTS index
	_, err = db.Exec(`DELETE FROM app_fts WHERE rowid IN (
		SELECT rowid FROM app WHERE bucket = ?
	)`, bucketName)
	return err
}

// RemoveByBucketAndName deletes a specific app from a bucket.
func RemoveByBucketAndName(bucketName, name string) error {
	db, err := Open()
	if err != nil {
		return err
	}

	_, err = db.Exec("DELETE FROM app WHERE bucket = ? AND name = ?", bucketName, name)
	return err
}

// Insert inserts or updates a single app row.
// Mirrors Set-ScoopDBItem from lib/database.ps1 L100-138.
func Insert(row *AppRow) error {
	db, err := Open()
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert the app row
	_, err = tx.Exec(`INSERT OR REPLACE INTO app
		(name, description, version, bucket, manifest, binary, shortcut, dependency, suggest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Name, row.Description, row.Version, row.Bucket,
		row.Manifest, row.Binary, row.Shortcut, row.Dependency, row.Suggest)
	if err != nil {
		return err
	}

	// Sync the FTS index: delete old entry if any, then insert new
	_, err = tx.Exec(`DELETE FROM app_fts WHERE rowid IN (
		SELECT rowid FROM app WHERE name = ? AND bucket = ? AND version = ?
	)`, row.Name, row.Bucket, row.Version)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`INSERT INTO app_fts(rowid, name, binary, shortcut, description)
		SELECT rowid, name, binary, shortcut, description FROM app
		WHERE name = ? AND bucket = ? AND version = ?`,
		row.Name, row.Bucket, row.Version)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// IsEnabled checks if SQLite cache is configured.
func IsEnabled() bool {
	if app.Config() == nil {
		return false
	}
	return app.Config().Config().UseSQLiteCache
}

// extractRow builds a database row from a parsed manifest.
// Processes all architectures (64bit, 32bit, arm64) for binaries and
// shortcuts (DB-04).
func extractRow(name, bucketName, manifestJSON string, m *manifest.Manifest) *AppRow {
	row := &AppRow{
		Name:        name,
		Description: m.Description,
		Version:     m.Version,
		Bucket:      bucketName,
		Manifest:    manifestJSON,
	}

	// Extract bin names from all architectures (DB-04)
	archs := []string{"64bit", "32bit", "arm64"}
	seenBins := make(map[string]bool)
	var binNames []string
	for _, arch := range archs {
		bins := manifest.BinEntries(m.GetBin(arch))
		for _, b := range bins {
			if !seenBins[b[1]] {
				seenBins[b[1]] = true
				binNames = append(binNames, b[1])
			}
		}
	}
	// Also process top-level bin (when no architecture section)
	tbins := manifest.BinEntries(m.Bin)
	for _, b := range tbins {
		if !seenBins[b[1]] {
			seenBins[b[1]] = true
			binNames = append(binNames, b[1])
		}
	}
	row.Binary = strings.Join(binNames, " | ")

	// Extract shortcut names from all architectures (DB-04)
	seenSCs := make(map[string]bool)
	var scNames []string
	for _, arch := range archs {
		shortcuts := m.GetShortcuts(arch)
		for _, s := range shortcuts {
			if len(s) > 1 && !seenSCs[s[1]] {
				seenSCs[s[1]] = true
				scNames = append(scNames, s[1])
			}
		}
	}
	// Also process top-level shortcuts
	for _, s := range m.Shortcuts {
		if len(s) > 1 && !seenSCs[s[1]] {
			seenSCs[s[1]] = true
			scNames = append(scNames, s[1])
		}
	}
	row.Shortcut = strings.Join(scNames, " | ")

	// Dependencies
	if len(m.Depends) > 0 {
		row.Dependency = strings.Join(m.Depends, " | ")
	}

	// Suggestions
	if len(m.Suggest) > 0 {
		var parts []string
		for _, apps := range m.Suggest {
			parts = append(parts, strings.Join(apps, " | "))
		}
		row.Suggest = strings.Join(parts, " | ")
	}

	return row
}

// init registers the config change hook so that enabling use_sqlite_cache
// triggers a cache rebuild (DB-03).
func init() {
	config.SetConfigChangeHook(func(name, value string) {
		switch name {
		case "use_sqlite_cache":
			if value == "true" || value == "1" {
				// Can't proceed if app isn't initialized yet
				if app.Dirs() == nil {
					return
				}
				if _, err := Open(); err != nil {
					logInfo("Failed to open SQLite cache: %v", err)
					return
				}
				if err := RebuildAll(); err != nil {
					logInfo("Failed to rebuild SQLite cache: %v", err)
				}
			}
		}
	})
}

// Helper to write logs when app package isn't available.
func logInfo(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "INFO  %s\n", fmt.Sprintf(f, a...))
}

func logSuccess(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "DONE  %s\n", fmt.Sprintf(f, a...))
}
