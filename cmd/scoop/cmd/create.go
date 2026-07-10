package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

//

var createCmd = &cobra.Command{
	Use:   "create <url>",
	Short: "Create a custom app manifest",
	Long:  "Creates a minimal Scoop manifest JSON file from a download URL.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawURL := args[0]

		// Validate URL
		parsed, err := url.Parse(rawURL)
		if err != nil || !parsed.IsAbs() {
			return fmt.Errorf("'%s' is not a valid URL", rawURL)
		}

		// Get path segments
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

		// Choose app name
		appName := chooseItem(segments, "App name")
		if appName == "" {
			// Default: last segment without extension
			last := segments[len(segments)-1]
			if idx := strings.LastIndex(last, "."); idx > 0 {
				appName = last[:idx]
			} else {
				appName = last
			}
		}

		// Choose version
		version := chooseItem(segments, "Version")
		if version == "" {
			version = "1.0.0"
		}

		// Build manifest
		manifest := map[string]interface{}{
			"version":  version,
			"homepage": "",
			"license":  "",
			"url":      rawURL,
			"hash":     "",
			"bin":      "",
			"depends":  "",
		}

		// Write to file
		manifestPath := filepath.Join(".", appName+".json")
		data, err := json.MarshalIndent(manifest, "", "    ")
		if err != nil {
			return fmt.Errorf("marshaling manifest: %w", err)
		}

		if err := os.WriteFile(manifestPath, data, 0644); err != nil {
			return fmt.Errorf("writing manifest: %w", err)
		}

		fmt.Printf("Created '%s'.\n", manifestPath)
		return nil
	},
}

// chooseItem displays numbered segements and prompts for selection or custom input.
func chooseItem(segments []string, prompt string) string {
	if len(segments) == 0 {
		return ""
	}

	for i, seg := range segments {
		fmt.Printf("  %d) %s\n", i+1, seg)
	}
	fmt.Printf("  (or type a custom value)\n")
	fmt.Printf("%s [1-%d]: ", prompt, len(segments))

	r := bufio.NewReader(os.Stdin)
	input, _ := r.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return ""
	}

	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(segments) {
		return segments[n-1]
	}

	return input
}

func init() {
	rootCmd.AddCommand(createCmd)
}
