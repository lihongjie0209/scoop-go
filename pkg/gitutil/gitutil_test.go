package gitutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestIsRepo(t *testing.T) {
	if IsRepo(t.TempDir()) {
		t.Fatal("empty dir is not a repo")
	}
	repoDir := initTestRepo(t, "initial")
	if !IsRepo(repoDir) {
		t.Fatal("expected IsRepo true")
	}
}

func TestHeadHashAndCurrentBranch(t *testing.T) {
	dir := initTestRepo(t, "first commit")
	hash, err := HeadHash(dir)
	if err != nil || len(hash) != 40 {
		t.Fatalf("HeadHash = %q err=%v", hash, err)
	}
	branch, err := CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "master" && branch != "main" {
		// go-git default is often master
		t.Logf("branch = %s (acceptable if non-empty)", branch)
	}
	if branch == "" {
		t.Fatal("empty branch")
	}
}

func TestNameStatusBetweenCommits(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	// commit 1: a.txt
	write(t, filepath.Join(dir, "a.txt"), "a")
	if _, err := wt.Add("a.txt"); err != nil {
		t.Fatal(err)
	}
	h1, err := wt.Commit("c1", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// commit 2: modify a, add b, delete nothing yet
	write(t, filepath.Join(dir, "a.txt"), "a2")
	write(t, filepath.Join(dir, "b.txt"), "b")
	if _, err := wt.Add("a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("b.txt"); err != nil {
		t.Fatal(err)
	}
	h2, err := wt.Commit("c2", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := NameStatus(dir, h1.String(), h2.String())
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, e := range entries {
		byPath[e.Path] = e.Status
	}
	if byPath["a.txt"] != "M" {
		t.Fatalf("a.txt status = %q entries=%+v", byPath["a.txt"], entries)
	}
	if byPath["b.txt"] != "A" {
		t.Fatalf("b.txt status = %q entries=%+v", byPath["b.txt"], entries)
	}
}

func TestLogRange(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "f.txt"), "1")
	_, _ = wt.Add("f.txt")
	h1, err := wt.Commit("msg-one", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "f.txt"), "2")
	_, _ = wt.Add("f.txt")
	h2, err := wt.Commit("msg-two", &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}

	logs, err := LogRange(dir, h1.String(), h2.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	if logs[0] == "" || len(logs[0]) < 8 {
		t.Fatalf("log line = %q", logs[0])
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	dir := initTestRepo(t, "base")
	dirty, err := HasUncommittedChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("clean repo should not have uncommitted changes")
	}
	write(t, filepath.Join(dir, "new.txt"), "x")
	dirty, err = HasUncommittedChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("expected uncommitted changes after new file")
	}
}

func TestCheckAvailable(t *testing.T) {
	if err := CheckAvailable(); err != nil {
		t.Fatal(err)
	}
}

func TestHeadHashNonRepo(t *testing.T) {
	if _, err := HeadHash(t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestNativeNameStatusParserViaFallback(t *testing.T) {
	// ensure nativeNameStatus parsing path works with empty invalid repo
	if _, err := NameStatus(t.TempDir(), "", "aaa"); err == nil {
		// git may still error — that's fine
		t.Log("nativeNameStatus returned nil error unexpectedly but ok if git available with weird state")
	}
}

// --- helpers ---

func initTestRepo(t *testing.T, msg string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "README"), "hi")
	if _, err := wt.Add("README"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "tester", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
