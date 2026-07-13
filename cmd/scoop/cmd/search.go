package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/scoopinstaller/scoop-go/pkg/bucket"
	"github.com/scoopinstaller/scoop-go/pkg/db"
	"github.com/scoopinstaller/scoop-go/pkg/manifest"
	"github.com/spf13/cobra"
)

var searchFlags struct {
	local bool
}

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search available apps",
	Long:  "Searches for apps that are available to install from any added bucket. Matches against app names, binaries, and shortcuts.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = args[0]
		}

		var (
			found int
			err   error
		)
		if db.IsEnabled() {
			found, err = searchWithCache(strings.ToLower(query))
		} else {
			found, err = searchLocal(query)
		}
		if err != nil {
			return err
		}

		if found == 0 && query != "" {
			if !searchFlags.local {
				fmt.Println("Searching remote buckets...")
				remoteFound, remoteErr := searchRemoteBuckets(query)
				if remoteErr != nil {
					app.LogWarn("Remote search failed: %v", remoteErr)
				} else if remoteFound > 0 {
					return nil
				}
			}
			app.LogInfo("No apps found matching '%s'", query)
		} else if found > 0 {
			app.LogInfo("%d app(s) found", found)
		}
		return nil
	},
}

func searchLocal(query string) (int, error) {
	localBuckets := bucket.ListLocal()
	if len(localBuckets) == 0 {
		app.LogWarn("No buckets found. Add a bucket first with 'scoop bucket add main'")
		return 0, nil
	}

	// Without SQLite, PowerShell uses regex against names and binaries.
	var re *regexp.Regexp
	if query != "" {
		var err error
		re, err = regexp.Compile("(?i)" + query)
		if err != nil {
			// Fallback to literal substring if invalid regex
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
		}
	}

	found := 0
	for _, b := range localBuckets {
		manifestDir := bucket.ManifestDir(b.Name)
		entries, err := os.ReadDir(manifestDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			name := strings.TrimSuffix(entry.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(manifestDir, entry.Name()))
			if err != nil {
				continue
			}
			m, err := manifest.Parse(data)
			if err != nil || m == nil {
				continue
			}

			matchedName := query == "" || (re != nil && re.MatchString(name))
			matchedBins := []string{}
			if !matchedName && re != nil {
				matchedBins = matchingBins(m, re)
			}
			if !matchedName && len(matchedBins) == 0 {
				// Also try shortcuts
				if re != nil && matchingShortcuts(m, re) {
					matchedName = true
				} else {
					continue
				}
			}

			installed := ""
			if isAppInstalledLocally(name) {
				installed = " [installed]"
			}

			bins := ""
			if len(matchedBins) > 0 {
				bins = " --> includes '" + strings.Join(matchedBins, "', '") + "'"
			} else {
				// Show all bins when name matched (compact)
				all := allBinNames(m)
				if len(all) > 0 {
					bins = " (bin: " + strings.Join(all, ", ") + ")"
				}
			}

			fmt.Printf("%-30s %s bucket%s%s\n", name, b.Name, bins, installed)
			found++
		}
	}

	return found, nil
}

func allBinNames(m *manifest.Manifest) []string {
	var names []string
	seen := map[string]bool{}
	for _, arch := range []string{"64bit", "32bit", "arm64"} {
		for _, be := range manifest.BinEntries(m.GetBin(arch)) {
			n := be[1]
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

func matchingBins(m *manifest.Manifest, re *regexp.Regexp) []string {
	var matched []string
	seen := map[string]bool{}
	for _, arch := range []string{"64bit", "32bit", "arm64"} {
		for _, be := range manifest.BinEntries(m.GetBin(arch)) {
			target := be[0]
			alias := be[1]
			base := filepath.Base(target)
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			if re.MatchString(stem) || re.MatchString(alias) || re.MatchString(base) {
				label := alias
				if label == "" {
					label = base
				}
				if !seen[label] {
					seen[label] = true
					matched = append(matched, label)
				}
			}
		}
	}
	return matched
}

func matchingShortcuts(m *manifest.Manifest, re *regexp.Regexp) bool {
	for _, arch := range []string{"64bit", "32bit", "arm64"} {
		for _, sc := range m.GetShortcuts(arch) {
			for _, part := range sc {
				if re.MatchString(part) {
					return true
				}
			}
		}
	}
	return false
}

func isAppInstalledLocally(name string) bool {
	for _, g := range []bool{false, true} {
		if _, err := os.Stat(app.AppCurrentDir(name, g)); err == nil {
			return true
		}
	}
	return false
}

func searchWithCache(query string) (int, error) {
	results, err := db.Search(query)
	if err != nil {
		return 0, fmt.Errorf("search failed: %w", err)
	}
	for _, r := range results {
		installed := ""
		if isAppInstalledLocally(r.Name) {
			installed = " [installed]"
		}
		bins := ""
		if r.Binary != "" {
			bins = " (bin: " + r.Binary + ")"
		}
		fmt.Printf("%-30s %s bucket%s%s\n", r.Name, r.Bucket, bins, installed)
	}
	return len(results), nil
}

type githubCodeSearchResponse struct {
	Items []struct {
		Name       string `json:"name"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"items"`
}

func searchRemoteBuckets(query string) (int, error) {
	searchURL := "https://api.github.com/search/code?q=" + url.QueryEscape(query+" in:path extension:json org:ScoopInstaller")
	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scoop-go")
	if token := app.Config().Config().GH_TOKEN; token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub search API returned %s", resp.Status)
	}

	var payload githubCodeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}

	seen := map[string]bool{}
	found := 0
	for _, item := range payload.Items {
		appName := strings.TrimSuffix(item.Name, filepath.Ext(item.Name))
		if appName == "" || item.Repository.FullName == "" {
			continue
		}
		key := item.Repository.FullName + ":" + appName
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Printf("Bucket: %s — %s\n", item.Repository.FullName, appName)
		found++
	}

	return found, nil
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().BoolVarP(&searchFlags.local, "local", "l", false, "Only search local buckets")
}
