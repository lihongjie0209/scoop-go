// Package gitutil wraps go-git operations for Scoop's Git needs.
// For large repositories where go-git has known limitations (OOM, unexpected EOF),
// it falls back to the native git CLI.
package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gogitConfig "github.com/go-git/go-git/v5/config"
)

// CheckAvailable verifies go-git is functional.
func CheckAvailable() error {
	return nil // Always available — pure Go
}

// CloneOptions holds git clone parameters.
type CloneOptions struct {
	URL   string
	Dest  string
	Quiet bool
	// Depth limits the clone to the specified number of commits (shallow clone).
	// 0 means full history. For large repos, Depth: 1 is recommended to avoid
	// go-git memory issues.
	Depth int
}

// Clone performs a git clone. For large repositories it first attempts a
// shallow go-git clone, and falls back to native git CLI if go-git fails.
func Clone(opts CloneOptions) error {
	if opts.Depth == 0 {
		opts.Depth = 1 // Default to shallow clone for performance
	}

	// Try go-git first with shallow clone options
	gogitOpts := &gogit.CloneOptions{
		URL:          opts.URL,
		Depth:        opts.Depth,
		SingleBranch: true,
		Tags:         gogit.NoTags,
	}
	if opts.Quiet {
		gogitOpts.Progress = nil
	}

	_, err := gogit.PlainClone(opts.Dest, false, gogitOpts)
	if err == nil {
		return nil
	}

	return nativeClone(opts)
}

// nativeClone falls back to the native git command-line tool.
func nativeClone(opts CloneOptions) error {
	args := []string{"clone"}
	if opts.Quiet {
		args = append(args, "-q")
	}
	if opts.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.Depth))
	}
	args = append(args, "--single-branch", "--no-tags", opts.URL, opts.Dest)

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone (native fallback): %w", err)
	}
	return nil
}

// Pull opens a repo and pulls latest changes.
// For shallow clones (Depth > 0), it uses native git for reliable pull/fetch behavior.
func Pull(repoPath string) error {
	// Try go-git first
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("opening repo: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return err
	}

	err = wt.Pull(&gogit.PullOptions{})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return nativePull(repoPath)
	}
	return nil
}

// nativePull updates a repository using the native git CLI.
func nativePull(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull (native fallback): %w", err)
	}
	return nil
}

// Fetch fetches from origin without merging.
func Fetch(repoPath string) error {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return err
	}

	remote, err := repo.Remote("origin")
	if err != nil {
		return err
	}

	err = remote.Fetch(&gogit.FetchOptions{})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return fmt.Errorf("git fetch: %w", err)
	}
	return nil
}

// CurrentBranch returns the current branch name.
func CurrentBranch(repoPath string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", err
	}

	ref, err := repo.Head()
	if err != nil {
		return "", err
	}

	if ref.Name().IsBranch() {
		return ref.Name().Short(), nil
	}
	return ref.Name().String(), nil
}

// HeadHash returns the full SHA of HEAD.
func HeadHash(repoPath string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", err
	}

	ref, err := repo.Head()
	if err != nil {
		return "", err
	}

	return ref.Hash().String(), nil
}

// RemoteURL returns the origin remote URL.
func RemoteURL(repoPath string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", err
	}

	remote, err := repo.Remote("origin")
	if err != nil {
		return "", err
	}

	urls := remote.Config().URLs
	if len(urls) > 0 {
		return urls[0], nil
	}
	return "", fmt.Errorf("no remote URL configured")
}

// CommitsAhead returns the number of commits ahead of a base ref.
func CommitsAhead(repoPath, baseRef string) (int, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return 0, err
	}

	baseHash := plumbing.NewHash(baseRef)

	head, err := repo.Head()
	if err != nil {
		return 0, err
	}

	cIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return 0, err
	}

	count := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == baseHash {
			return fmt.Errorf("stop")
		}
		count++
		return nil
	})
	if err != nil && err.Error() != "stop" {
		return 0, err
	}

	return count, nil
}

// CommitsBehindHead returns commits between HEAD and a target ref (HEAD..target).
func CommitsBehindHead(repoPath, targetRef string) (int, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return 0, err
	}

	head, err := repo.Head()
	if err != nil {
		return 0, err
	}

	targetHash := plumbing.NewHash(targetRef)

	cIter, err := repo.Log(&gogit.LogOptions{From: targetHash})
	if err != nil {
		return 0, err
	}

	count := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == head.Hash() {
			return fmt.Errorf("stop")
		}
		count++
		return nil
	})
	if err != nil && err.Error() != "stop" {
		return 0, err
	}

	return count, nil
}

// HasUncommittedChanges checks if there are unstaged changes.
func HasUncommittedChanges(repoPath string) (bool, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return false, err
	}

	wt, err := repo.Worktree()
	if err != nil {
		return false, err
	}

	status, err := wt.Status()
	if err != nil {
		return false, err
	}

	return !status.IsClean(), nil
}

// LsRemote verifies a remote repository exists and is accessible.
func LsRemote(url string) error {
	ep, err := transport.NewEndpoint(url)
	if err != nil {
		return fmt.Errorf("invalid remote URL: %w", err)
	}

	_, err = gogit.NewRemote(nil, &gogitConfig.RemoteConfig{
		Name: "origin",
		URLs: []string{ep.String()},
	}).List(&gogit.ListOptions{})
	if err != nil {
		return fmt.Errorf("remote not accessible: %w", err)
	}

	return nil
}

// IsRepo checks if a directory is a git repository.
func IsRepo(path string) bool {
	_, err := gogit.PlainOpen(path)
	return err == nil
}

// LogRange returns the one-line commit messages between oldHash and newHash.
func LogRange(repoPath, oldHash, newHash string) ([]string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, err
	}

	old := plumbing.NewHash(oldHash)
	new := plumbing.NewHash(newHash)

	cIter, err := repo.Log(&gogit.LogOptions{From: new})
	if err != nil {
		return nil, err
	}

	var messages []string
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == old {
			return fmt.Errorf("stop")
		}
		msg := strings.SplitN(c.Message, "\n", 2)[0]
		shortHash := c.Hash.String()[:7]
		messages = append(messages, fmt.Sprintf("%s %s", shortHash, msg))
		return nil
	})
	if err != nil && err.Error() != "stop" {
		return nil, err
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}
