package manifest

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// HashExtraction describes autoupdate.hash modes used by Scoop manifests.
// Supported modes: extract, json, github, sourceforge, metalink, xpath, rdf.
type HashExtraction struct {
	URL      string
	Mode     string
	Regex    string
	JSONPath string
	XPath    string
	Find     string
}

// FetchHashForURL tries to obtain a hash for downloadURL using autoupdate hash
// configuration and standard Scoop fallbacks (github/sourceforge/extract).
// Returns empty string if no hash could be extracted (caller should compute).
func FetchHashForURL(ctx context.Context, client *http.Client, downloadURL string, version string, hashCfg any, githubToken string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	subs := versionSubstitutions(version)
	basename := urlRemoteFilename(downloadURL)
	subs["$url"] = stripFragment(downloadURL)
	subs["$baseurl"] = strings.TrimRight(stripFilename(stripFragment(downloadURL)), "/")
	subs["$basename"] = basename
	subs["$urlNoExt"] = stripExt(stripFragment(downloadURL))
	subs["$basenameNoExt"] = stripExt(basename)

	extraction := parseHashExtraction(hashCfg, 0)
	mode := extraction.Mode
	hashURL := substituteVersionString(extraction.URL, subs)
	regex := extraction.Regex
	if regex == "" {
		regex = extraction.Find
	}
	jsonPath := extraction.JSONPath
	// mode xpath may set xpath via jsonpath field empty — read raw map
	if mode == "" {
		if m, ok := hashCfg.(map[string]any); ok {
			if _, has := m["xpath"]; has {
				mode = "xpath"
			}
		}
	}

	if mode == "" && hashURL != "" {
		mode = "extract"
	}
	mode = detectHashMode(mode, downloadURL)

	switch mode {
	case "extract", "":
		if hashURL == "" {
			return "", nil
		}
		return findHashInText(ctx, client, hashURL, subs, regex, basename)
	case "json":
		if hashURL == "" {
			return "", nil
		}
		return findHashInJSON(ctx, client, hashURL, subs, jsonPath)
	case "github":
		return findHashGitHub(ctx, client, downloadURL, githubToken)
	case "sourceforge":
		return findHashSourceForge(ctx, client, downloadURL, basename)
	case "metalink":
		return findHashMetalink(ctx, client, downloadURL, subs)
	case "xpath":
		if hashURL == "" {
			return "", nil
		}
		xpath := extraction.XPath
		if xpath == "" {
			if m, ok := hashCfg.(map[string]any); ok {
				if s, ok := m["xpath"].(string); ok {
					xpath = s
				}
			}
		}
		xpath = substituteVersionString(xpath, subs)
		return findHashInXML(ctx, client, hashURL, xpath)
	case "rdf":
		if hashURL == "" {
			return "", nil
		}
		return findHashInRDF(ctx, client, hashURL, basename)
	default:
		if hashURL != "" {
			return findHashInText(ctx, client, hashURL, subs, regex, basename)
		}
		return "", nil
	}
}

// detectHashMode auto-selects mode from the download URL when mode is empty.
func detectHashMode(mode, downloadURL string) string {
	if mode != "" {
		return mode
	}
	switch {
	case regexp.MustCompile(`https://github\.com/[^/]+/[^/]+/releases/download/`).MatchString(downloadURL):
		return "github"
	case regexp.MustCompile(`(?:downloads\.)?sourceforge\.net/`).MatchString(downloadURL):
		return "sourceforge"
	default:
		return mode
	}
}

func findHashSourceForge(ctx context.Context, client *http.Client, downloadURL, basename string) (string, error) {
	// Match: (downloads.)?sourceforge.net/project(s)?/<project>/(files/)?<file>
	re := regexp.MustCompile(`(?:downloads\.)?sourceforge\.net/(?:projects?)/([^/]+)/(?:files/)?(.*)$`)
	// Also: downloads.sourceforge.net/project/<project>/<file>
	re2 := regexp.MustCompile(`(?:downloads\.)?sourceforge\.net/project/([^/]+)/(.*)$`)
	m := re.FindStringSubmatch(stripFragment(downloadURL))
	if m == nil {
		m = re2.FindStringSubmatch(stripFragment(downloadURL))
	}
	if m == nil {
		return "", nil
	}
	project, file := m[1], m[2]
	file = strings.TrimSuffix(file, "/download")
	hashURL := fmt.Sprintf("https://sourceforge.net/projects/%s/files/%s", project, file)
	// Parent directory listing often holds checksums
	hashURL = strings.TrimRight(stripFilename(hashURL), "/")
	regex := `"$basename":.*?"sha1":\s*"([a-fA-F0-9]{40})"`
	subs := map[string]string{"$basename": basename}
	return findHashInText(ctx, client, hashURL, subs, regex, basename)
}

