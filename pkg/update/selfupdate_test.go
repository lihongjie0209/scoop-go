package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanAndStageSelfUpdate(t *testing.T) {
	binary := append([]byte("MZ"), []byte("fake-windows-binary")...)
	assetName := "scoop-go-v1.2.0-windows-amd64.exe"
	hash := fmt.Sprintf("%x", sha256.Sum256(binary))

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/scoop-go/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.0","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
				assetName, server.URL+"/download/binary", server.URL+"/download/checksums")
		case "/download/binary":
			_, _ = w.Write(binary)
		case "/download/checksums":
			fmt.Fprintf(w, "%s  %s\n", hash, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	plan, err := planSelfUpdate(context.Background(), server.Client(), server.URL, "acme/scoop-go", "1.0.0", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Version != "1.2.0" || plan.AssetName != assetName {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	target := filepath.Join(t.TempDir(), "scoop-go.exe")
	staged, err := stageSelfUpdate(context.Background(), server.Client(), plan, target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(staged)
	if err != nil || string(data) != string(binary) {
		t.Fatalf("staged binary mismatch: %v", err)
	}
}

func TestPlanSelfUpdateAlreadyCurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.0","assets":[]}`)
	}))
	defer server.Close()
	plan, err := planSelfUpdate(context.Background(), server.Client(), server.URL, "acme/scoop-go", "1.2.0", "windows", "amd64")
	if err != nil || plan != nil {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
}

func TestPlanSelfUpdateDoesNotDowngrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.0","assets":[]}`)
	}))
	defer server.Close()
	plan, err := planSelfUpdate(context.Background(), server.Client(), server.URL, "acme/scoop-go", "2.0.0", "windows", "amd64")
	if err != nil || plan != nil {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
}

func TestChecksumForRejectsMissingAndMalformedEntries(t *testing.T) {
	if _, err := checksumFor([]byte("not-a-hash file.exe\n"), "file.exe"); err == nil {
		t.Fatal("expected malformed checksum to fail")
	}
	if _, err := checksumFor([]byte{}, "file.exe"); err == nil {
		t.Fatal("expected missing checksum to fail")
	}
}

func TestRunReplaceHelperKeepsRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "scoop-go.exe")
	staged := target + ".new"
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := RunReplaceHelper(1, target, staged, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if got, _ := os.ReadFile(target + ".old"); string(got) != "old" {
		t.Fatalf("rollback = %q", got)
	}
}

func TestRunReplaceHelperRejectsUnrelatedStagedPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "scoop-go.exe")
	if err := RunReplaceHelper(1, target, filepath.Join(filepath.Dir(target), "other.exe"), ""); err == nil {
		t.Fatal("expected unrelated staged path to be rejected")
	}
}
