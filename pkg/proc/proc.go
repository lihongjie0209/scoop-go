// Package proc detects running processes for Scoop update/uninstall guards.
// It avoids PowerShell; Windows uses CreateToolhelp32Snapshot APIs.
package proc

import (
	"path/filepath"
	"strings"
)

// PathEnumerator returns executable paths of running processes.
type PathEnumerator func() ([]string, error)

// ImageEnumerator returns process image names (e.g. "git.exe").
type ImageEnumerator func() ([]string, error)

// AnyRunningUnderPath reports whether any process executable lives under rootDir.
// On enumerator errors it returns false (safe to proceed), matching Scoop's
// "if we can't check, continue" behavior.
func AnyRunningUnderPath(rootDir string, list PathsEnumerator) (bool, []string) {
	if list == nil {
		list = ListProcessPaths
	}
	paths, err := list()
	if err != nil {
		return false, nil
	}
	rootDir = filepath.Clean(rootDir)
	var names []string
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if pathIsUnder(p, rootDir) {
			base := filepath.Base(p)
			if !seen[base] {
				seen[base] = true
				names = append(names, base)
			}
		}
	}
	return len(names) > 0, names
}

// PathsEnumerator is an alias kept for readable signatures.
type PathsEnumerator = PathEnumerator

// AnyRunningByImageName reports whether any of the given app names (with or
// without .exe) appear in the process image list.
func AnyRunningByImageName(names []string, list ImageEnumerator) bool {
	if list == nil {
		list = ListProcessImages
	}
	images, err := list()
	if err != nil {
		return false
	}
	want := map[string]bool{}
	for _, n := range names {
		if img := normalizeImage(n); img != "" {
			want[img] = true
		}
	}
	for _, img := range images {
		if want[normalizeImage(img)] {
			return true
		}
	}
	return false
}

func normalizeImage(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ToLower(filepath.Base(name))
	if !strings.HasSuffix(name, ".exe") {
		name += ".exe"
	}
	return name
}

func pathIsUnder(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	// Windows paths are case-insensitive
	if equalFoldPath(path, root) {
		return true
	}
	rel, err := filepath.Rel(normalizePathCase(root), normalizePathCase(path))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func equalFoldPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func normalizePathCase(p string) string {
	// Lowercase for Rel on Windows; on Unix keep as-is (EqualFold still used for root)
	if filepath.Separator == '\\' {
		return strings.ToLower(p)
	}
	return p
}
