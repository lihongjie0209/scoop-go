package autoupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

const defaultTimeout = 10 * time.Second

var githubAPIBaseURL = "https://api.github.com"

// CheckVersion resolves the latest version for a manifest using Scoop checkver
// semantics.
func CheckVersion(m *manifest.Manifest, appName string) (string, error) {
	cfg, rawString, err := normalizeCheckver(m)
	if err != nil {
		return "", fmt.Errorf("normalizing checkver for %s: %w", appName, err)
	}

	if cfg.Script != nil {
		version, err := runCheckverScript(cfg.Script)
		if err != nil {
			return "", fmt.Errorf("running checkver script for %s: %w", appName, err)
		}
		if version == "" {
			return "", fmt.Errorf("checkver script for %s returned empty version", appName)
		}
		return version, nil
	}

	switch {
	case strings.EqualFold(rawString, "github") || cfg.Github != "":
		version, err := checkGitHubVersion(m, cfg)
		if err != nil {
			return "", fmt.Errorf("checking GitHub version for %s: %w", appName, err)
		}
		return version, nil
	case cfg.URL != "":
		version, err := checkURLVersion(cfg.URL, cfg)
		if err != nil {
			return "", fmt.Errorf("checking URL version for %s: %w", appName, err)
		}
		return version, nil
	case cfg.Regex != "":
		version, err := checkURLVersion(m.Homepage, cfg)
		if err != nil {
			return "", fmt.Errorf("checking homepage version for %s: %w", appName, err)
		}
		return version, nil
	default:
		return "", fmt.Errorf("manifest has no supported checkver configuration")
	}
}

func normalizeCheckver(m *manifest.Manifest) (manifest.CheckverObj, string, error) {
	if m == nil || m.Checkver == nil {
		return manifest.CheckverObj{}, "", fmt.Errorf("checkver is not configured")
	}

	switch v := m.Checkver.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "github") {
			return manifest.CheckverObj{Github: m.Homepage}, v, nil
		}
		return manifest.CheckverObj{Regex: v}, v, nil
	case manifest.CheckverObj:
		return v, "", nil
	case *manifest.CheckverObj:
		if v == nil {
			return manifest.CheckverObj{}, "", fmt.Errorf("checkver is nil")
		}
		return *v, "", nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return manifest.CheckverObj{}, "", err
		}
		var obj manifest.CheckverObj
		if err := json.Unmarshal(data, &obj); err != nil {
			return manifest.CheckverObj{}, "", err
		}
		return obj, "", nil
	}
}

func checkGitHubVersion(m *manifest.Manifest, cfg manifest.CheckverObj) (string, error) {
	repoPath, err := parseGitHubRepoPath(firstNonEmpty(cfg.Github, m.Homepage))
	if err != nil {
		return "", err
	}

	apiURL := strings.TrimRight(githubAPIBaseURL, "/") + "/repos/" + repoPath + "/releases/latest"
	body, err := httpGet(apiURL, cfg.UserAgent)
	if err != nil {
		return "", err
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decoding GitHub response: %w", err)
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("GitHub response missing tag_name")
	}

	version := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v"), "V")
	if cfg.Regex != "" {
		version, err = extractWithRegex(version, cfg.Regex, false, cfg.Replace)
		if err != nil {
			return "", err
		}
	} else {
		version = applyReplace(version, "", cfg.Replace)
	}
	if version == "" {
		return "", fmt.Errorf("empty version extracted from GitHub tag")
	}
	return version, nil
}

func checkURLVersion(rawURL string, cfg manifest.CheckverObj) (string, error) {
	body, err := httpGet(rawURL, cfg.UserAgent)
	if err != nil {
		return "", err
	}

	switch {
	case cfg.JSONPath != "":
		version, err := jsonPathExtract(body, cfg.JSONPath)
		if err != nil {
			return "", fmt.Errorf("extracting JSONPath %q: %w", cfg.JSONPath, err)
		}
		return applyReplace(strings.TrimSpace(version), "", cfg.Replace), nil
	case cfg.XPath != "":
		version, err := xmlPathExtract(body, cfg.XPath)
		if err != nil {
			return "", fmt.Errorf("extracting XPath %q: %w", cfg.XPath, err)
		}
		return applyReplace(strings.TrimSpace(version), "", cfg.Replace), nil
	case cfg.Regex != "":
		return extractWithRegex(string(body), cfg.Regex, cfg.Reverse, cfg.Replace)
	default:
		return "", fmt.Errorf("checkver URL requires regex, jsonpath, or xpath")
	}
}

func httpGet(rawURL, userAgent string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", firstNonEmpty(userAgent, "scoop-go/autoupdate"))

	resp, err := (&http.Client{Timeout: defaultTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(resp.Body)
}

func extractWithRegex(text, pattern string, reverse bool, replace string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}

	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("regex %q did not match", pattern)
	}

	versions := make([]string, 0, len(matches))
	for _, match := range matches {
		full := match[0]
		value := full
		if replace != "" {
			value = re.ReplaceAllString(full, replace)
		} else {
			for _, group := range match[1:] {
				if group != "" {
					value = group
					break
				}
			}
		}
		value = strings.TrimSpace(value)
		if value != "" {
			versions = append(versions, value)
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("regex %q produced no version", pattern)
	}
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	}
	return versions[0], nil
}

