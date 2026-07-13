package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manifest represents a Scoop app manifest JSON file.
// It matches the schema defined in schema.json.
// FlexibleStrings handles JSON that can be a single string or an array of strings.
type FlexibleStrings []string

func (fs *FlexibleStrings) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		return json.Unmarshal(data, (*[]string)(fs))
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*fs = []string{s}
	return nil
}

type Manifest struct {
	// === Required fields ===
	Version  string `json:"version"`
	Homepage string `json:"homepage"`
	License  any    `json:"license"` // string or LicenseObj

	// === Core resources ===
	URL  FlexibleStrings `json:"url,omitempty"`
	Hash FlexibleStrings `json:"hash,omitempty"`

	// === Execution entries ===
	Bin       any        `json:"bin,omitempty"`       // string, []string, or [][2/3]string
	Shortcuts [][]string `json:"shortcuts,omitempty"` // [[name, target, args?, icon?]]
	Persist   any        `json:"persist,omitempty"`   // string, []string, or [][2]string

	// === Dependencies ===
	Depends FlexibleStrings            `json:"depends,omitempty"`
	Suggest map[string]FlexibleStrings `json:"suggest,omitempty"`

	// === Hooks ===
	PreInstall    FlexibleStrings `json:"pre_install,omitempty"`
	PostInstall   FlexibleStrings `json:"post_install,omitempty"`
	PreUninstall  FlexibleStrings `json:"pre_uninstall,omitempty"`
	PostUninstall FlexibleStrings `json:"post_uninstall,omitempty"`

	// === Installer ===
	InnoSetup   bool         `json:"innosetup,omitempty"`
	Installer   *Installer   `json:"installer,omitempty"`
	Uninstaller *Uninstaller `json:"uninstaller,omitempty"`

	// === Environment ===
	EnvAddPath FlexibleStrings   `json:"env_add_path,omitempty"`
	EnvSet     map[string]string `json:"env_set,omitempty"`

	// === PowerShell Module ===
	PsModule *PsModule `json:"psmodule,omitempty"`

	// === Cookie (download authentication) ===
	Cookie map[string]string `json:"cookie,omitempty"`

	// === Architecture-specific overrides ===
	Architecture *struct {
		X32bit *ArchContent `json:"32bit,omitempty"`
		X64bit *ArchContent `json:"64bit,omitempty"`
		Arm64  *ArchContent `json:"arm64,omitempty"`
	} `json:"architecture,omitempty"`

	// === Auto-update ===
	Checkver   any `json:"checkver,omitempty"`   // string or CheckverObj
	Autoupdate any `json:"autoupdate,omitempty"` // AutoupdateObj

	// === Metadata ===
	Description string          `json:"description,omitempty"`
	Notes       FlexibleStrings `json:"notes,omitempty"`
	ExtractDir  any             `json:"extract_dir,omitempty"` // string or []string
	ExtractTo   any             `json:"extract_to,omitempty"`  // string or []string
	Comment     any             `json:"##,omitempty"`          // comment field
}

// ArchContent holds architecture-specific manifest fields.
// These override the top-level fields when present.
type ArchContent struct {
	URL           FlexibleStrings   `json:"url,omitempty"`
	Hash          FlexibleStrings   `json:"hash,omitempty"`
	Bin           any               `json:"bin,omitempty"`
	Shortcuts     [][]string        `json:"shortcuts,omitempty"`
	EnvAddPath    FlexibleStrings   `json:"env_add_path,omitempty"`
	EnvSet        map[string]string `json:"env_set,omitempty"`
	ExtractDir    any               `json:"extract_dir,omitempty"`
	ExtractTo     any               `json:"extract_to,omitempty"`
	PreInstall    FlexibleStrings   `json:"pre_install,omitempty"`
	PostInstall   FlexibleStrings   `json:"post_install,omitempty"`
	PreUninstall  FlexibleStrings   `json:"pre_uninstall,omitempty"`
	PostUninstall FlexibleStrings   `json:"post_uninstall,omitempty"`
	Installer     *Installer        `json:"installer,omitempty"`
	Uninstaller   *Uninstaller      `json:"uninstaller,omitempty"`
	// Fields that may also be overridden per-architecture:
	Cookie   map[string]string `json:"cookie,omitempty"`
	PsModule *PsModule `json:"psmodule,omitempty"`
	Persist any                        `json:"persist,omitempty"` // string, []string, or [][2]string
	Notes   FlexibleStrings            `json:"notes,omitempty"`
	License any                        `json:"license,omitempty"` // string or LicenseObj
	Depends FlexibleStrings            `json:"depends,omitempty"`
	Suggest map[string]FlexibleStrings `json:"suggest,omitempty"`
}

