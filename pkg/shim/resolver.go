// Package shim resolves Shim targets — reading .shim files, wrapper scripts,
// and returning the actual executable path they point to.
// Mirrors core.ps1 Get-ShimTarget and related functions.
package shim

import (
	"bufio"
	"os"
	"strings"
)

// ResolveShimTarget reads a .shim file and returns the path it points to.
// .shim format:
//
//	path = "C:\...\program.exe"
//	args = ...
func ResolveShimTarget(shimPath string) string {
	f, err := os.Open(shimPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "path = ") {
			val := strings.TrimPrefix(line, "path = ")
			val = strings.Trim(val, `"`)
			return val
		}
	}
	return ""
}

// ResolveWrapperTarget reads a .cmd or .ps1 wrapper and extracts the
// target path from the first comment line.
// Format: @rem C:\...\program.exe  (for .cmd)
// Format: # C:\...\program.exe      (for .ps1, sh scripts)
func ResolveWrapperTarget(wrapperPath string) string {
	f, err := os.Open(wrapperPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Check for @rem or # prefix
		line = strings.TrimPrefix(line, "@rem ")
		line = strings.TrimPrefix(line, "# ")
		line = strings.TrimSpace(line)

		// Validate it looks like a path
		if strings.Contains(line, "\\") || strings.Contains(line, "/") || strings.Contains(line, ":") {
			return line
		}
	}
	return ""
}

// ResolveShimTargetFromExe resolves the target for a .exe shim by reading
// its accompanying .shim file.
func ResolveShimTargetFromExe(exePath string) string {
	shimPath := strings.TrimSuffix(exePath, ".exe") + ".shim"
	return ResolveShimTarget(shimPath)
}
