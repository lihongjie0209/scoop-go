package manifest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchHashExtractMode(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(want + "  tool.zip\n"))
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/tool.zip", "1.2.3",
		map[string]any{
			"url":   server.URL + "/SHA256SUMS",
			"mode":  "extract",
			"regex": `$sha256`,
		}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
}

func TestFetchHashFromBasenameLine(t *testing.T) {
	const want = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(want + "  app-1.0.0.zip\n"))
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://cdn.example.com/files/app-1.0.0.zip", "1.0.0",
		map[string]any{"url": server.URL + "/hashes.txt"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
}

func TestFetchHashVersionSubstitutionInURL(t *testing.T) {
	const want = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte(want + "\n"))
	}))
	defer server.Close()

	cfg := map[string]any{
		"url":  server.URL + "/v$version/SHA256",
		"mode": "extract",
	}
	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/app-9.9.9.zip", "9.9.9", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("hash = %q", hash)
	}
	if !strings.Contains(seenPath, "v9.9.9") {
		t.Fatalf("version not substituted in hash URL path: %s", seenPath)
	}
}

func TestFetchHashJSONMode(t *testing.T) {
	const want = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"checksums": map[string]any{"sha256": want},
		})
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/f.zip", "1.0.0",
		map[string]any{
			"url":      server.URL + "/meta.json",
			"jsonpath": "$.checksums.sha256",
		}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
}

func TestFetchHashGitHubAPIMock(t *testing.T) {
	const want = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	downloadURL := "https://github.com/owner/repo/releases/download/v1.0.0/tool.zip"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"browser_download_url": downloadURL, "digest": "sha256:" + want},
			},
		})
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.github.com") {
			req2 := req.Clone(req.Context())
			u, err := req.URL.Parse(server.URL + req.URL.Path)
			if err != nil {
				return nil, err
			}
			req2.URL = u
			req2.Host = u.Host
			return http.DefaultTransport.RoundTrip(req2)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}

	// nil hashCfg triggers github auto-detect from download URL
	hash, err := FetchHashForURL(context.Background(), client, downloadURL, "1.0.0", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("github hash = %q, want %q", hash, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchHashesForURLsArrayConfig(t *testing.T) {
	h1 := "1111111111111111111111111111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222222222222222222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/1"):
			_, _ = w.Write([]byte(h1 + "\n"))
		case strings.HasSuffix(r.URL.Path, "/2"):
			_, _ = w.Write([]byte(h2 + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	urls := []string{"https://ex.com/a.zip", "https://ex.com/b.zip"}
	cfg := []any{
		map[string]any{"url": server.URL + "/1", "mode": "extract"},
		map[string]any{"url": server.URL + "/2", "mode": "extract"},
	}
	got := FetchHashesForURLs(context.Background(), server.Client(), urls, "1.0", cfg, "")
	if len(got) != 2 || got[0] != h1 || got[1] != h2 {
		t.Fatalf("got %v", got)
	}
}

func TestAutoupdateHashConfigArch(t *testing.T) {
	auto := map[string]any{
		"architecture": map[string]any{
			"64bit": map[string]any{
				"hash": map[string]any{"url": "https://example.com/h64", "mode": "extract"},
			},
		},
		"hash": "https://example.com/hdefault",
	}
	cfg := AutoupdateHashConfig(auto, "64bit")
	m, ok := cfg.(map[string]any)
	if !ok {
		t.Fatalf("cfg type %T", cfg)
	}
	if m["url"] != "https://example.com/h64" {
		t.Fatalf("url = %v", m["url"])
	}
	cfg2 := AutoupdateHashConfig(auto, "arm64")
	if cfg2 != "https://example.com/hdefault" {
		t.Fatalf("global hash cfg = %v", cfg2)
	}
}

func TestParseHashExtraction(t *testing.T) {
	if got := parseHashExtraction("https://h", 0); got.URL != "https://h" || got.Mode != "extract" {
		t.Fatalf("%+v", got)
	}
	he := parseHashExtraction(map[string]any{"url": "u", "jp": "$.x"}, 0)
	if he.JSONPath != "$.x" || he.Mode != "json" {
		t.Fatalf("%+v", he)
	}
	arr := parseHashExtraction([]any{map[string]any{"url": "u2"}}, 0)
	if arr.URL != "u2" {
		t.Fatalf("%+v", arr)
	}
}

func TestFormatHashAndDigest(t *testing.T) {
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := formatHash(strings.ToUpper(want)); got != want {
		t.Fatalf("got %s", got)
	}
	if got := parseGitHubDigest("sha256:" + want); got != want {
		t.Fatalf("digest = %q", got)
	}
}

func TestURLRemoteFilename(t *testing.T) {
	if got := urlRemoteFilename("https://x.com/a/b/c.zip#/dir"); got != "c.zip" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchHashMissingReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("no hashes here\n"))
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/tool.zip", "1.0.0",
		map[string]any{"url": server.URL + "/empty", "mode": "extract"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash, got %q", hash)
	}
}

func TestFetchHashSourceForgeMode(t *testing.T) {
	const want = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // sha1
	// downloads.sourceforge.net URL should rewrite to sourceforge.net/projects/.../files page
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expected path fragment for project sevenzip file page
		if !strings.Contains(r.URL.Path, "sevenzip") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`"7z2408-x64.exe": {"sha1": "` + want + `"}`))
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "sourceforge.net") {
			req2 := req.Clone(req.Context())
			u, err := req.URL.Parse(server.URL + req.URL.Path)
			if err != nil {
				return nil, err
			}
			req2.URL = u
			req2.Host = u.Host
			return http.DefaultTransport.RoundTrip(req2)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}

	downloadURL := "https://downloads.sourceforge.net/project/sevenzip/7-Zip/24.08/7z2408-x64.exe"
	hash, err := FetchHashForURL(context.Background(), client, downloadURL, "24.08",
		map[string]any{"mode": "sourceforge"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("sourceforge hash = %q want %q", hash, want)
	}
}

