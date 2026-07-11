# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

Scoop Go is a **pure Go rewrite** of [Scoop](https://scoop.sh) — the Windows package manager. It produces a single, self-contained binary that installs, updates, and manages Windows applications from the command line.

**Key design decisions:**
- Zero PowerShell runtime dependency (except for user-defined manifest hooks)
- Zero external tool dependencies replaced by Go-native implementations (git via `go-git`, 7z via `bodgit/sevenzip`, multi-connection download replaces Aria2)
- Embedded shim binary (`shim.exe`) built from `internal/shimbinary/main.go`
- SQLite search cache via `modernc.org/sqlite` (pure Go, no CGo)

## Build & Test Commands

```bash
# Build all packages
go build ./...

# Build the scoop binary
go build -ldflags="-X 'github.com/scoopinstaller/scoop-go/cmd/scoop/cmd.BuildDate=$(date +%Y-%m-%d)' -X 'github.com/scoopinstaller/scoop-go/cmd/scoop/cmd.Version=0.1.0'" -o scoop-go ./cmd/scoop/

# Run all tests
go test ./...

# Run tests for a specific package
go test ./pkg/manifest/ -v
go test ./pkg/shim/ -v
go test ./pkg/download/ -v

# Run a single test
go test ./pkg/manifest/ -v -run TestParseValidManifest

# Lint
go vet ./...

# Cross-compile shim binary (for Go developers modifying it)
GOOS=windows GOARCH=amd64 go build -o pkg/shim/shim.exe ./internal/shimbinary/

# Full rebuild (shim + main binary)
GOOS=windows GOARCH=amd64 go build -o pkg/shim/shim.exe ./internal/shimbinary/ && \
go build -ldflags="-X 'cmd/scoop/cmd.BuildDate=$(date +%Y-%m-%d)' -X 'cmd/scoop/cmd.Version=0.1.0'" -o scoop-go ./cmd/scoop/
```

## Architecture

### Command Layer (`cmd/scoop/cmd/`)

Each `.go` file is one command (e.g. `install.go`, `search.go`, `bucket.go`). Commands are registered via `init()` functions adding to `rootCmd`. The entry point is `cmd/scoop/main.go` → `cmd.Execute()`.

### Core Packages (`pkg/`)

```
pkg/app/         — Global state: config loading, directory paths, logging
pkg/config/      — ~34 config keys, JSON read/write, CompleteConfigChange side effects
pkg/manifest/    — Manifest struct, JSON parsing, architecture selection (ResolveArch)
pkg/bucket/      — Git bucket management (clone/pull/list/add/remove via go-git)
pkg/download/    — HTTP download engine: multi-connection, hash verify, cache, rate limit, resume, proxy
pkg/extract/     — Archive extraction: zip/tar/gz/xz/bz2/7z/msi/inno/wix (Go-native + external fallbacks)
pkg/install/     — Full install pipeline: download → extract → hooks → shim → shortcuts → PATH → persist
pkg/update/      — Scoop self-update, bucket sync, app update
pkg/shim/        — .exe/.bat/.ps1/.jar/.py shim creation + embedded shim.exe binary
pkg/env/         — PATH management, environment variables
pkg/shortcut/    — Start menu .lnk creation (Windows COM via PowerShell)
pkg/version/     — SemVer version comparison
pkg/dependency/  — Dependency resolution (topological sort with cycle detection)
pkg/db/          — SQLite search cache (modernc.org/sqlite)
pkg/status/      — App status checking (outdated/failed/hold/missing deps)
pkg/diagnostic/  — 8 health checks for scoop installation
pkg/gitutil/     — go-git wrapper (clone/fetch/pull/branch/log)
```

### Shim Binary (`internal/shimbinary/`)

A standalone Windows executable that reads `.shim` config files and forwards execution to the real binary. Compiled separately and embedded via `//go:embed` into the main scoop binary at `pkg/shim/embed.go`. Follows the [ScoopInstaller/Shim](https://github.com/ScoopInstaller/Shim) C# implementation 1:1.

### Key Data Flow: Install

```
scoop install git
  → cmd/install.go (parse flags)
  → install.FindManifest() (search local buckets / URL)
  → manifest.ResolveArch() (architecture selection)
  → dependency.Resolve() (topological sort + installation helpers)
  → install.Install():
      1. os.MkdirAll (version dir)
      2. download.Download (HTTP/multi-connection + hash verify + cache)
      3. extract.DetectExtractor + Extract (zip/tar/7z/etc.)
      4. runHooks (pre_install PowerShell)
      5. runInstaller (.exe/.ps1)
      6. linkCurrent (os.Symlink junction)
      7. createShims (shim.Create → .shim + shim.exe)
      8. createShortcuts (.lnk via PowerShell COM)
      9. envAddPath (PATH management)
      10. envSet (environment variables)
      11. persistData (junction/hardlink for configs)
      12. saveInstallInfo + saveManifest (install.json + manifest.json)
```

### Key Data Flow: Search (with SQLite cache)

```
scoop search git
  → db.IsEnabled()? (check use_sqlite_cache config)
  → YES → db.Search() (SQLite LIKE query on name/binary/shortcut)
  → NO → iterate all local buckets, scan .json files
```

## Important Patterns

- **Config access**: Always use `app.Config()` singleton, initialized in `PersistentPreRunE`.
- **Logging**: Use `app.LogInfo`, `app.LogWarn`, `app.LogError`, `app.LogSuccess`, `app.Debug`. Never use `fmt.Println` for user-facing output except for command results.
- **Windows-specific code**: Use `runtime.GOOS == "windows"` guards. Windows-only features (registry, junctions) degrade gracefully on other platforms.
- **Error handling**: Systems return errors that propagate up to the cobra command's `RunE`. Avoid `os.Exit` in packages (only in `main`).
- **Testing**: Test files follow `*_test.go` naming. Use `tempDir(t)` helper pattern for filesystem tests.

## Version & Release

- Version is set via `-ldflags` at build time
- Binaries are self-contained (single file, no external dependencies at runtime)
