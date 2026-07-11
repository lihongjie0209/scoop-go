package cmd

import (
	"context"
	"fmt"
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
	skipHash bool
}

var downloadCmd = &cobra.Command{
	Use:   "download <app>",
	Short: "Download apps in the cache folder and verify hashes",
	Long:  "Download an app to the cache without installing it.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName, _, requestedVersion := parseAppRef(args[0])

		// Find manifest
		m, bucketName, err := install.FindManifest(appName)
		if err != nil {
			return err
		}

		arch := install.GetArchitecture(downloadFlags.arch)
		if requestedVersion != "" && requestedVersion != m.Version {
			m, err = install.GenerateVersionManifest(context.Background(), appName, m, requestedVersion, arch, !downloadFlags.noCache)
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

			expectedHash := ""
			if !downloadFlags.skipHash {
				expectedHash = m.HashForURL(url, supportedArch)
			}

			dl := download.NewDownloader(&download.Config{
				URL:          url,
				Destination:  targetPath,
				CacheDir:     cacheDir,
				CacheKey:     cacheKey,
				UseCache:     !downloadFlags.noCache,
				Cookies:      m.Cookie,
				ExpectedHash: expectedHash,
			})

			result, err := dl.Download(context.Background())
			if err != nil {
				return fmt.Errorf("downloading %s: %w", url, err)
			}

			app.LogSuccess("Downloaded %s to cache (%d bytes)", fname, result.TotalBytes)
		}

		return nil
	},
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
	downloadCmd.Flags().BoolVarP(&downloadFlags.noCache, "no-cache", "k", false, "Don't use cache")
	downloadCmd.Flags().BoolVarP(&downloadFlags.skipHash, "skip-hash-check", "s", false, "Skip hash check")
}