func findHashMetalink(ctx context.Context, client *http.Client, downloadURL string, subs map[string]string) (string, error) {
	// Scoop metalink: HEAD without following redirects; read Digest header on 3xx.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Scoop/1.0 (+http://scoop.sh/) Go")

	// Custom client that does not follow redirects
	noRedirect := *client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if d := resp.Header.Get("Digest"); d != "" {
			if h := parseDigestHeader(d); h != "" {
				return h, nil
			}
		}
	}
	// Fallback: downloadURL.meta4 text extract
	return findHashInText(ctx, client, downloadURL+".meta4", subs, "", urlRemoteFilename(downloadURL))
}

func parseDigestHeader(header string) string {
	// e.g. SHA-256=base64, SHA=base64, MD5=base64
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		algo := strings.ToUpper(strings.TrimSpace(part[:eq]))
		val := strings.TrimSpace(part[eq+1:])
		if algo == "SHA-256" || algo == "SHA" || algo == "MD5" || algo == "SHA-1" || algo == "SHA1" {
			return formatHash(val)
		}
	}
	return ""
}

// FetchHashesForURLs resolves hashes for each URL. hashCfg may be a single
// extraction object, a string URL, or an array aligned with URLs.
func FetchHashesForURLs(ctx context.Context, client *http.Client, urls []string, version string, hashCfg any, githubToken string) []string {
	out := make([]string, len(urls))
	for i, u := range urls {
		// Per-index extraction when hash is an array
		cfg := hashCfg
		if arr, ok := hashCfg.([]any); ok && i < len(arr) {
			cfg = arr[i]
		}
		h, err := FetchHashForURL(ctx, client, u, version, cfg, githubToken)
		if err == nil && h != "" {
			out[i] = h
		}
	}
	return out
}

func parseHashExtraction(cfg any, index int) HashExtraction {
	if cfg == nil {
		return HashExtraction{}
	}
	switch v := cfg.(type) {
	case string:
		return HashExtraction{URL: v, Mode: "extract"}
	case []any:
		if index < len(v) {
			return parseHashExtraction(v[index], 0)
		}
		return HashExtraction{}
	case map[string]any:
		he := HashExtraction{}
		if s, ok := v["url"].(string); ok {
			he.URL = s
		}
		if s, ok := v["mode"].(string); ok {
			he.Mode = s
		}
		if s, ok := v["regex"].(string); ok {
			he.Regex = s
		}
		if s, ok := v["find"].(string); ok {
			he.Find = s
		}
		if s, ok := v["jsonpath"].(string); ok {
			he.JSONPath = s
			if he.Mode == "" {
				he.Mode = "json"
			}
		}
		if s, ok := v["jp"].(string); ok {
			he.JSONPath = s
			if he.Mode == "" {
				he.Mode = "json"
			}
		}
		if s, ok := v["xpath"].(string); ok {
			he.XPath = s
			if he.Mode == "" {
				he.Mode = "xpath"
			}
		}
		return he
	default:
		// Autoupdate stored as json.RawMessage-like via re-marshal
		data, err := json.Marshal(v)
		if err != nil {
			return HashExtraction{}
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			return HashExtraction{}
		}
		return parseHashExtraction(m, 0)
	}
}

func findHashInText(ctx context.Context, client *http.Client, hashURL string, subs map[string]string, regex, basename string) (string, error) {
	body, err := httpGetBody(ctx, client, hashURL, "")
	if err != nil {
		return "", err
	}
	text := string(body)

	if regex == "" {
		regex = `^\s*([a-fA-F0-9]+)\s*$`
	}
	// Template placeholders for hash types
	templates := map[string]string{
		"$md5":      `([a-fA-F0-9]{32})`,
		"$sha1":     `([a-fA-F0-9]{40})`,
		"$sha256":   `([a-fA-F0-9]{64})`,
		"$sha512":   `([a-fA-F0-9]{128})`,
		"$checksum": `([a-fA-F0-9]{32,128})`,
		"$base64":   `([a-zA-Z0-9+/=]{24,88})`,
	}
	for k, v := range templates {
		regex = strings.ReplaceAll(regex, k, v)
	}
	regex = substituteVersionString(regex, subs)

	re, err := regexp.Compile("(?m)" + regex)
	if err != nil {
		return "", err
	}
	if m := re.FindStringSubmatch(text); len(m) > 1 {
		return formatHash(m[1]), nil
	}

	// Filename-based fallback
	escaped := regexp.QuoteMeta(basename)
	fileRe := regexp.MustCompile(`(?i)([a-fA-F0-9]{32,128})[\x20\t]+.*` + escaped + `|(?:` + escaped + `)[\x20\t]+.*?([a-fA-F0-9]{32,128})`)
	if m := fileRe.FindStringSubmatch(text); len(m) > 0 {
		for _, g := range m[1:] {
			if g != "" {
				return formatHash(g), nil
			}
		}
	}
	return "", nil
}