// Installer describes how to run the app's installer.
type Installer struct {
	File   string          `json:"file,omitempty"`
	Args   FlexibleStrings `json:"args,omitempty"`
	Script FlexibleStrings `json:"script,omitempty"`
	Keep   bool            `json:"keep,omitempty"`
}

// Uninstaller describes how to run the app's uninstaller.
type Uninstaller struct {
	File   string          `json:"file,omitempty"`
	Args   FlexibleStrings `json:"args,omitempty"`
	Script FlexibleStrings `json:"script,omitempty"`
}

// LicenseObj is the structured form of the license field.
type LicenseObj struct {
	Identifier string `json:"identifier"`
	URL        string `json:"url,omitempty"`
}

type PsModule struct {
	Name string `json:"name"`
}

// CheckverObj is the structured form of checkver.
type CheckverObj struct {
	Github    string `json:"github,omitempty"`
	Regex     string `json:"regex,omitempty"`
	URL       string `json:"url,omitempty"`
	JSONPath  string `json:"jsonpath,omitempty"`
	XPath     string `json:"xpath,omitempty"`
	Reverse   bool   `json:"reverse,omitempty"`
	Replace   string `json:"replace,omitempty"`
	UserAgent string `json:"useragent,omitempty"`
	Script    any    `json:"script,omitempty"` // string or []string
}

// AutoupdateObj is the structured form of autoupdate.
type AutoupdateObj struct {
	Architecture *struct {
		X32bit *AutoupdateArch `json:"32bit,omitempty"`
		X64bit *AutoupdateArch `json:"64bit,omitempty"`
		Arm64  *AutoupdateArch `json:"arm64,omitempty"`
	} `json:"architecture,omitempty"`
	URL        any `json:"url,omitempty"`
	Hash       any `json:"hash,omitempty"`
	Bin        any `json:"bin,omitempty"`
	EnvAddPath any `json:"env_add_path,omitempty"`
	ExtractDir any `json:"extract_dir,omitempty"`
	Shortcuts  any `json:"shortcuts,omitempty"`
	License    any `json:"license,omitempty"`
	Notes      any `json:"notes,omitempty"`
	Persist    any `json:"persist,omitempty"`
	PsModule   any `json:"psmodule,omitempty"`
}

// AutoupdateArch is architecture-specific autoupdate overrides.
type AutoupdateArch struct {
	URL        any `json:"url,omitempty"`
	Hash       any `json:"hash,omitempty"`
	Bin        any `json:"bin,omitempty"`
	EnvAddPath any `json:"env_add_path,omitempty"`
	ExtractDir any `json:"extract_dir,omitempty"`
	Shortcuts  any `json:"shortcuts,omitempty"`
}

// --- Parsing ---

// Parse parses a manifest from JSON bytes.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	return &m, nil
}

// ParseFile reads and parses a manifest from a file path.
func ParseFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest file %s: %w", path, err)
	}
	return Parse(data)
}

// MustParse is like Parse but panics on error (for test helpers).
func MustParse(data []byte) *Manifest {
	m, err := Parse(data)
	if err != nil {
		panic(err)
	}
	return m
}

// Validate checks required fields and basic constraints.
func (m *Manifest) Validate() error {
	if m.Version == "" {
		return fmt.Errorf("version is required")
	}
	if m.Homepage == "" {
		return fmt.Errorf("homepage is required")
	}
	if m.License == nil {
		return fmt.Errorf("license is required")
	}
	return nil
}

// --- Architecture selection ---

func (m *Manifest) GetArchContent(arch string) *ArchContent {
	if m.Architecture == nil {
		return nil
	}
	switch arch {
	case "32bit":
		return m.Architecture.X32bit
	case "64bit":
		return m.Architecture.X64bit
	case "arm64":
		return m.Architecture.Arm64
	}
	return nil
}

func (m *Manifest) GetURL(arch string) FlexibleStrings {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.URL) > 0 {
		return ac.URL
	}
	return m.URL
}

func (m *Manifest) GetHash(arch string) FlexibleStrings {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Hash) > 0 {
		return ac.Hash
	}
	return m.Hash
}