func applyReplace(value, pattern, replace string) string {
	if replace == "" {
		return value
	}
	if pattern != "" {
		if re, err := regexp.Compile(pattern); err == nil && re.MatchString(value) {
			return strings.TrimSpace(re.ReplaceAllString(value, replace))
		}
	}
	return strings.TrimSpace(strings.NewReplacer("$0", value, "${0}", value).Replace(replace))
}

func parseGitHubRepoPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty GitHub repository")
	}
	if !strings.Contains(raw, "://") {
		raw = strings.Trim(raw, "/")
		parts := strings.Split(raw, "/")
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1], nil
		}
		return "", fmt.Errorf("invalid GitHub repository %q", raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid GitHub homepage %q", raw)
	}
	return parts[0] + "/" + parts[1], nil
}

func runCheckverScript(script any) (string, error) {
	lines := manifest.NormalizeStringSlice(script)
	if len(lines) == 0 {
		return "", fmt.Errorf("unsupported script value")
	}
	scriptText := strings.Join(lines, "\n")
	for _, exe := range []string{"powershell", "pwsh"} {
		cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", scriptText)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			return strings.TrimSpace(stdout.String()), nil
		} else if execErr, ok := err.(*exec.Error); ok && execErr.Err != nil {
			continue
		} else {
			return "", fmt.Errorf("%s failed: %w: %s", exe, err, strings.TrimSpace(stderr.String()))
		}
	}
	return "", fmt.Errorf("powershell executable not found")
}

func jsonPathExtract(body []byte, expr string) (string, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", err
	}

	tokens, err := parseJSONPath(expr)
	if err != nil {
		return "", err
	}

	cur := root
	for _, token := range tokens {
		switch t := token.(type) {
		case string:
			obj, ok := cur.(map[string]any)
			if !ok {
				return "", fmt.Errorf("path segment %q is not an object", t)
			}
			cur, ok = obj[t]
			if !ok {
				return "", fmt.Errorf("path segment %q not found", t)
			}
		case int:
			arr, ok := cur.([]any)
			if !ok {
				return "", fmt.Errorf("path index %d is not an array", t)
			}
			if t < 0 || t >= len(arr) {
				return "", fmt.Errorf("path index %d out of range", t)
			}
			cur = arr[t]
		}
	}

	switch v := cur.(type) {
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func parseJSONPath(expr string) ([]any, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, fmt.Errorf("empty jsonpath")
	}

	var tokens []any
	for len(expr) > 0 {
		if expr[0] == '.' {
			expr = expr[1:]
			continue
		}
		if expr[0] == '[' {
			end := strings.IndexByte(expr, ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated jsonpath index")
			}
			index, err := strconv.Atoi(expr[1:end])
			if err != nil {
				return nil, fmt.Errorf("invalid jsonpath index: %w", err)
			}
			tokens = append(tokens, index)
			expr = expr[end+1:]
			continue
		}
		next := len(expr)
		if dot := strings.IndexByte(expr, '.'); dot >= 0 && dot < next {
			next = dot
		}
		if bracket := strings.IndexByte(expr, '['); bracket >= 0 && bracket < next {
			next = bracket
		}
		tokens = append(tokens, expr[:next])
		expr = expr[next:]
	}
	return tokens, nil
}

type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []xmlNode  `xml:",any"`
}

func xmlPathExtract(body []byte, expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.Trim(expr, "/")
	if expr == "" {
		return "", fmt.Errorf("empty xpath")
	}

	var root xmlNode
	if err := xml.Unmarshal(body, &root); err != nil {
		return "", err
	}

	segments, err := parseXPath(expr)
	if err != nil {
		return "", err
	}
	nodes := []*xmlNode{&root}
	if len(segments) > 0 && segments[0].name == root.XMLName.Local {
		segments = segments[1:]
	}

	for _, segment := range segments {
		var next []*xmlNode
		for _, node := range nodes {
			for i := range node.Children {
				child := &node.Children[i]
				if child.XMLName.Local != segment.name {
					continue
				}
				if segment.attrName != "" && xmlAttr(child.Attrs, segment.attrName) != segment.attrValue {
					continue
				}
				next = append(next, child)
			}
		}
		if len(next) == 0 {
			return "", fmt.Errorf("xpath segment %q not found", segment.name)
		}
		nodes = next
	}

	return strings.TrimSpace(nodes[0].Text), nil
}

type xpathSegment struct {
	name      string
	attrName  string
	attrValue string
}

func parseXPath(expr string) ([]xpathSegment, error) {
	parts := strings.Split(expr, "/")
	segments := make([]xpathSegment, 0, len(parts))
	re := regexp.MustCompile(`^([^\[]+)(?:\[@([^=]+)=['"]([^'"]+)['"]\])?$`)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		match := re.FindStringSubmatch(part)
		if len(match) == 0 {
			return nil, fmt.Errorf("unsupported xpath segment %q", part)
		}
		segments = append(segments, xpathSegment{
			name:      match[1],
			attrName:  match[2],
			attrValue: match[3],
		})
	}
	return segments, nil
}

func xmlAttr(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
