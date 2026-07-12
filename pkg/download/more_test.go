package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseHashAndVerify(t *testing.T) {
	algo, hex := ParseHash("sha256:abcd")
	if algo != "sha256" || hex != "abcd" {
		t.Fatalf("%s %s", algo, hex)
	}
	algo, hex = ParseHash("deadbeef")
	if algo != "sha256" {
		t.Fatal(algo)
	}
	_ = hex
	if err := verifyHash("aa", "bb"); err == nil {
		t.Fatal("mismatch")
	}
	if err := verifyHash("aa", "AA"); err != nil {
		t.Fatal(err)
	}
	if err := verifyHash("", "anything"); err != nil {
		t.Fatal(err)
	}
}

func TestHashTypeAndFile(t *testing.T) {
	if hashTypeFromString("md5:x") != "md5" {
		t.Fatal()
	}
	if hashTypeFromString("") != "" {
		t.Fatal()
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	_ = os.WriteFile(p, []byte("hi"), 0644)
	h := hashFile(p, "sha256")
	if len(h) != 64 {
		t.Fatal(h)
	}
	if hashFile(filepath.Join(dir, "missing"), "sha256") != "" {
		t.Fatal("missing file")
	}
}

func TestProxyFromConfigNoneAndHostPort(t *testing.T) {
	fn := proxyFromConfig("none")
	if u, err := fn(nil); err != nil || u != nil {
		t.Fatalf("%v %v", u, err)
	}
	// Scoop proxy format is host:port without scheme
	fn = proxyFromConfig("127.0.0.1:8080")
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	u, err := fn(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("%v %v", u, err)
	}
}

func TestDownloadWithCacheHit(t *testing.T) {
	content := []byte("cached-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	cacheKey := "app#1#abc"
	cachePath := filepath.Join(dir, cacheKey)
	_ = os.WriteFile(cachePath, content, 0644)
	dest := filepath.Join(dir, "out.bin")

	dl := NewDownloader(&Config{
		URL:          server.URL + "/f.bin",
		Destination:  dest,
		CacheDir:     dir,
		CacheKey:     cacheKey,
		UseCache:     true,
		ExpectedHash: "", // skip verify
	})
	res, err := dl.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatal("expected cache hit")
	}
}

func TestUserAgent(t *testing.T) {
	ua := userAgent()
	if ua == "" {
		t.Fatal("empty ua")
	}
}