func findHashInJSON(ctx context.Context, client *http.Client, hashURL string, subs map[string]string, jsonPath string) (string, error) {
	body, err := httpGetBody(ctx, client, hashURL, "")
	if err != nil {
		return "", err
	}
	jsonPath = substituteVersionString(jsonPath, subs)
	// Very small JSONPath subset: $.a.b.c or a.b.c
	path := strings.TrimPrefix(jsonPath, "$")
	path = strings.TrimPrefix(path, ".")
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", err
	}
	cur := root
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		// Skip filters for now; handle simple keys
		if i := strings.Index(part, "["); i >= 0 {
			part = part[:i]
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return "", nil
		}
		cur, ok = m[part]
		if !ok {
			return "", nil
		}
	}
	switch v := cur.(type) {
	case string:
		return formatHash(v), nil
	default:
		return "", nil
	}
}

func findHashGitHub(ctx context.Context, client *http.Client, downloadURL, token string) (string, error) {
	re := regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+)/releases/download/([^/]+)/`)
	m := re.FindStringSubmatch(downloadURL)
	if len(m) < 4 {
		return "", nil
	}
	owner, repo, tag := m[1], m[2], m[3]
	api := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	body, err := httpGetBody(ctx, client, api, token)
	if err != nil {
		// fallback: list releases and search assets
		api = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)
		body, err = httpGetBody(ctx, client, api, token)
		if err != nil {
			return "", err
		}
	}

	// Try single release object first
	var release struct {
		Assets []struct {
			BrowserDownloadURL string `json:"browser_download_url"`
			Digest             string `json:"digest"`
		} `json:"assets"`
	}
	if json.Unmarshal(body, &release) == nil && len(release.Assets) > 0 {
		for _, a := range release.Assets {
			if a.BrowserDownloadURL == downloadURL || strings.HasSuffix(downloadURL, path.Base(a.BrowserDownloadURL)) {
				if h := parseGitHubDigest(a.Digest); h != "" {
					return h, nil
				}
			}
		}
	}

	// Array of releases
	var releases []struct {
		Assets []struct {
			BrowserDownloadURL string `json:"browser_download_url"`
			Digest             string `json:"digest"`
		} `json:"assets"`
	}
	if json.Unmarshal(body, &releases) == nil {
		for _, rel := range releases {
			for _, a := range rel.Assets {
				if a.BrowserDownloadURL == downloadURL {
					if h := parseGitHubDigest(a.Digest); h != "" {
						return h, nil
					}
				}
			}
		}
	}
	return "", nil
}

func parseGitHubDigest(digest string) string {
	// format: "sha256:hex" or similar
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return ""
	}
	if i := strings.Index(digest, ":"); i >= 0 {
		return formatHash(digest[i+1:])
	}
	return formatHash(digest)
}

func httpGetBody(ctx context.Context, client *http.Client, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Scoop/1.0 (+http://scoop.sh/) Go")
	if token != "" && strings.Contains(rawURL, "api.github.com") {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	// Referer for hash files
	if u, err := url.Parse(rawURL); err == nil {
		ref := *u
		ref.Path = path.Dir(ref.Path)
		req.Header.Set("Referer", ref.String())
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(resp.Body)
}

func formatHash(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(strings.ToLower(h), "sha256:") {
		return formatHash(h[strings.Index(h, ":")+1:])
	}
	// Hex digests: normalize case
	if isHex(h) {
		return strings.ToLower(h)
	}
	// Digest headers often use base64
	if decoded, err := base64.StdEncoding.DecodeString(h); err == nil && len(decoded) > 0 {
		hexStr := hex.EncodeToString(decoded)
		switch len(hexStr) {
		case 32, 40, 64, 128:
			return hexStr
		}
	}
	// Also try URL-safe base64
	if decoded, err := base64.URLEncoding.DecodeString(h); err == nil && len(decoded) > 0 {
		hexStr := hex.EncodeToString(decoded)
		switch len(hexStr) {
		case 32, 40, 64, 128:
			return hexStr
		}
	}
	return strings.ToLower(h)
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func urlRemoteFilename(raw string) string {
	raw = stripFragment(raw)
	if u, err := url.Parse(raw); err == nil {
		base := path.Base(u.Path)
		if base != "" && base != "." && base != "/" {
			if decoded, err := url.QueryUnescape(base); err == nil {
				return decoded
			}
			return base
		}
	}
	return path.Base(raw)
}

func stripFragment(raw string) string {
	if i := strings.Index(raw, "#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

func stripFilename(raw string) string {
	if i := strings.LastIndexAny(raw, "/\\"); i >= 0 {
		return raw[:i+1]
	}
	return raw
}

func stripExt(name string) string {
	return strings.TrimSuffix(name, path.Ext(name))
}

func substituteVersionString(s string, subs map[string]string) string {
	if s == "" {
		return s
	}
	// Longest keys first
	keys := make([]string, 0, len(subs))
	for k := range subs {
		keys = append(keys, k)
	}
	// simple n^2 sort by length
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if len(keys[j]) > len(keys[i]) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		s = strings.ReplaceAll(s, k, subs[k])
	}
	return s
}

func findHashInXML(ctx context.Context, client *http.Client, hashURL, xpath string) (string, error) {
	body, err := httpGetBody(ctx, client, hashURL, "")
	if err != nil {
		return "", err
	}
	// Minimal XPath subset: /a/b/c and /a/b[@attr='val']/c
	return xpathExtract(string(body), xpath)
}

func findHashInRDF(ctx context.Context, client *http.Client, hashURL, basename string) (string, error) {
	body, err := httpGetBody(ctx, client, hashURL, "")
	if err != nil {
		return "", err
	}
	text := string(body)
	// Look for Content about="basename" ... <sha256>...</sha256>
	// Simple scan without full RDF parser
	re := regexp.MustCompile(`(?is)about\s*=\s*["']` + regexp.QuoteMeta(basename) + `["'][^>]*>.*?<(?:sha256|SHA256)[^>]*>\s*([a-fA-F0-9]{64})\s*<`)
	if m := re.FindStringSubmatch(text); len(m) > 1 {
		return formatHash(m[1]), nil
	}
	// Alternate: element order Content then about
	re2 := regexp.MustCompile(`(?is)<Content[^>]*about\s*=\s*["']` + regexp.QuoteMeta(basename) + `["'][^>]*>.*?<(?:sha256|SHA256)[^>]*>\s*([a-fA-F0-9]{64})`)
	if m := re2.FindStringSubmatch(text); len(m) > 1 {
		return formatHash(m[1]), nil
	}
	return "", nil
}

