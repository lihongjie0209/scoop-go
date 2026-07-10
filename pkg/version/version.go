// Package version provides SemVer-compatible version comparison.
// It mirrors lib/versions.ps1 from the original Scoop.
package version

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Compare compares two versions according to SemVer-like rules.
// Returns:
//
//	0 if difference == reference
//	1 if difference > reference
//	-1 if difference < reference
func Compare(reference, difference string) int {
	if reference == "" && difference == "" {
		return 0
	}
	if difference == "" {
		return -1
	}
	if reference == "" {
		return 1
	}

	// Handle nightly versions
	if reference == "nightly" && difference == "nightly" {
		return 0
	}

	// Replace '+' with '-' for post-release handling (mirrors PowerShell behavior)
	ref := strings.ReplaceAll(reference, "+", "-")
	diff := strings.ReplaceAll(difference, "+", "-")

	if diff == ref {
		return 0
	}

	// Split and compare
	refParts := splitVersion(ref, "-")
	diffParts := splitVersion(diff, "-")

	maxLen := int(math.Max(float64(len(refParts)), float64(len(diffParts))))

	for i := 0; i < maxLen; i++ {
		// '1.1-alpha' is less than '1.1'
		if i >= len(refParts) {
			if isPreRelease(diffParts, i) {
				return -1
			}
			return 1
		}
		// '1.1' is greater than '1.1-beta'
		if i >= len(diffParts) {
			if isPreRelease(refParts, i) {
				return 1
			}
			return -1
		}

		refStr := fmt.Sprintf("%v", refParts[i])
		diffStr := fmt.Sprintf("%v", diffParts[i])

		// Sub-version comparison with '.'
		if hasSubDelimiter(refStr, '.') || hasSubDelimiter(diffStr, '.') {
			result := compareSubVersion(refStr, diffStr, ".")
			if result != 0 {
				return result
			}
			continue
		}

		// Sub-version comparison with '_'
		if hasSubDelimiter(refStr, '_') || hasSubDelimiter(diffStr, '_') {
			result := compareSubVersion(refStr, diffStr, "_")
			if result != 0 {
				return result
			}
			continue
		}

		// Convert to comparable types
		rVal, rIsNum := toNumeric(refParts[i])
		dVal, dIsNum := toNumeric(diffParts[i])

		// String comparison vs numeric
		if !rIsNum && dIsNum {
			return -1 // string < number (e.g., "alpha" < 1)
		}
		if rIsNum && !dIsNum {
			return 1 // number > string
		}

		// Both numeric — direct comparison
		if rIsNum && dIsNum {
			if dVal > rVal {
				return 1
			}
			if dVal < rVal {
				return -1
			}
		}

		// Both strings — lexicographic comparison
		if !rIsNum && !dIsNum {
			rs := fmt.Sprintf("%v", refParts[i])
			ds := fmt.Sprintf("%v", diffParts[i])
			if ds > rs {
				return 1
			}
			if ds < rs {
				return -1
			}
		}
	}

	return 0
}

// IsNightly returns true if the manifest version is "nightly".
func IsNightly(version string) bool {
	return version == "nightly"
}

// FormatNightlyVersion creates a nightly version string with a date.
func FormatNightlyVersion(dateStr string) string {
	return "nightly-" + dateStr
}

// isPreRelease checks if a version part at index indicates a pre-release.
func isPreRelease(parts []any, index int) bool {
	if index >= len(parts) {
		return false
	}
	s := fmt.Sprintf("%v", parts[index])
	re := regexp.MustCompile(`(?i)alpha|beta|rc|pre`)
	return re.MatchString(s)
}

// hasSubDelimiter checks if a version part contains the given delimiter.
func hasSubDelimiter(part string, delim rune) bool {
	return strings.ContainsRune(part, delim)
}

// compareSubVersion compares two version parts using a sub-delimiter.
func compareSubVersion(ref, diff, delim string) int {
	refSub := splitVersion(ref, delim)
	diffSub := splitVersion(diff, delim)

	maxLen := int(math.Max(float64(len(refSub)), float64(len(diffSub))))

	for i := 0; i < maxLen; i++ {
		if i >= len(refSub) {
			return 1
		}
		if i >= len(diffSub) {
			return -1
		}

		rVal, _ := toNumeric(refSub[i])
		dVal, _ := toNumeric(diffSub[i])

		if dVal > rVal {
			return 1
		}
		if dVal < rVal {
			return -1
		}
	}

	return 0
}

// splitVersion splits a version string by delimiter and processes each part.
func splitVersion(version, delim string) []any {
	var parts []any

	// Insert delimiters around alphabetic sequences for fine-grained comparison
	// e.g., "1.0.0beta" -> "1.0.0-beta-"
	re := regexp.MustCompile(`[a-zA-Z]+`)
	version = re.ReplaceAllStringFunc(version, func(match string) string {
		return delim + match + delim
	})

	rawParts := strings.Split(version, delim)
	for _, p := range rawParts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Try to parse as number
		if n, err := strconv.ParseInt(p, 10, 64); err == nil {
			parts = append(parts, n)
		} else if f, err := strconv.ParseFloat(p, 64); err == nil {
			parts = append(parts, f)
		} else {
			parts = append(parts, p)
		}
	}

	return parts
}

// toNumeric converts a value to a numeric representation for comparison.
func toNumeric(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int64:
		return float64(val), true
	case int:
		return float64(val), true
	default:
		return 0, false
	}
}
