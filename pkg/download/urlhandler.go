// Package download handles URL resolution for special download sources.
// Mirrors handle_special_urls() from lib/download.ps1.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// URLHandler resolves special download URLs.
type URLHandler struct {
	client      *http.Client
	githubToken string
}

// NewURLHandler creates a new URL handler.
func NewURLHandler(client *http.Client, githubToken string) *URLHandler {
	return &URLHandler{
		client:      client,
		githubToken: githubToken,
	}
}

// Resolve processes a URL and returns the actual download URL.
func (h *URLHandler) Resolve(ctx context.Context, url string) (string, error) {
	url = h.resolveFosshub(ctx, url)
	url = h.resolveSourceForge(url)
	url = h.resolveGithubPrivate(ctx, url)
	return url, nil
}

// ActualURL returns the URL without any #fragment.
func ActualURL(rawURL string) string {
	if idx := strings.Index(rawURL, "#"); idx >= 0 {
		return rawURL[:idx]
	}
	return rawURL
}

// URLFilename extracts the intended local filename from a URL,
// respecting Scoop's #/filename convention.
func URLFilename(rawURL string) string {
	if idx := strings.Index(rawURL, "#/"); idx >= 0 {
		fragment := rawURL[idx+2:]
		if fragment != "" {
			if i := strings.Index(fragment, "#"); i >= 0 {
				fragment = fragment[:i]
			}
			return fragment
		}
	}
	base := rawURL
	if idx := strings.Index(base, "?"); idx >= 0 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "#"); idx >= 0 {
		base = base[:idx]
	}
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}

func (h *URLHandler) resolveSourceForge(url string) string {
	re := regexp.MustCompile(`(?:downloads\.)?sourceforge\.net/projects?/([^/]+)/(?:files/)?(.*?)(?:$|/download|\?)`)
	if m := re.FindStringSubmatch(url); len(m) >= 3 {
		return fmt.Sprintf("https://downloads.sourceforge.net/project/%s/%s", m[1], m[2])
	}
	return url
}

func (h *URLHandler) resolveFosshub(ctx context.Context, url string) string {
	re := regexp.MustCompile(`^(?:.*fosshub\.com/)(?P<name>.*)(?:/|\?dwl=)(?P<filename>.*)$`)
	if !re.MatchString(url) {
		return url
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return url
	}
	req.Header.Set("User-Agent", "Scoop/1.0")
	resp, err := h.client.Do(req)
	if err != nil {
		return url
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return url
	}
	idRe := regexp.MustCompile(`"p":"([a-f0-9]{24}).*?"r":"([a-f0-9]{24})"`)
	idMatch := idRe.FindStringSubmatch(string(body))
	if len(idMatch) < 3 {
		return url
	}
	payload := map[string]interface{}{
		"projectUri":      extractFosshubProject(url),
		"fileName":        extractFosshubFilename(url),
		"source":          "CF",
		"isLatestVersion": true,
		"projectId":       idMatch[1],
		"releaseId":       idMatch[2],
	}
	payloadBytes, _ := json.Marshal(payload)
	apiReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.fosshub.com/download/",
		strings.NewReader(string(payloadBytes)))
	if err != nil {
		return url
	}
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("User-Agent", "Scoop/1.0")
	apiResp, err := h.client.Do(apiReq)
	if err != nil {
		return url
	}
	defer apiResp.Body.Close()
	apiBody, err := io.ReadAll(apiResp.Body)
	if err != nil {
		return url
	}
	var fosshubResp struct {
		Error interface{} `json:"error"`
		Data  struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(apiBody, &fosshubResp); err != nil {
		return url
	}
	if fosshubResp.Error == nil && fosshubResp.Data.URL != "" {
		return fosshubResp.Data.URL
	}
	return url
}

func (h *URLHandler) resolveGithubPrivate(ctx context.Context, url string) string {
	if h.githubToken == "" {
		return url
	}
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/releases/download/([^/]+)/([^/#]+)`)
	m := re.FindStringSubmatch(url)
	if len(m) < 5 {
		return url
	}
	owner, repo, tag, file := m[1], m[2], m[3], m[4]
	repoURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	repoReq, _ := http.NewRequestWithContext(ctx, "GET", repoURL, nil)
	repoReq.Header.Set("Authorization", "Bearer "+h.githubToken)
	repoReq.Header.Set("User-Agent", "Scoop/1.0")
	repoResp, err := h.client.Do(repoReq)
	if err != nil {
		return url
	}
	defer repoResp.Body.Close()
	var repoInfo struct {
		Private bool `json:"private"`
	}
	if err := json.NewDecoder(repoResp.Body).Decode(&repoInfo); err != nil {
		return url
	}
	if !repoInfo.Private {
		return url
	}
	assetURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	assetReq, _ := http.NewRequestWithContext(ctx, "GET", assetURL, nil)
	assetReq.Header.Set("Authorization", "Bearer "+h.githubToken)
	assetReq.Header.Set("User-Agent", "Scoop/1.0")
	assetResp, err := h.client.Do(assetReq)
	if err != nil {
		return url
	}
	defer assetResp.Body.Close()
	var release struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(assetResp.Body).Decode(&release); err != nil {
		return url
	}
	for _, asset := range release.Assets {
		if asset.Name == file {
			return asset.URL + "/" + file
		}
	}
	return url
}

func extractFosshubProject(url string) string {
	re := regexp.MustCompile(`fosshub\.com/([^/]+)`)
	m := re.FindStringSubmatch(url)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func extractFosshubFilename(url string) string {
	re := regexp.MustCompile(`\?dwl=([^&]+)`)
	m := re.FindStringSubmatch(url)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