func TestFetchHashMetalinkModeFromDigestHeader(t *testing.T) {
	// SHA-256 of empty string as base64
	// e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	b64 := "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Method == http.MethodGet {
			// 302 with Digest header (as Scoop metalink mode expects on redirect response)
			w.Header().Set("Digest", "SHA-256="+b64)
			w.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		server.URL+"/file.zip", "1.0.0",
		map[string]any{"mode": "metalink"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("metalink hash = %q want %q", hash, want)
	}
}

func TestDetectHashModeFromURL(t *testing.T) {
	if got := detectHashMode("", "https://downloads.sourceforge.net/project/foo/bar.exe"); got != "sourceforge" {
		t.Fatalf("got %q", got)
	}
	if got := detectHashMode("", "https://github.com/a/b/releases/download/v1/x.zip"); got != "github" {
		t.Fatalf("got %q", got)
	}
	if got := detectHashMode("extract", "https://example.com/x"); got != "extract" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchHashXPathMode(t *testing.T) {
	const want = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<hashes>
  <file name="tool.zip"><sha256>` + want + `</sha256></file>
</hashes>`))
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/tool.zip", "1.0.0",
		map[string]any{
			"url":   server.URL + "/hashes.xml",
			"xpath": "/hashes/file[@name='tool.zip']/sha256",
		}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("xpath hash = %q want %q", hash, want)
	}
}

func TestFetchHashRDFMode(t *testing.T) {
	const want = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<RDF xmlns="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <Content about="tool.zip"><sha256>` + want + `</sha256></Content>
  <Content about="other.zip"><sha256>0000000000000000000000000000000000000000000000000000000000000000</sha256></Content>
</RDF>`))
	}))
	defer server.Close()

	hash, err := FetchHashForURL(context.Background(), server.Client(),
		"https://example.com/releases/tool.zip", "1.0.0",
		map[string]any{
			"url":  server.URL + "/checksum.rdf",
			"mode": "rdf",
		}, "")
	if err != nil {
		t.Fatal(err)
	}
	if hash != want {
		t.Fatalf("rdf hash = %q want %q", hash, want)
	}
}