func (m *Manifest) GetBin(arch string) any {
	if ac := m.GetArchContent(arch); ac != nil && ac.Bin != nil {
		return ac.Bin
	}
	return m.Bin
}

func (m *Manifest) GetShortcuts(arch string) [][]string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Shortcuts) > 0 {
		return ac.Shortcuts
	}
	return m.Shortcuts
}

func (m *Manifest) GetEnvAddPath(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.EnvAddPath) > 0 {
		return ac.EnvAddPath
	}
	return m.EnvAddPath
}

func (m *Manifest) GetEnvSet(arch string) map[string]string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.EnvSet) > 0 {
		return ac.EnvSet
	}
	return m.EnvSet
}

func (m *Manifest) GetPreInstall(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.PreInstall) > 0 {
		return ac.PreInstall
	}
	return m.PreInstall
}

func (m *Manifest) GetPostInstall(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.PostInstall) > 0 {
		return ac.PostInstall
	}
	return m.PostInstall
}

func (m *Manifest) GetPreUninstall(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.PreUninstall) > 0 {
		return ac.PreUninstall
	}
	return m.PreUninstall
}

func (m *Manifest) GetPostUninstall(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.PostUninstall) > 0 {
		return ac.PostUninstall
	}
	return m.PostUninstall
}

func (m *Manifest) GetInstaller(arch string) *Installer {
	if ac := m.GetArchContent(arch); ac != nil && ac.Installer != nil {
		return ac.Installer
	}
	return m.Installer
}

func (m *Manifest) GetUninstaller(arch string) *Uninstaller {
	if ac := m.GetArchContent(arch); ac != nil && ac.Uninstaller != nil {
		return ac.Uninstaller
	}
	return m.Uninstaller
}

func (m *Manifest) GetExtractDir(arch string) any {
	if ac := m.GetArchContent(arch); ac != nil && ac.ExtractDir != nil {
		return ac.ExtractDir
	}
	return m.ExtractDir
}

func (m *Manifest) GetExtractTo(arch string) any {
	if ac := m.GetArchContent(arch); ac != nil && ac.ExtractTo != nil {
		return ac.ExtractTo
	}
	return m.ExtractTo
}

func (m *Manifest) GetCookie(arch string) map[string]string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Cookie) > 0 {
		return ac.Cookie
	}
	return m.Cookie
}

func (m *Manifest) GetPsModule(arch string) *PsModule {
	if ac := m.GetArchContent(arch); ac != nil && ac.PsModule != nil {
		return ac.PsModule
	}
	return m.PsModule
}

func (m *Manifest) GetPersist(arch string) any {
	if ac := m.GetArchContent(arch); ac != nil && ac.Persist != nil {
		return ac.Persist
	}
	return m.Persist
}

func (m *Manifest) GetNotes(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Notes) > 0 {
		return ac.Notes
	}
	return m.Notes
}

func (m *Manifest) GetLicense(arch string) any {
	if ac := m.GetArchContent(arch); ac != nil && ac.License != nil {
		return ac.License
	}
	return m.License
}

func (m *Manifest) GetDepends(arch string) []string {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Depends) > 0 {
		return ac.Depends
	}
	return m.Depends
}

func (m *Manifest) GetSuggest(arch string) map[string]FlexibleStrings {
	if ac := m.GetArchContent(arch); ac != nil && len(ac.Suggest) > 0 {
		return ac.Suggest
	}
	return m.Suggest
}

// --- Utility functions ---

func URLFilename(url string) string {
	url = strings.Split(url, "?")[0]
	return filepath.Base(url)
}

func AppNameFromURL(url string) string {
	name := filepath.Base(url)
	return strings.TrimSuffix(name, ".json")
}

func (m *Manifest) ResolveArch(requested string) string {
	if m.Architecture == nil {
		if len(m.URL) > 0 {
			return requested
		}
		return ""
	}
	switch requested {
	case "arm64":
		if m.Architecture.Arm64 != nil {
			return "arm64"
		}
	case "64bit":
		if m.Architecture.X64bit != nil {
			return "64bit"
		}
	case "32bit":
		if m.Architecture.X32bit != nil {
			return "32bit"
		}
	}
	if m.Architecture.X64bit != nil {
		return "64bit"
	}
	if m.Architecture.X32bit != nil {
		return "32bit"
	}
	if m.Architecture.Arm64 != nil {
		return "arm64"
	}
	if len(m.URL) > 0 {
		return requested
	}
	return ""
}

