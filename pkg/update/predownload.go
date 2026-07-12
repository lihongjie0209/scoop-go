package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/download"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// PrefetchApp downloads an app's URLs into the Scoop cache (and verifies hashes)
// without installing. Used by update to ensure the new version is fetchable
// before tearing down the old installation (mirrors scoop-update.ps1 pre-download).
func PrefetchApp(ctx context.Context, appName string, m *manifest.Manifest, arch string, useCache, checkHash bool) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	resolved := m.ResolveArch(arch)
	if resolved == "" {
		return fmt.Errorf("'%s' doesn't support architecture %s", appName, arch)
	}
	urls := m.GetURL(resolved)
	if len(urls) == 0 {
		return fmt.Errorf("no URLs defined for '%s'", appName)
	}
	if err := os.MkdirAll(app.Dirs().CacheDir, 0755); err != nil {
		return err
	}

	var githubToken, proxy string
	if cfg := app.Config(); cfg != nil {
		githubToken = cfg.Config().GH_TOKEN
		proxy = cfg.Config().Proxy
	}

	for _, u := range urls {
		expectedHash := ""
		if checkHash {
			expectedHash = m.HashForURL(u, resolved)
		}
		cacheKey := fmt.Sprintf("%s#%s#%s", appName, m.Version, shortHash(u))
		dest := filepath.Join(app.Dirs().CacheDir, cacheKey)
		dl := download.NewDownloader(&download.Config{
			URL:          u,
			Destination:  dest,
			CacheDir:     app.Dirs().CacheDir,
			CacheKey:     cacheKey,
			UseCache:     useCache,
			Cookies:      m.Cookie,
			ExpectedHash: expectedHash,
			GithubToken:  githubToken,
			Proxy:        proxy,
		})
		if _, err := dl.Download(ctx); err != nil {
			return fmt.Errorf("prefetch %s: %w", u, err)
		}
		app.LogInfo("Downloaded %s to cache", filepath.Base(manifest.URLFilename(u)))
	}
	return nil
}
