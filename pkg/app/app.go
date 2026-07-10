// Package app holds the global Scoop application state: configuration, paths, and logging.
// It initializes once per CLI invocation and provides singleton access
// to the config manager and all Scoop directory paths.
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/scoopinstaller/scoop-go/pkg/config"
)

// Global app state
var (
	cfg    *config.Manager
	scoop  *ScoopDirs
	logger *ScoopLogger
)

// ScoopDirs holds all Scoop directory paths, mirroring core.ps1.
type ScoopDirs struct {
	ScoopDir    string // ~/scoop or $env:SCOOP
	GlobalDir   string // $env:ProgramData\scoop or $env:SCOOP_GLOBAL
	CacheDir    string // <scoopdir>\cache
	AppsDir     string // <scoopdir>\apps
	BucketsDir  string // <scoopdir>\buckets
	ShimsDir    string // <scoopdir>\shims
	PersistDir  string // <scoopdir>\persist
	ModulesDir  string // <scoopdir>\modules

	// Path environment variable name (for isolated path support)
	PathEnvVar string
}

// Initialize loads configuration and sets up directory paths.
// Must be called once at startup, typically from cobra's PersistentPreRun.
func Initialize(cfgPath string) error {
	// Load config
	cfg = config.NewManager(cfgPath)
	if err := cfg.Load(); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Determine directories
	scoopDir := resolveScoopDir(cfg)
	globalDir := resolveGlobalDir(cfg)
	cacheDir := resolveCacheDir(cfg, scoopDir)

	// Determine PATH env var name
	var pathEnvVar string
	switch v := cfg.Config().UseIsolatedPath.(type) {
	case string:
		pathEnvVar = strings.ToUpper(v)
	case bool:
		if v {
			pathEnvVar = "SCOOP_PATH"
		}
	default:
		pathEnvVar = "PATH"
	}
	if pathEnvVar == "" {
		pathEnvVar = "PATH"
	}

	scoop = &ScoopDirs{
		ScoopDir:   scoopDir,
		GlobalDir:  globalDir,
		CacheDir:   cacheDir,
		AppsDir:    filepath.Join(scoopDir, "apps"),
		BucketsDir: filepath.Join(scoopDir, "buckets"),
		ShimsDir:   filepath.Join(scoopDir, "shims"),
		PersistDir: filepath.Join(scoopDir, "persist"),
		ModulesDir: filepath.Join(scoopDir, "modules"),
		PathEnvVar: pathEnvVar,
	}

	// Initialize logger
	logger = NewLogger(os.Stdout, cfg.Config().Debug)

	// Main bucket auto-init is handled by cmd layer (avoids circular import)

	return nil
}

// Config returns the global config manager.
func Config() *config.Manager {
	return cfg
}

// Dirs returns the global Scoop directory paths.
func Dirs() *ScoopDirs {
	return scoop
}

// GetLogger returns the global logger.
func GetLogger() *ScoopLogger {
	return logger
}

// resolveScoopDir determines the base scoop directory.
func resolveScoopDir(cfg *config.Manager) string {
	if d := os.Getenv("SCOOP"); d != "" {
		return d
	}
	if cfg.Config().RootPath != "" {
		return cfg.Config().RootPath
	}
	return config.DefaultScoopDir()
}

// resolveGlobalDir determines the global scoop directory.
func resolveGlobalDir(cfg *config.Manager) string {
	if d := os.Getenv("SCOOP_GLOBAL"); d != "" {
		return d
	}
	if cfg.Config().GlobalPath != "" {
		return cfg.Config().GlobalPath
	}
	return config.DefaultGlobalDir()
}

// resolveCacheDir determines the cache directory.
func resolveCacheDir(cfg *config.Manager, scoopDir string) string {
	if d := os.Getenv("SCOOP_CACHE"); d != "" {
		return d
	}
	if cfg.Config().CachePath != "" {
		return cfg.Config().CachePath
	}
	return filepath.Join(scoopDir, "cache")
}

