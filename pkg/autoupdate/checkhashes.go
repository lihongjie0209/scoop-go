package autoupdate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// UpdateHashes resolves hashes for the given manifest and stores them back into
// the manifest's top-level or architecture-specific hash fields.
func UpdateHashes(m *manifest.Manifest, version, arch string) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}

	urls := m.GetURL(arch)
	if len(urls) == 0 {
		return nil
	}

	hashCfg := manifest.AutoupdateHashConfig(m.Autoupdate, arch)
	hashes := make(manifest.FlexibleStrings, 0, len(urls))
	for i, rawURL := range urls {
		cfg := hashConfigForIndex(hashCfg, i)
		hash, err := resolveHash(rawURL, version, cfg)
		if err != nil {
			return fmt.Errorf("resolving hash for %s: %w", rawURL, err)
		}
		hashes = append(hashes, hash)
	}

	if archContent := m.GetArchContent(arch); arch != "" && archContent != nil && len(archContent.URL) > 0 {
		archContent.Hash = hashes
	} else {
		m.Hash = hashes
	}
	return nil
}

type hashConfig struct {
	URL   string
	Mode  string
	Regex string
	Raw   any
}

func hashConfigForIndex(raw any, index int) hashConfig {
	if arr, ok := raw.([]any); ok {
		if index < len(arr) {
			return parseHashConfig(arr[index])
		}
	}
	return parseHashConfig(raw)
}

func parseHashConfig(raw any) hashConfig {
	cfg := hashConfig{Raw: raw}
	switch v := raw.(type) {
	case string:
		if looksLikeURL(v) {
			cfg.URL = v
		} else {
			cfg.Regex = v
		}
	case map[string]any:
		if s, ok := v["url"].(string); ok {
			cfg.URL = s
		}
		if s, ok := v["mode"].(string); ok {
			cfg.Mode = strings.ToLower(s)
		}
		if s, ok := v["regex"].(string); ok {
			cfg.Regex = s
		}
		if s, ok := v["find"].(string); ok && cfg.Regex == "" {
			cfg.Regex = s
		}
	default:
		if raw == nil {
			return cfg
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return cfg
		}
		var mapped map[string]any
		if err := json.Unmarshal(data, &mapped); err != nil {
			return cfg
		}
		return parseHashConfig(mapped)
	}
	return cfg
}

func resolveHash(downloadURL, version string, cfg hashConfig) (string, error) {
	switch cfg.Mode {
	case "download", "fosshub":
		return downloadAndHash(downloadURL)
	}

	if cfg.Mode == "sourceforge" || cfg.URL != "" || cfg.Raw != nil {
		hash, err := manifest.FetchHashForURL(
			context.Background(),
			&http.Client{Timeout: defaultTimeout},
			downloadURL,
			version,
			cfg.Raw,
			"",
		)
		if err != nil {
			return "", err
		}
		if hash != "" {
			return hash, nil
		}
	}

	if cfg.Regex != "" {
		hashURL := sidecarHashURL(downloadURL)
		hash, err := manifest.FetchHashForURL(
			context.Background(),
			&http.Client{Timeout: defaultTimeout},
			downloadURL,
			version,
			map[string]any{"url": hashURL, "regex": cfg.Regex},
			"",
		)
		if err != nil {
			return "", err
		}
		if hash != "" {
			return hash, nil
		}
	}

	return downloadAndHash(downloadURL)
}

func downloadAndHash(rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "scoop-go/autoupdate")

	resp, err := (&http.Client{Timeout: defaultTimeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}

	sum := sha256.New()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sum.Sum(nil)), nil
}

func sidecarHashURL(rawURL string) string {
	if looksLikeURL(rawURL) {
		if parsed, err := url.Parse(rawURL); err == nil {
			parsed.RawQuery = ""
			parsed.Fragment = ""
			parsed.Path = strings.TrimSuffix(parsed.Path, path.Ext(parsed.Path)) + ".sha256"
			return parsed.String()
		}
	}
	return strings.TrimSuffix(rawURL, path.Ext(rawURL)) + ".sha256"
}

func looksLikeURL(value string) bool {
	return regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`).MatchString(strings.TrimSpace(value))
}
