package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/version"
)

const defaultGoReleaseRepo = "lihongjie0209/scoop-go"

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Draft   bool           `json:"draft"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type selfUpdatePlan struct {
	Version     string
	AssetName   string
	AssetURL    string
	ChecksumURL string
}

func planSelfUpdate(ctx context.Context, client *http.Client, apiBase, repo, currentVersion, goos, goarch string) (*selfUpdatePlan, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid release repository %q; expected owner/repo", repo)
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/releases/latest"
	var release githubRelease
	if err := getJSON(ctx, client, endpoint, &release); err != nil {
		return nil, fmt.Errorf("querying latest release: %w", err)
	}
	if release.Draft || release.TagName == "" {
		return nil, fmt.Errorf("latest release is not publishable")
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if currentVersion != "" && currentVersion != "dev" && version.Compare(currentVersion, latest) <= 0 {
		return nil, nil
	}

	wantSuffix := fmt.Sprintf("-%s-%s.exe", goos, goarch)
	var binary, checksums releaseAsset
	for _, asset := range release.Assets {
		lower := strings.ToLower(asset.Name)
		switch {
		case lower == "checksums.txt":
			checksums = asset
		case strings.HasSuffix(lower, wantSuffix) && strings.HasPrefix(lower, "scoop-go-"):
			binary = asset
		}
	}
	if binary.URL == "" {
		return nil, fmt.Errorf("release %s has no asset for %s/%s", release.TagName, goos, goarch)
	}
	if checksums.URL == "" {
		return nil, fmt.Errorf("release %s has no checksums.txt", release.TagName)
	}
	return &selfUpdatePlan{Version: latest, AssetName: binary.Name, AssetURL: binary.URL, ChecksumURL: checksums.URL}, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scoop-go-self-update")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target)
}

func downloadBytes(ctx context.Context, client *http.Client, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "scoop-go-self-update")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("download exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func checksumFor(checksums []byte, assetName string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(checksums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == assetName {
			hash := strings.ToLower(fields[0])
			if len(hash) != sha256.Size*2 {
				return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
			}
			if _, err := hex.DecodeString(hash); err != nil {
				return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
			}
			return hash, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s not found", assetName)
}

func stageSelfUpdate(ctx context.Context, client *http.Client, plan *selfUpdatePlan, targetPath string) (string, error) {
	checksums, err := downloadBytes(ctx, client, plan.ChecksumURL, 4<<20)
	if err != nil {
		return "", fmt.Errorf("downloading checksums: %w", err)
	}
	expected, err := checksumFor(checksums, plan.AssetName)
	if err != nil {
		return "", err
	}
	binary, err := downloadBytes(ctx, client, plan.AssetURL, 200<<20)
	if err != nil {
		return "", fmt.Errorf("downloading update: %w", err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(binary))
	if actual != expected {
		return "", fmt.Errorf("update checksum mismatch: expected %s, got %s", expected, actual)
	}
	if len(binary) < 2 || string(binary[:2]) != "MZ" {
		return "", fmt.Errorf("downloaded update is not a Windows executable")
	}

	staged := targetPath + ".new"
	if err := os.WriteFile(staged, binary, 0755); err != nil {
		return "", fmt.Errorf("staging update: %w", err)
	}
	return staged, nil
}

func launchReplaceHelper(targetPath, stagedPath string) error {
	current, err := os.Executable()
	if err != nil {
		return err
	}
	helper := filepath.Join(app.Dirs().ScoopDir, fmt.Sprintf("scoop-go-update-%d.exe", time.Now().UnixNano()))
	data, err := os.ReadFile(current)
	if err != nil {
		return fmt.Errorf("reading updater helper: %w", err)
	}
	if err := os.WriteFile(helper, data, 0755); err != nil {
		return fmt.Errorf("writing updater helper: %w", err)
	}
	cmd := exec.Command(helper, "__self-update-replace", "--pid", strconv.Itoa(os.Getpid()), "--target", targetPath, "--staged", stagedPath, "--helper", helper)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

// RunReplaceHelper waits for the parent process and atomically promotes the
// staged binary. The previous executable is retained as .old for rollback.
func RunReplaceHelper(parentPID int, targetPath, stagedPath, helperPath string) error {
	if parentPID <= 0 || targetPath == "" || stagedPath == "" {
		return fmt.Errorf("invalid self-update helper arguments")
	}
	targetPath = filepath.Clean(targetPath)
	stagedPath = filepath.Clean(stagedPath)
	if !filepath.IsAbs(targetPath) || stagedPath != targetPath+".new" {
		return fmt.Errorf("self-update paths do not form a valid target/staged pair")
	}
	backup := targetPath + ".old"
	_ = os.Remove(backup)
	var renameErr error
	for i := 0; i < 6000; i++ {
		renameErr = os.Rename(targetPath, backup)
		if renameErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if renameErr != nil {
		return fmt.Errorf("backing up current executable: %w", renameErr)
	}
	if err := os.Rename(stagedPath, targetPath); err != nil {
		_ = os.Rename(backup, targetPath)
		return fmt.Errorf("promoting update: %w", err)
	}
	if helperPath != "" {
		_ = os.Remove(helperPath)
	}
	return nil
}

func releaseRepo() string {
	if repo := os.Getenv("SCOOP_GO_REPO"); repo != "" {
		return repo
	}
	if cfg := app.Config(); cfg != nil && cfg.Config().ScoopGoRepo != "" {
		return cfg.Config().ScoopGoRepo
	}
	return defaultGoReleaseRepo
}

func githubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if cfg := app.Config(); cfg != nil {
		return cfg.Config().GH_TOKEN
	}
	return ""
}

func platformSupported() bool { return runtime.GOOS == "windows" }

// SelfUpdate securely stages the latest GitHub release and starts a detached
// helper that replaces the running executable after this process exits.
func SelfUpdate(currentVersion string) (bool, error) {
	if !platformSupported() {
		return false, fmt.Errorf("self-update is only supported on Windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Minute}
	apiBase := os.Getenv("SCOOP_GO_API_BASE")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	plan, err := planSelfUpdate(ctx, client, apiBase, releaseRepo(), currentVersion, runtime.GOOS, runtime.GOARCH)
	if err != nil || plan == nil {
		return false, err
	}
	target, err := os.Executable()
	if err != nil {
		return false, err
	}
	staged, err := stageSelfUpdate(ctx, client, plan, target)
	if err != nil {
		return false, err
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 30*time.Second)
	defer verifyCancel()
	if output, err := exec.CommandContext(verifyCtx, staged, "version").CombinedOutput(); err != nil {
		_ = os.Remove(staged)
		return false, fmt.Errorf("validating staged executable: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if err := launchReplaceHelper(target, staged); err != nil {
		_ = os.Remove(staged)
		return false, err
	}
	return true, nil
}
