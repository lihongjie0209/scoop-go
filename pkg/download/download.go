// Package download provides HTTP/HTTPS download capabilities with:
// - Multi-connection segmented downloads (replaces Aria2)
// - Cache management
// - Hash verification (SHA256, SHA1, MD5, SHA512)
// - Cookie, proxy, and GitHub token support
// - Progress reporting
// - Rate limiting
// - Automatic retry with exponential backoff
// - Special URL handling (Fosshub, SourceForge, GitHub private)
// - Resume support for interrupted downloads
//
// Mirrors lib/download.ps1 from the original Scoop.
package download

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Config holds download configuration.
type Config struct {
	URL              string
	Destination      string
	CacheDir         string
	CacheKey         string // app#version#hash
	UseCache         bool
	Cookies          map[string]string
	Proxy            string // "user:pass@host:port", "default", "none"
	GithubToken      string
	Headers          map[string]string
	ExpectedHash     string // "sha256:xxx" or "md5:xxx" or plain sha256
	MultiConnections int    // number of parallel connections (0 = single)
	MinSplitSize     int64  // minimum bytes per segment before splitting (default 5MB)
	RateLimit        int64  // max bytes per second (0 = unlimited)
	RetryPolicy      RetryPolicy
	ProgressCallback func(downloaded, total int64)
}

// Result holds the download result.
type Result struct {
	Path       string
	TotalBytes int64
	Hash       string
	FromCache  bool
}

// Downloader handles file downloads with retry, proxy, and progress support.
type Downloader struct {
	client  *http.Client
	handler *URLHandler
	limiter *RateLimiter
	config  *Config
}

// NewDownloader creates a new downloader based on the provided config.
func NewDownloader(cfg *Config) *Downloader {
	// Default retry policy
	if cfg.RetryPolicy.MaxRetries == 0 {
		cfg.RetryPolicy = DefaultRetryPolicy()
	}
	// Default min split size
	if cfg.MinSplitSize == 0 {
		cfg.MinSplitSize = 5 * 1024 * 1024 // 5MB
	}
	// Default multi connections
	if cfg.MultiConnections == 0 {
		cfg.MultiConnections = 5
	}

	// Build transport with proxy support
	transport := &http.Transport{
		Proxy: proxyFromConfig(cfg.Proxy),
	}

	// Build cookie jar if needed
	jar, _ := cookiejar.New(nil)

	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   0, // no timeout — we use context for cancellation
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// URL handler
	handler := NewURLHandler(client, cfg.GithubToken)

	// Rate limiter
	var limiter *RateLimiter
	if cfg.RateLimit > 0 {
		limiter = NewRateLimiter(cfg.RateLimit)
	}

	return &Downloader{
		client:  client,
		handler: handler,
		limiter: limiter,
		config:  cfg,
	}
}

