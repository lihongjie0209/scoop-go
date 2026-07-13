package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/download"
	"github.com/scoopinstaller/scoop-go/pkg/install"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/spf13/cobra"
)

var downloadFlags struct {
	arch     string
	noCache  bool
	force    bool
	skipHash bool
}

var downloadCmd = &cobra.Command{
	Use:   "download <app> [app...]",
	Short: "Download apps in the cache folder and verify hashes",
	Long:  "Download apps to the cache without installing them.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var firstErr error
		for _, raw := range args {
			if err := downloadOne(raw); err != nil {
				app.LogError("%v", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	},
}

func downloadOne(raw string) error {
	appName, preferredBucket, requestedVersion := parseAppRef(raw)

	m, bucketName, err := install.FindAvailableManifest(appName, preferredBucket)
	if err != nil {
		return err
	}

	arch := install.GetArchitecture(downloadFlags.arch)
	useCache := !downloadFlags.noCache && !downloadFlags.force
	if requestedVersion != "" && requestedVersion != m.Version {
		m, err = install.GenerateVersionManifest(context.Background(), appName, m, requestedVersion, arch, useCache)
		if err != nil {
			return err
		}
	}
	supportedArch := m.ResolveArch(arch)
	if supportedArch == "" {
		return fmt.Errorf("'%s' doesn't support architecture %s", appName, arch)
	}

	urls := m.GetURL(supportedArch)
	if len(urls) == 0 {
		return fmt.Errorf("no URLs defined for '%s'", appName)
	}

	app.LogInfo("Downloading '%s' (%s) [%s]", appName, m.Version, supportedArch)
	_ = bucketName

	for _, url := range urls {
		fname := manifest.URLFilename(url)
		cacheDir := app.Dirs().CacheDir
		cacheKey := fmt.Sprintf("%s#%s#%s", appName, m.Version, shortHash(url))
		targetPath := filepath.Join(cacheDir, cacheKey)

		if downloadFlags.force {
			_ = os.Remove(targetPath)
		}

		expectedHash := ""
		if !downloadFlags.skipHash {
			expectedHash = m.HashForURL(url, supportedArch)
		}

		dl := download.NewDownloader(&download.Config{
			URL:          url,
			Destination:  targetPath,
			CacheDir:     cacheDir,
			CacheKey:     cacheKey,
			UseCache:     useCache,
			Cookies:      m.GetCookie(supportedArch),
			ExpectedHash: expectedHash,
		})

		result, err := dl.Download(context.Background())
		if err != nil {
			return fmt.Errorf("downloading %s: %w", url, err)
		}

		app.LogSuccess("Downloaded %s to cache (%d bytes)", fname, result.TotalBytes)
	}

	return nil
}

func shortHash(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%08x", h)[:7]
}

func init() {
	rootCmd.AddCommand(downloadCmd)
	downloadCmd.Flags().StringVarP(&downloadFlags.arch, "arch", "a", "", "Architecture")
	downloadCmd.Flags().BoolVarP(&downloadFlags.force, "force", "f", false, "Force re-download (ignore cache)")
	downloadCmd.Flags().BoolVarP(&downloadFlags.noCache, "no-cache", "k", false, "Don't use cache")
	downloadCmd.Flags().BoolVarP(&downloadFlags.skipHash, "skip-hash-check", "s", false, "Skip hash check")
}
