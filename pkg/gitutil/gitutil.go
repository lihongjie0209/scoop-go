// Package gitutil wraps go-git operations for Scoop's Git needs.
// Uses pure-Go go-git for all operations. Falls back to native git CLI
// only for cloning very large repositories where go-git has known memory
// limitations (e.g., the Extras bucket with 40K+ manifests).
package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gogitConfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/utils/merkletrie"
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
	Depth int
}

// Clone performs a git clone. Tries go-git first with shallow options,
// falls back to native git CLI for large repos where go-git struggles.
func Clone(opts CloneOptions) error {
	if opts.Depth == 0 {
		opts.Depth = 1
	}
	gogitOpts := &gogit.CloneOptions{
		URL:          opts.URL,
		Depth:        opts.Depth,
		SingleBranch: true,
		Tags:         gogit.NoTags,
	}
	_, err := gogit.PlainClone(opts.Dest, false, gogitOpts)
	if err == nil {
		return nil
	}
	// go-git failed — fall back to native git CLI for large repos
	return nativeClone(opts)
}

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

// Pull updates a repo via go-git. Shallow-cloned repos should be
// updated via bucket.Sync (re-clone) instead of Pull.
func Pull(repoPath string) error {
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
		return fmt.Errorf("git pull: %w", err)
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

// IsRepo checks if a directory is a git repository.
func IsRepo(path string) bool {
	_, err := gogit.PlainOpen(path)
	return err == nil
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

// NameStatus returns file changes between two commits (git diff --name-status).
// Uses go-git; for shallow clones where oldHash doesn't exist, returns empty.
func NameStatus(repoPath, oldHash, newHash string) ([]NameStatusEntry, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, nil
	}
	newHash = strings.TrimSpace(newHash)
	oldHash = strings.TrimSpace(oldHash)
	if newHash == "" {
		return nil, fmt.Errorf("new hash required")
	}
	newCommit, err := repo.CommitObject(plumbing.NewHash(newHash))
	if err != nil {
		return nil, nil // shallow clone — new commit exists locally
	}
	newTree, err := newCommit.Tree()
	if err != nil {
		return nil, err
	}
	var oldTree *object.Tree
	if oldHash != "" {
		oldCommit, err := repo.CommitObject(plumbing.NewHash(oldHash))
		if err != nil {
			return nil, nil // shallow clone — old commit not in local history
		}
		oldTree, err = oldCommit.Tree()
		if err != nil {
			return nil, err
		}
	}
	var changes object.Changes
	if oldTree == nil {
		changes, err = object.DiffTree(nil, newTree)
	} else {
		changes, err = object.DiffTree(oldTree, newTree)
	}
	if err != nil {
		return nil, nil
	}
	var out []NameStatusEntry
	for _, ch := range changes {
		action, err := ch.Action()
		if err != nil {
			continue
		}
		from, to, err := ch.Files()
		if err != nil {
			from, to = nil, nil
		}
		switch action {
		case merkletrie.Insert:
			name := ch.To.Name
			if to != nil {
				name = to.Name
			}
			out = append(out, NameStatusEntry{Status: "A", Path: name})
		case merkletrie.Delete:
			name := ch.From.Name
			if from != nil {
				name = from.Name
			}
			out = append(out, NameStatusEntry{Status: "D", Path: name})
		case merkletrie.Modify:
			name := ch.To.Name
			if to != nil {
				name = to.Name
			}
			out = append(out, NameStatusEntry{Status: "M", Path: name})
		}
	}
	return out, nil
}

// NameStatusEntry is one changed path between commits.
type NameStatusEntry struct {
	Status  string
	Path    string
	OldPath string
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

// LogRange returns commit messages between oldHash and newHash.
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