func (m *Manifest) HashForURL(url string, arch string) string {
	urls := m.GetURL(arch)
	hashes := m.GetHash(arch)
	for i, u := range urls {
		if u == url && i < len(hashes) {
			return hashes[i]
		}
	}
	fname := URLFilename(url)
	for i, u := range urls {
		if URLFilename(u) == fname && i < len(hashes) {
			return hashes[i]
		}
	}
	return ""
}

func BinEntries(bin any) [][3]string {
	var result [][3]string
	if bin == nil {
		return result
	}
	switch b := bin.(type) {
	case string:
		result = append(result, [3]string{b, nameFromPath(b), ""})
	case []any:
		for _, item := range b {
			switch v := item.(type) {
			case string:
				result = append(result, [3]string{v, nameFromPath(v), ""})
			case []any:
				var entry [3]string
				for i, e := range v {
					if s, ok := e.(string); ok && i < 3 {
						entry[i] = s
					}
				}
				if entry[1] == "" {
					entry[1] = nameFromPath(entry[0])
				}
				result = append(result, entry)
			}
		}
	}
	return result
}

func nameFromPath(p string) string {
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// GenerateUserManifest generates a manifest for a specific version by merging
// the autoupdate template over the install properties and substituting Scoop's
// standard version variables. Hashes are intentionally cleared; callers must
// compute and persist hashes for the generated URLs before installation.
func GenerateUserManifest(m *Manifest, targetVersion string) ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decoding manifest: %w", err)
	}
	auto, ok := root["autoupdate"].(map[string]any)
	if !ok || len(auto) == 0 {
		return nil, fmt.Errorf("manifest does not have autoupdate capability")
	}

	properties := []string{"url", "bin", "env_add_path", "extract_dir", "extract_to", "shortcuts", "license", "notes", "persist", "psmodule", "installer"}
	for _, property := range properties {
		if value, exists := auto[property]; exists {
			root[property] = value
		}
	}
	delete(root, "hash")

	if autoArch, ok := auto["architecture"].(map[string]any); ok {
		rootArch, _ := root["architecture"].(map[string]any)
		if rootArch == nil {
			rootArch = make(map[string]any)
		}
		for _, arch := range []string{"32bit", "64bit", "arm64"} {
			override, ok := autoArch[arch].(map[string]any)
			if !ok {
				continue
			}
			base, _ := rootArch[arch].(map[string]any)
			if base == nil {
				base = make(map[string]any)
			}
			delete(base, "hash")
			for _, property := range properties {
				if value, exists := override[property]; exists {
					base[property] = value
				}
			}
			rootArch[arch] = base
		}
		root["architecture"] = rootArch
	}

	root["version"] = targetVersion
	root = substituteVersionValue(root, versionSubstitutions(targetVersion)).(map[string]any)
	return json.MarshalIndent(root, "", "  ")
}

func versionSubstitutions(version string) map[string]string {
	normalize := func(separator string) string {
		r := strings.NewReplacer(".", separator, "_", separator, "-", separator)
		return r.Replace(version)
	}
	first := strings.Split(version, "-")[0]
	lastParts := strings.Split(version, "-")
	parts := strings.Split(first, ".")
	part := func(index int) string {
		if index < len(parts) {
			return parts[index]
		}
		return ""
	}
	return map[string]string{
		"${version}":         version,
		"$version":           version,
		"$dotVersion":        normalize("."),
		"$underscoreVersion": normalize("_"),
		"$dashVersion":       normalize("-"),
		"$cleanVersion":      normalize(""),
		"$majorVersion":      part(0),
		"$minorVersion":      part(1),
		"$patchVersion":      part(2),
		"$buildVersion":      part(3),
		"$preReleaseVersion": lastParts[len(lastParts)-1],
	}
}

func substituteVersionValue(value any, substitutions map[string]string) any {
	switch typed := value.(type) {
	case string:
		// Longest keys first prevents $version from partially consuming
		// ${version} or another future variable with a shared prefix.
		keys := make([]string, 0, len(substitutions))
		for key := range substitutions {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, key := range keys {
			typed = strings.ReplaceAll(typed, key, substitutions[key])
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = substituteVersionValue(typed[i], substitutions)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = substituteVersionValue(item, substitutions)
		}
		return typed
	default:
		return value
	}
}

func NormalizeStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case string:
		return []string{val}
	case []any:
		var result []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	}
	return nil
}
