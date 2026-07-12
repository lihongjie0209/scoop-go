package install

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

// ShouldShowManifest reports whether install should display the manifest first.
func ShouldShowManifest(enabled bool) bool {
	return enabled
}

// FormatManifestReview renders a human-readable manifest summary for review.
func FormatManifestReview(m *manifest.Manifest, appName string) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Manifest: %s.json\n", appName)
	fmt.Fprintf(&b, "  Version:  %s\n", m.Version)
	fmt.Fprintf(&b, "  Homepage: %s\n", m.Homepage)
	if m.Description != "" {
		fmt.Fprintf(&b, "  Summary:  %s\n", m.Description)
	}
	urls := m.GetURL("64bit")
	if len(urls) == 0 {
		urls = m.URL
	}
	for i, u := range urls {
		if i == 0 {
			fmt.Fprintf(&b, "  URL:      %s\n", u)
		} else {
			fmt.Fprintf(&b, "            %s\n", u)
		}
	}
	return b.String()
}

// ConfirmManifestInstall prints a manifest review and asks the user to continue.
// Empty input or y/Y continues; n/N cancels. Returns ok=false when cancelled.
func ConfirmManifestInstall(m *manifest.Manifest, appName string, in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, FormatManifestReview(m, appName))
	fmt.Fprint(out, "Continue installation? [Y/n] ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.TrimSpace(line)
	if answer == "" || strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return true, nil
	}
	if strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
		return false, nil
	}
	// Unknown answer: default to cancel for safety? PS only checks n/N.
	// Other answers continue like empty.
	return true, nil
}
