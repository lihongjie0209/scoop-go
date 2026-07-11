package download

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Test helpers

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "scoop-download-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func testServer(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
}

// --- Tests ---

func TestURLFilename(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/file.zip", "file.zip"},
		{"https://example.com/path/to/app.exe?download=1", "app.exe"},
		{"https://example.com/dl.zip#/myapp.7z", "myapp.7z"},
		{"https://example.com/file", "file"},
	}
	for _, tt := range tests {
		got := URLFilename(tt.url)
		if got != tt.want {
			t.Errorf("URLFilename(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestParseHash(t *testing.T) {
	tests := []struct {
		input string
		algo  string
		hex   string
	}{
		{"sha256:abc123", "sha256", "abc123"},
		{"md5:def456", "md5", "def456"},
		{"aabbccdd", "sha256", "aabbccdd"},
	}
	for _, tt := range tests {
		algo, hex := ParseHash(tt.input)
		if algo != tt.algo || hex != tt.hex {
			t.Errorf("ParseHash(%q) = (%q, %q), want (%q, %q)", tt.input, algo, hex, tt.algo, tt.hex)
		}
	}
}

func TestBasicDownload(t *testing.T) {
	content := "test file content for scoop download test"
	server := testServer(content)
	defer server.Close()

	dir := tempDir(t)
	dest := filepath.Join(dir, "test.txt")

	dl := NewDownloader(&Config{
		URL:         server.URL,
		Destination: dest,
	})

	result, err := dl.Download(context.Background())
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("downloaded content = %q, want %q", string(data), content)
	}
}

func TestCacheHit(t *testing.T) {
	content := "cached content"
	server := testServer(content)
	defer server.Close()

	dir := tempDir(t)
	cacheKey := "app#1.0#abc12345"
	dest := filepath.Join(dir, "output.txt")
	cachedPath := filepath.Join(dir, cacheKey)

	// Pre-populate cache with correct hash
	if err := os.WriteFile(cachedPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	dl := NewDownloader(&Config{
		URL:         server.URL,
		Destination: dest,
		CacheDir:    dir,
		CacheKey:    cacheKey,
		UseCache:    true,
	})

	result, err := dl.Download(context.Background())
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if !result.FromCache {
		t.Error("expected result from cache")
	}
}

func TestHashVerification(t *testing.T) {
	content := "verify this content"
	server := testServer(content)
	defer server.Close()

	dir := tempDir(t)
	dest := filepath.Join(dir, "verify.txt")

	// Calculate correct SHA256
	correctHash := sha256BytesFromString(content)

	// Test with correct hash
	dl := NewDownloader(&Config{
		URL:          server.URL,
		Destination:  dest,
		ExpectedHash: correctHash,
	})
	_, err := dl.Download(context.Background())
	if err != nil {
		t.Fatalf("Download with correct hash failed: %v", err)
	}

	// Test with wrong hash
	dest2 := filepath.Join(dir, "verify2.txt")
	dl2 := NewDownloader(&Config{
		URL:          server.URL,
		Destination:  dest2,
		ExpectedHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	_, err = dl2.Download(context.Background())
	if err == nil {
		t.Error("expected error for wrong hash, got nil")
	}
}

func TestSHA512Verification(t *testing.T) {
	content := []byte("sha512-content")
	sum := sha512.Sum512(content)
	dir := t.TempDir()
	cached := filepath.Join(dir, "sha512-cache")
	if err := os.WriteFile(cached, content, 0644); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader(&Config{CacheDir: dir, CacheKey: "sha512-cache", UseCache: true, ExpectedHash: fmt.Sprintf("sha512:%x", sum)})
	result, err := d.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.FromCache {
		t.Fatal("expected verified cache hit")
	}
}

func TestSourceForgeURL(t *testing.T) {
	handler := NewURLHandler(http.DefaultClient, "")

	url := handler.resolveSourceForge("https://sourceforge.net/projects/sevenzip/files/7-Zip/24.08/7z24008.exe/download")
	expected := "https://downloads.sourceforge.net/project/sevenzip/7-Zip/24.08/7z24008.exe"
	if url != expected {
		t.Errorf("SourceForge URL = %q, want %q", url, expected)
	}
}

func TestProxyFromConfig(t *testing.T) {
	tests := []struct {
		proxy string
		want  string // expected proxy host (empty = direct)
	}{
		{"", ""},        // default → ProxyFromEnvironment
		{"default", ""}, // default
		{"none", ""},    // direct
		{"proxy.example.com:8080", "proxy.example.com:8080"},
		{"user:pass@proxy.example.com:3128", "proxy.example.com:3128"},
	}

	for _, tt := range tests {
		proxyFn := proxyFromConfig(tt.proxy)
		if tt.want == "" {
			// Just ensure it doesn't panic
			if proxyFn == nil {
				t.Errorf("proxyFromConfig(%q) returned nil", tt.proxy)
			}
		}
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(1000) // 1KB/s
	defer rl.Stop()

	// Should be able to consume 1000 bytes quickly
	if err := rl.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRetryPolicyDefaults(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.MaxRetries != 3 {
		t.Errorf("expected 3 retries, got %d", p.MaxRetries)
	}
	if p.IsRetryableStatusCode(500) != true {
		t.Error("expected 500 to be retryable")
	}
	if p.IsRetryableStatusCode(404) != false {
		t.Error("expected 404 to not be retryable")
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := []string{
		"dial tcp: connection refused",
		"read: connection reset by peer",
		"request timeout",
		"tls handshake error",
	}
	nonRetryable := []string{
		"404 Not Found",
		"403 Forbidden",
	}

	for _, msg := range retryable {
		if !IsRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected %q to be retryable", msg)
		}
	}
	for _, msg := range nonRetryable {
		if IsRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected %q to NOT be retryable", msg)
		}
	}
}

func TestActualURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/file.zip", "https://example.com/file.zip"},
		{"https://example.com/file.zip#/dl.7z", "https://example.com/file.zip"},
		{"https://example.com/file#fragment", "https://example.com/file"},
	}
	for _, tt := range tests {
		got := ActualURL(tt.input)
		if got != tt.want {
			t.Errorf("ActualURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMultiConnectionDisabledForSmallFile(t *testing.T) {
	content := "small file"
	server := testServer(content)
	defer server.Close()

	dir := tempDir(t)

	dl := NewDownloader(&Config{
		URL:              server.URL,
		Destination:      filepath.Join(dir, "small.txt"),
		MultiConnections: 5,
		MinSplitSize:     10 * 1024 * 1024, // 10MB — file is smaller
	})

	result, err := dl.Download(context.Background())
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if result.TotalBytes != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), result.TotalBytes)
	}
}

// sha256BytesFromString calculates SHA256 of a string.
func sha256BytesFromString(s string) string {
	h := sha256FromContent([]byte(s))
	return fmt.Sprintf("%x", h)
}

func sha256FromContent(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	return h.Sum(nil)
}