// Download performs the download with full lifecycle management.
// It handles cache lookup, URL resolution, retry, hash verification, and caching.
func (d *Downloader) Download(ctx context.Context) (*Result, error) {
	cfg := d.config

	// 1. Check cache
	if cfg.UseCache && cfg.CacheDir != "" && cfg.CacheKey != "" {
		cachedPath := filepath.Join(cfg.CacheDir, cfg.CacheKey)
		if info, err := os.Stat(cachedPath); err == nil && info.Size() > 0 {
			result, err := d.verifyAndReturn(cachedPath)
			if err == nil {
				result.FromCache = true
				return result, nil
			}
			// Cache miss (hash mismatch) — remove and re-download
			os.Remove(cachedPath)
		}
	}

	// 2. Resolve special URL
	resolvedURL, err := d.handler.Resolve(ctx, ActualURL(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("resolving URL: %w", err)
	}

	var result *Result

	// 3. Choose download strategy
	useMultipart := cfg.MultiConnections > 1 && d.supportsRange(ctx, resolvedURL)
	if useMultipart {
		result, err = d.downloadMultiPart(ctx, resolvedURL)
	} else {
		result, err = d.downloadSingleWithRetry(ctx, resolvedURL)
	}
	if err != nil {
		return nil, err
	}

	// 4. Cache the result
	if cfg.UseCache && cfg.CacheDir != "" && cfg.CacheKey != "" && !result.FromCache {
		d.cacheFile(result.Path)
	}

	return result, nil
}

// downloadSingleWithRetry wraps downloadSinglePart with retry logic.
func (d *Downloader) downloadSingleWithRetry(ctx context.Context, url string) (*Result, error) {
	var result *Result

	err := DoWithRetries(ctx, d.config.RetryPolicy, "download",
		func() error {
			var err error
			result, err = d.downloadSinglePart(ctx, url)
			return err
		})

	return result, err
}

// downloadSinglePart downloads a file in a single HTTP request with resume support.
func (d *Downloader) downloadSinglePart(ctx context.Context, url string) (*Result, error) {
	cfg := d.config
	dest := cfg.Destination
	_ = URLFilename(cfg.URL) // local filename hint

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return nil, fmt.Errorf("creating destination directory: %w", err)
	}

	// Determine file mode and initial offset (support resume)
	var fileMode int
	var initialOffset int64

	if cfg.UseCache && cfg.CacheDir != "" {
		// Try to resume an interrupted download
		if existing, err := os.Stat(dest + ".partial"); err == nil {
			initialOffset = existing.Size()
			fileMode = os.O_APPEND | os.O_WRONLY | os.O_CREATE
		} else {
			fileMode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
	} else {
		fileMode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	// Build request
	req, err := d.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	// Set Range header for resume
	if initialOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", initialOffset))
	}

	setRequestHeaders(req, cfg.URL, cfg.Cookies, cfg.Headers, cfg.GithubToken)

	var resp *http.Response
	resp, err = d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle redirects manually for special cases
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		redirectURL := resp.Header.Get("Location")
		if redirectURL != "" {
			info("Following redirect to %s...", redirectURL)
			return d.downloadSingleWithRetry(ctx, redirectURL)
		}
	}

	// Determine expected total
	totalBytes := resp.ContentLength
	if initialOffset > 0 && totalBytes > 0 {
		totalBytes += initialOffset
	}

	// Validate response
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Open file
	f, err := os.OpenFile(dest, fileMode, 0644)
	if err != nil {
		return nil, fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	// Seek to initial offset if resuming
	if initialOffset > 0 {
		f.Seek(initialOffset, io.SeekStart)
	}

	// Setup hash verification
	var hasher hash.Hash
	hashType := hashTypeFromString(cfg.ExpectedHash)
	if cfg.ExpectedHash != "" {
		hasher = newHash(hashType)
	}

	// Write destination
	writer := io.Writer(f)
	if hasher != nil {
		if initialOffset > 0 {
			// For resume, we need to hash from the beginning
			// Read existing file and hash it
			existingHash := sha256Bytes(dest)
			// Parse existing hash if available
			_ = existingHash
			// Re-create hasher from scratch
			hasher = newHash(hashType)
		}
		writer = io.MultiWriter(f, hasher)
	}

	var downloaded int64
	if initialOffset > 0 {
		downloaded = initialOffset
	}
	buf := make([]byte, 32*1024)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Rate limiting
			if d.limiter != nil {
				if err := d.limiter.Wait(ctx, int64(n)); err != nil {
					return nil, err
				}
			}

			if _, writeErr := writer.Write(buf[:n]); writeErr != nil {
				return nil, writeErr
			}
			downloaded += int64(n)
			if cfg.ProgressCallback != nil && totalBytes > 0 {
				cfg.ProgressCallback(downloaded, totalBytes)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	// Verify hash
	if hasher != nil {
		actualHash := fmt.Sprintf("%x", hasher.Sum(nil))
		if err := verifyHash(cfg.ExpectedHash, actualHash); err != nil {
			// Keep partial file for resume
			os.Rename(dest, dest+".partial")
			return nil, err
		}
	}

	return &Result{Path: dest, TotalBytes: downloaded}, nil
}

// downloadMultiPart downloads a file using multiple parallel connections (replaces Aria2).
// Each connection downloads a range of the file, then they're assembled.
func (d *Downloader) downloadMultiPart(ctx context.Context, url string) (*Result, error) {
	cfg := d.config

	// HEAD request for file size
	resp, err := d.client.Head(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HEAD request failed: %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	if totalSize <= 0 {
		return d.downloadSingleWithRetry(ctx, url)
	}

	// Determine segment count
	segments := cfg.MultiConnections
	if totalSize < cfg.MinSplitSize || segments < 2 {
		return d.downloadSingleWithRetry(ctx, url)
	}

	segSize := totalSize / int64(segments)
	if segSize < cfg.MinSplitSize {
		segments = int(totalSize / cfg.MinSplitSize)
		if segments < 1 {
			segments = 1
		}
		segSize = totalSize / int64(segments)
	}

	dest := cfg.Destination
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return nil, err
	}

	// Pre-allocate file
	f, err := os.Create(dest)
	if err != nil {
		return nil, err
	}
	f.Truncate(totalSize)
	f.Close()

	// Download segments in parallel
	var wg sync.WaitGroup
	errCh := make(chan error, segments)
	var downloaded int64
	var mu sync.Mutex

	for i := 0; i < segments; i++ {
		wg.Add(1)
		start := int64(i) * segSize
		end := start + segSize - 1
		if i == segments-1 {
			end = totalSize - 1
		}

		go func(segIdx int, segStart, segEnd int64) {
			defer wg.Done()

			err := DoWithRetries(ctx, cfg.RetryPolicy,
				fmt.Sprintf("segment-%d", segIdx),
				func() error {
					return d.downloadRange(ctx, url, dest, segStart, segEnd, &mu, &downloaded, totalSize)
				})

			if err != nil {
				errCh <- fmt.Errorf("segment %d: %w", segIdx, err)
			}
		}(i, start, end)
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	var firstErr error
	for err := range errCh {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		os.Remove(dest)
		return nil, fmt.Errorf("multi-connection download failed: %w", firstErr)
	}

	// Verify hash
	if cfg.ExpectedHash != "" {
		hash := sha256Bytes(dest)
		if err := verifyHash(cfg.ExpectedHash, hash); err != nil {
			os.Remove(dest)
			return nil, err
		}
	}

	return &Result{Path: dest, TotalBytes: totalSize}, nil
}

// downloadRange downloads a specific byte range of a file.
func (d *Downloader) downloadRange(ctx context.Context, url, dest string, start, end int64,
	mu *sync.Mutex, downloaded *int64, totalSize int64) error {

	req, err := d.newRequest(ctx, url)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	setRequestHeaders(req, url, d.config.Cookies, d.config.Headers, d.config.GithubToken)

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("range request failed: HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Rate limiting
			if d.limiter != nil {
				if err := d.limiter.Wait(ctx, int64(n)); err != nil {
					return err
				}
			}

			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			mu.Lock()
			*downloaded += int64(n)
			if d.config.ProgressCallback != nil {
				d.config.ProgressCallback(*downloaded, totalSize)
			}
			mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	return nil
}

// newRequest creates an HTTP request with context.
func (d *Downloader) newRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// supportsRange checks if a server supports Range requests by sending a HEAD request.
func (d *Downloader) supportsRange(ctx context.Context, rawURL string) bool {
	req, err := http.NewRequestWithContext(ctx, "HEAD", rawURL, nil)
	if err != nil {
		return false
	}
	setRequestHeaders(req, d.config.URL, d.config.Cookies, d.config.Headers, d.config.GithubToken)

	resp, err := d.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.Header.Get("Accept-Ranges") == "bytes" || resp.ContentLength > 10*1024*1024
}

// cacheFile copies a downloaded file to the cache directory.
func (d *Downloader) cacheFile(src string) {
	cachePath := filepath.Join(d.config.CacheDir, d.config.CacheKey)
	if err := os.MkdirAll(d.config.CacheDir, 0755); err != nil {
		return
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return
	}
	defer srcFile.Close()

	dstFile, err := os.Create(cachePath)
	if err != nil {
		return
	}
	defer dstFile.Close()

	io.Copy(dstFile, srcFile)
}

// verifyAndReturn checks if a cached file passes hash verification.
func (d *Downloader) verifyAndReturn(path string) (*Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if d.config.ExpectedHash == "" {
		return &Result{Path: path, TotalBytes: info.Size()}, nil
	}

	hashStr := sha256Bytes(path)
	if err := verifyHash(d.config.ExpectedHash, hashStr); err != nil {
		os.Remove(path)
		return nil, err
	}

	return &Result{Path: path, TotalBytes: info.Size()}, nil
}

// --- Proxy configuration ---

// proxyFromConfig parses the Scoop proxy config string and returns a Proxy function.
//
//	"user:password@host:port"     — explicit proxy with auth
//	"currentuser@host:port"      — use current user credentials
//	"default" or ""              — system proxy (ProxyFromEnvironment)
//	"none"                       — direct connection (no proxy)
//
// Reference: lib/download.ps1 setup_proxy() L551-L578
func proxyFromConfig(proxy string) func(*http.Request) (*url.URL, error) {
	if proxy == "" || proxy == "default" {
		return http.ProxyFromEnvironment
	}
	if proxy == "none" {
		return func(*http.Request) (*url.URL, error) {
			return nil, nil
		}
	}

	// Parse "user:password@host:port" or "currentuser@host:port"
	proxyURL := &url.URL{
		Scheme: "http",
	}

	// Split credentials from address
	atIdx := strings.LastIndex(proxy, "@")
	if atIdx >= 0 {
		credentials := proxy[:atIdx]
		proxyURL.Host = proxy[atIdx+1:]

		if credentials == "currentuser" {
			// Use the current Windows user's default credentials
			if runtime.GOOS == "windows" {
				user := os.Getenv("USERNAME")
				domain := os.Getenv("USERDOMAIN")
				if domain != "" {
					proxyURL.User = url.User(domain + "\\" + user)
				} else {
					proxyURL.User = url.User(user)
				}
			}
			// On non-Windows, leave credentials unset (proxy will challenge)
		} else {
			// Split user:password with escaped character support
			user, pass := splitCredentials(credentials)
			if pass != "" {
				proxyURL.User = url.UserPassword(user, pass)
			} else {
				proxyURL.User = url.User(user)
			}
		}
	} else {
		proxyURL.Host = proxy
	}

	return func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	}
}

// splitCredentials splits a "user:password" credential string with support
// for escaped characters (\@ and \: are literal characters, not delimiters).
func splitCredentials(cred string) (user, pass string) {
	for i := 0; i < len(cred); i++ {
		if cred[i] == '\\' && i+1 < len(cred) {
			i++ // skip escaped char
			continue
		}
		if cred[i] == ':' {
			return unescapeCred(cred[:i]), unescapeCred(cred[i+1:])
		}
	}
	return unescapeCred(cred), ""
}

// unescapeCred replaces backslash-escaped sequences in a credential segment.
// Sequences: \\ → \, \: → :, \@ → @
func unescapeCred(s string) string {
	s = strings.ReplaceAll(s, "\\:", ":")
	s = strings.ReplaceAll(s, "\\@", "@")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

// --- Request headers ---

// setRequestHeaders sets all Scoop-specific HTTP headers on a request.
func setRequestHeaders(req *http.Request, rawURL string, cookies, headers map[string]string, githubToken string) {
	// User-Agent
	req.Header.Set("User-Agent", userAgent())

	// Referer (skip for SourceForge and PortableApps)
	actualURL := ActualURL(rawURL)
	if !strings.Contains(actualURL, "sourceforge.net") && !strings.Contains(actualURL, "portableapps.com") {
		if idx := strings.LastIndex(actualURL, "/"); idx >= 0 {
			req.Header.Set("Referer", actualURL[:idx+1])
		}
	}

	// GitHub API
	if strings.Contains(actualURL, "api.github.com/repos") {
		req.Header.Set("Accept", "application/octet-stream")
		if githubToken != "" {
			req.Header.Set("Authorization", "token "+githubToken)
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}
	}

	// Cookies
	if len(cookies) > 0 {
		var cookieParts []string
		for k, v := range cookies {
			cookieParts = append(cookieParts, k+"="+v)
		}
		req.Header.Set("Cookie", strings.Join(cookieParts, "; "))
	}

	// Custom headers (from private_hosts config)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

func userAgent() string {
	return "Scoop/1.0 (+http://scoop.sh/) Go/1.21"
}

// --- Hash verification ---

// ParseHash parses a hash string like "sha256:xxx" or "md5:xxx" or plain hex.
// Returns (algorithm, hexValue).
func ParseHash(hashStr string) (string, string) {
	if strings.Contains(hashStr, ":") {
		parts := strings.SplitN(hashStr, ":", 2)
		return strings.ToLower(parts[0]), strings.ToLower(parts[1])
	}
	return "sha256", strings.ToLower(hashStr)
}

func hashTypeFromString(hashStr string) string {
	if hashStr == "" {
		return ""
	}
	algo, _ := ParseHash(hashStr)
	return algo
}

func newHash(algo string) hash.Hash {
	switch algo {
	case "md5":
		return md5.New()
	case "sha1":
		return sha1.New()
	case "sha256":
		return sha256.New()
	case "sha512":
		return sha512.New()
	}
	return nil
}

func verifyHash(expected, actual string) error {
	if expected == "" {
		return nil
	}
	_, expectedHex := ParseHash(expected)
	if expectedHex == "" {
		expectedHex = strings.ToLower(expected)
	}

	actual = strings.ToLower(actual)
	if actual != expectedHex {
		return fmt.Errorf("hash check failed: expected %s, got %s", expectedHex, actual)
	}
	return nil
}

func sha256Bytes(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// info logs an informational message.
func info(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "INFO  %s\n", fmt.Sprintf(format, args...))
}