// AppDir returns the apps directory for the given scope.
func AppDir(global bool) string {
	if global {
		return filepath.Join(scoop.GlobalDir, "apps")
	}
	return scoop.AppsDir
}

// AppVersionDir returns the version-specific directory for an app.
func AppVersionDir(app, version string, global bool) string {
	return filepath.Join(AppDir(global), app, version)
}

// AppCurrentDir returns the 'current' symlink path for an app.
func AppCurrentDir(app string, global bool) string {
	return filepath.Join(AppDir(global), app, "current")
}

// PersistDir returns the persist directory for an app.
func PersistDir(app string, global bool) string {
	base := scoop.PersistDir
	if global {
		base = filepath.Join(scoop.GlobalDir, "persist")
	}
	return filepath.Join(base, app)
}

// ShimDir returns the shims directory for the given scope.
func ShimDir(global bool) string {
	if global {
		return filepath.Join(scoop.GlobalDir, "shims")
	}
	return scoop.ShimsDir
}

// CachePath returns the cache file path for an app/version/url combination.
func CachePath(app, version, url string) string {
	hash := shortHash(url)
	ext := filepath.Ext(url)
	filename := fmt.Sprintf("%s#%s#%s%s", app, version, hash, ext)
	return filepath.Join(scoop.CacheDir, filename)
}

// NeedsMainBucket returns true if no buckets exist yet (used by cmd layer).
func NeedsMainBucket() bool {
	if scoop == nil {
		return false
	}
	entries, err := os.ReadDir(scoop.BucketsDir)
	return err != nil || len(entries) == 0
}

func shortHash(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%08x", h)[:7]
}

// --- Logging ---

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelSuccess
)

type ScoopLogger struct {
	out   io.Writer
	debug bool
}

func NewLogger(out io.Writer, debug bool) *ScoopLogger {
	return &ScoopLogger{out: out, debug: debug}
}

func (l *ScoopLogger) Debug(format string, args ...interface{}) {
	if !l.debug {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.out, "%s %s\n", color.New(color.FgCyan).Sprint("DEBUG"), msg)
}

func (l *ScoopLogger) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.out, "%s %s\n", color.New(color.FgWhite, color.Faint).Sprint("INFO"), msg)
}

func (l *ScoopLogger) Warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.out, "%s %s\n", color.New(color.FgYellow).Sprint("WARN"), msg)
}

func (l *ScoopLogger) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.out, "%s %s\n", color.New(color.FgRed, color.Faint).Sprint("ERROR"), msg)
}

func (l *ScoopLogger) Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.out, "%s %s\n", color.New(color.FgGreen).Sprint("SUCCESS"), msg)
}

func (l *ScoopLogger) Print(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprint(l.out, msg)
}

func (l *ScoopLogger) Println(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(l.out, msg)
}

func (l *ScoopLogger) SetDebug(enabled bool) {
	l.debug = enabled
}

// IsDebug returns whether debug mode is enabled.
func (l *ScoopLogger) IsDebug() bool {
	return l.debug
}

// Package-level convenience functions using the global logger.

func LogDebug(format string, args ...interface{}) {
	if logger != nil {
		logger.Debug(format, args...)
	}
}

func LogInfo(format string, args ...interface{}) {
	if logger != nil {
		logger.Info(format, args...)
	}
}

func LogWarn(format string, args ...interface{}) {
	if logger != nil {
		logger.Warn(format, args...)
	}
}

func LogError(format string, args ...interface{}) {
	if logger != nil {
		logger.Error(format, args...)
	}
}

func LogSuccess(format string, args ...interface{}) {
	if logger != nil {
		logger.Success(format, args...)
	}
}

// Abort prints an error and exits with code 1.
func Abort(format string, args ...interface{}) {
	LogError(format, args...)
	os.Exit(1)
}