// xpathExtract supports a small absolute path subset used by Scoop manifests:
// /root/child/grand and /root/item[@name='x']/sha256
func xpathExtract(xmlText, xpath string) (string, error) {
	xpath = strings.TrimSpace(xpath)
	if xpath == "" {
		return "", nil
	}
	// Attribute predicate path: /hashes/file[@name='tool.zip']/sha256
	if re := regexp.MustCompile(`^/([\w:.-]+)/([\w:.-]+)\[@([\w:.-]+)='([^']+)'\]/([\w:.-]+)$`); re.MatchString(xpath) {
		m := re.FindStringSubmatch(xpath)
		// root, elem, attr, attrVal, child
		root, elem, attr, attrVal, child := m[1], m[2], m[3], m[4], m[5]
		blockRe := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(elem) + `[^>]*` + regexp.QuoteMeta(attr) + `\s*=\s*["']` + regexp.QuoteMeta(attrVal) + `["'][^>]*>(.*?)</` + regexp.QuoteMeta(elem) + `>`)
		// Prefer searching within root context if present
		_ = root
		if bm := blockRe.FindStringSubmatch(xmlText); len(bm) > 1 {
			childRe := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(child) + `[^>]*>\s*([^<]+?)\s*</` + regexp.QuoteMeta(child) + `>`)
			if cm := childRe.FindStringSubmatch(bm[1]); len(cm) > 1 {
				return formatHash(strings.TrimSpace(cm[1])), nil
			}
		}
		return "", nil
	}
	// Simple /a/b/c path — take last element text of matching nest (last occurrence)
	parts := strings.Split(strings.Trim(xpath, "/"), "/")
	if len(parts) == 0 {
		return "", nil
	}
	last := parts[len(parts)-1]
	re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(last) + `[^>]*>\s*([a-fA-F0-9]{32,128})\s*</` + regexp.QuoteMeta(last) + `>`)
	if m := re.FindStringSubmatch(xmlText); len(m) > 1 {
		return formatHash(m[1]), nil
	}
	return "", nil
}

// AutoupdateHashConfig extracts the hash extraction config from a raw autoupdate field.
func AutoupdateHashConfig(autoupdate any, arch string) any {
	if autoupdate == nil {
		return nil
	}
	data, err := json.Marshal(autoupdate)
	if err != nil {
		return nil
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	// Prefer architecture-specific hash
	if archMap, ok := root["architecture"].(map[string]any); ok {
		if ac, ok := archMap[arch].(map[string]any); ok {
			if h, ok := ac["hash"]; ok {
				return h
			}
		}
	}
	if h, ok := root["hash"]; ok {
		return h
	}
	return nil
}
