// Package config handles Scoop configuration.
// It reads/writes ~/.config/scoop/config.json (or the XDG_CONFIG_HOME equivalent).
// Config values use case-insensitive matching, mirroring the PowerShell behavior.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the scoop configuration file structure.
// Fields map directly to the PowerShell config keys.
type Config struct {
	// Paths
	RootPath   string `json:"root_path,omitempty"`
	GlobalPath string `json:"global_path,omitempty"`
	CachePath  string `json:"cache_path,omitempty"`

	// Network
	Proxy string `json:"proxy,omitempty"`

	// Repository
	SCOOPRepo   string `json:"scoop_repo,omitempty"`
	SCOOPBranch string `json:"scoop_branch,omitempty"`
	ScoopGoRepo string `json:"scoop_go_repo,omitempty"`

	// GitHub
	GH_TOKEN string `json:"gh_token,omitempty"`

	// Aria2
	Aria2Enabled          *bool    `json:"aria2-enabled,omitempty"`
	Aria2WarningEnabled   *bool    `json:"aria2-warning-enabled,omitempty"`
	Aria2RetryWait        int      `json:"aria2-retry-wait,omitempty"`
	Aria2Split            int      `json:"aria2-split,omitempty"`
	Aria2MaxConnPerServer int      `json:"aria2-max-connection-per-server,omitempty"`
	Aria2MinSplitSize     string   `json:"aria2-min-split-size,omitempty"`
	Aria2Options          []string `json:"aria2-options,omitempty"`
	UseExternal7Zip       bool     `json:"use_external_7zip,omitempty"`
	UseLessMSI            bool     `json:"use_lessmsi,omitempty"`

	// Update behavior
	ForceUpdate   bool  `json:"force_update,omitempty"`
	ShowUpdateLog *bool `json:"show_update_log,omitempty"`
	ShowManifest  bool  `json:"show_manifest,omitempty"`
	UpdateNightly bool  `json:"update_nightly,omitempty"`

	// Search cache
	UseSQLiteCache bool `json:"use_sqlite_cache,omitempty"`

	// Shim / Junction
	NoJunction bool   `json:"no_junction,omitempty"`
	Shim       string `json:"shim,omitempty"`

	// Debug
	Debug bool `json:"debug,omitempty"`

	// Architecture
	DefaultArchitecture string `json:"default_architecture,omitempty"`

	// UI
	CatStyle string `json:"cat_style,omitempty"`

	// GitHub proxy (mirror for faster downloads in some regions)

	// Security / private hosts
	PrivateHosts []PrivateHostRule `json:"private_hosts,omitempty"`

	// Path isolation
	UseIsolatedPath interface{} `json:"use_isolated_path,omitempty"` // bool | string

	// Runtime behavior
	IgnoreRunningProcesses bool   `json:"ignore_running_processes,omitempty"`
	HoldUpdateUntil        string `json:"hold_update_until,omitempty"`
	AutostashOnConflict    bool   `json:"autostash_on_conflict,omitempty"`

	// Last update timestamp (ISO 8601)
	LastUpdate string `json:"last_update,omitempty"`

	// VirusTotal API key
	VTApiKey string `json:"virustotal_api_key,omitempty"`

	// Aliases
	Alias map[string]string `json:"alias,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for backward compatibility.
// It normalizes legacy snake_case aria2 keys to the current kebab-case format.
func (c *Config) UnmarshalJSON(data []byte) error {
	// Unmarshal into a map first to inspect and normalize keys
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Normalize snake_case aria2 keys to kebab-case for backward compatibility
	aria2SnakeToKebab := map[string]string{
		"aria2_enabled":                   "aria2-enabled",
		"aria2_warning_enabled":           "aria2-warning-enabled",
		"aria2_retry_wait":                "aria2-retry-wait",
		"aria2_split":                     "aria2-split",
		"aria2_max_connection_per_server": "aria2-max-connection-per-server",
		"aria2_min_split_size":            "aria2-min-split-size",
		"aria2_options":                   "aria2-options",
	}

	for snake, kebab := range aria2SnakeToKebab {
		if val, ok := raw[snake]; ok {
			if _, exists := raw[kebab]; !exists {
				raw[kebab] = val
			}
			delete(raw, snake)
		}
	}

	// Re-marshal and unmarshal into the struct using a type alias to avoid recursion
	normalized, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	type Alias Config
	return json.Unmarshal(normalized, (*Alias)(c))
}

// PrivateHostRule defines a rule for private hosts requiring custom headers.
type PrivateHostRule struct {
	Match   string `json:"match"`
	Headers string `json:"headers"`
}

// ConfigChangeHook is a function type for handling side effects of config changes.
// The app layer registers a hook to perform actions like SQLite DB rebuild
// without creating circular imports between config and db packages.
type ConfigChangeHook func(name, value string)

var (
	// completeConfigHook is the registered side-effect handler, or nil.
	completeConfigHook ConfigChangeHook
)

// SetConfigChangeHook registers a hook that CompleteConfigChange will call.
// This allows the app layer to inject side-effect behavior (e.g., db.RebuildAll)
// without introducing circular imports.
func SetConfigChangeHook(hook ConfigChangeHook) {
	completeConfigHook = hook
}

// Manager handles config file I/O with case-insensitive key access.
type Manager struct {
	config       *Config
	configPath   string
	loaded       bool
	explicitKeys map[string]bool // tracks keys explicitly set or loaded from config
}

// NewManager creates a new config manager.
func NewManager(configPath string) *Manager {
	return &Manager{
		config:       DefaultConfig(),
		configPath:   configPath,
		explicitKeys: make(map[string]bool),
	}
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		SCOOPRepo:              "https://github.com/ScoopInstaller/Scoop",
		SCOOPBranch:            "master",
		ScoopGoRepo:            "lihongjie0209/scoop-go",
		Aria2Enabled:           boolPtr(true),
		Aria2WarningEnabled:    boolPtr(true),
		Aria2RetryWait:         2,
		Aria2Split:             5,
		Aria2MaxConnPerServer:  5,
		Aria2MinSplitSize:      "5M",
		ShowUpdateLog:          boolPtr(true),
		IgnoreRunningProcesses: false,
	}
}

// Load reads the config file from disk.
func (m *Manager) Load() error {
	if m.configPath == "" {
		m.configPath = defaultConfigPath()
	}

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Config file doesn't exist yet — use defaults
			m.loaded = true
			return nil
		}
		return fmt.Errorf("reading config file: %w", err)
	}

	// Before unmarshaling, record which keys are present in the JSON
	// so we can distinguish explicitly-set values from zero values.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err == nil {
		for key := range rawMap {
			m.explicitKeys[strings.ToLower(key)] = true
		}
	}

	if err := json.Unmarshal(data, m.config); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	m.loaded = true
	return nil
}

// Save writes the config to disk, creating directories if needed.
func (m *Manager) Save() error {
	if m.configPath == "" {
		m.configPath = defaultConfigPath()
	}

	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(m.configPath, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	// Finalize side effects for tracked changes after a successful save
	m.finalizeChanges()

	return nil
}

// finalizeChanges calls CompleteConfigChange for all config keys that
// have side effects, ensuring they are finalized after saving.
func (m *Manager) finalizeChanges() {
	keys := []string{"use_sqlite_cache", "use_isolated_path"}
	for _, key := range keys {
		if m.explicitKeys[key] {
			val := m.Get(key)
			m.CompleteConfigChange(key, valueToString(val))
		}
	}
}

// valueToString converts a config value to its string representation
// for passing to CompleteConfigChange.
func valueToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case string:
		return val
	case *bool:
		if val == nil {
			return ""
		}
		if *val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Get returns the config value for a given key (case-insensitive).
// Accepts an optional default value that is returned when the key is
// not set in configuration. For bool and int fields that have never
// been explicitly set, nil is returned instead of the zero value,
// allowing callers to distinguish "unset" from "set to zero".
func (m *Manager) Get(key string, defaultValue ...interface{}) interface{} {
	key = strings.ToLower(key)
	var val interface{}

	switch key {
	case "root_path":
		val = m.config.RootPath
	case "global_path":
		val = m.config.GlobalPath
	case "cache_path":
		val = m.config.CachePath
	case "proxy":
		val = m.config.Proxy
	case "scoop_repo":
		val = m.config.SCOOPRepo
	case "scoop_branch":
		val = m.config.SCOOPBranch
	case "scoop_go_repo":
		val = m.config.ScoopGoRepo
	case "gh_token":
		val = m.config.GH_TOKEN
	// Aria2: kebab-case (current)
	case "aria2-enabled", "aria2_enabled":
		val = m.config.Aria2Enabled
	case "aria2-warning-enabled", "aria2_warning_enabled":
		val = m.config.Aria2WarningEnabled
	case "aria2-retry-wait", "aria2_retry_wait":
		if m.explicitKeys[key] && m.config.Aria2RetryWait != 0 {
			val = m.config.Aria2RetryWait
		} else if m.explicitKeys[key] {
			val = m.config.Aria2RetryWait
		}
	case "aria2-split", "aria2_split":
		if m.explicitKeys[key] && m.config.Aria2Split != 0 {
			val = m.config.Aria2Split
		} else if m.explicitKeys[key] {
			val = m.config.Aria2Split
		}
	case "aria2-max-connection-per-server", "aria2_max_connection_per_server":
		if m.explicitKeys[key] && m.config.Aria2MaxConnPerServer != 0 {
			val = m.config.Aria2MaxConnPerServer
		} else if m.explicitKeys[key] {
			val = m.config.Aria2MaxConnPerServer
		}
	case "aria2-min-split-size", "aria2_min_split_size":
		val = m.config.Aria2MinSplitSize
	case "aria2-options", "aria2_options":
		val = m.config.Aria2Options
	case "use_external_7zip":
		if m.explicitKeys["use_external_7zip"] {
			val = m.config.UseExternal7Zip
		}
	case "use_lessmsi":
		if m.explicitKeys["use_lessmsi"] {
			val = m.config.UseLessMSI
		}
	case "force_update":
		if m.explicitKeys["force_update"] {
			val = m.config.ForceUpdate
		}
	case "show_update_log":
		val = m.config.ShowUpdateLog
	case "show_manifest":
		if m.explicitKeys["show_manifest"] {
			val = m.config.ShowManifest
		}
	case "update_nightly":
		if m.explicitKeys["update_nightly"] {
			val = m.config.UpdateNightly
		}
	case "use_sqlite_cache":
		if m.explicitKeys["use_sqlite_cache"] {
			val = m.config.UseSQLiteCache
		}
	case "no_junction":
		if m.explicitKeys["no_junction"] {
			val = m.config.NoJunction
		}
	case "shim":
		val = m.config.Shim
	case "debug":
		if m.explicitKeys["debug"] {
			val = m.config.Debug
		}
	case "default_architecture":
		val = m.config.DefaultArchitecture
	case "cat_style":
		val = m.config.CatStyle
	case "private_hosts":
		val = m.config.PrivateHosts
	case "use_isolated_path":
		val = m.config.UseIsolatedPath
	case "ignore_running_processes":
		if m.explicitKeys["ignore_running_processes"] {
			val = m.config.IgnoreRunningProcesses
		}
	case "hold_update_until":
		val = m.config.HoldUpdateUntil
	case "autostash_on_conflict":
		if m.explicitKeys["autostash_on_conflict"] {
			val = m.config.AutostashOnConflict
		}
	case "last_update":
		val = m.config.LastUpdate
	case "virustotal_api_key":
		val = m.config.VTApiKey
	case "alias":
		val = m.config.Alias
	default:
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return nil
	}

	// If val is nil and defaults were provided, return the default
	if val == nil && len(defaultValue) > 0 {
		return defaultValue[0]
	}

	// Return nil for non-pointer zero values (bool=false, int=0) that
	// weren't explicitly set — the switch cases above handle this by
	// only setting val when explicitKeys is true.
	return val
}

// Set sets a config value. Supported types: string, bool, int, []string, map[string]string.
// Passing nil removes the value.
// After setting, CompleteConfigChange is called for keys that have side effects.
func (m *Manager) Set(key string, value interface{}) error {
	key = strings.ToLower(key)
	priorRaw := m.Get(key)

	switch key {
	case "root_path":
		err := setString(&m.config.RootPath, value)
		if err != nil {
			return err
		}
	case "global_path":
		err := setString(&m.config.GlobalPath, value)
		if err != nil {
			return err
		}
	case "cache_path":
		err := setString(&m.config.CachePath, value)
		if err != nil {
			return err
		}
	case "proxy":
		err := setString(&m.config.Proxy, value)
		if err != nil {
			return err
		}
	case "scoop_repo":
		err := setString(&m.config.SCOOPRepo, value)
		if err != nil {
			return err
		}
	case "scoop_branch":
		err := setString(&m.config.SCOOPBranch, value)
		if err != nil {
			return err
		}
	case "scoop_go_repo":
		err := setString(&m.config.ScoopGoRepo, value)
		if err != nil {
			return err
		}
	case "gh_token":
		err := setString(&m.config.GH_TOKEN, value)
		if err != nil {
			return err
		}
	// Aria2 keys: accept both kebab-case and snake_case
	case "aria2-enabled", "aria2_enabled":
		err := setBoolPtr(&m.config.Aria2Enabled, value)
		if err != nil {
			return err
		}
	case "aria2-warning-enabled", "aria2_warning_enabled":
		err := setBoolPtr(&m.config.Aria2WarningEnabled, value)
		if err != nil {
			return err
		}
	case "aria2-retry-wait", "aria2_retry_wait":
		err := setInt(&m.config.Aria2RetryWait, value)
		if err != nil {
			return err
		}
	case "aria2-split", "aria2_split":
		err := setInt(&m.config.Aria2Split, value)
		if err != nil {
			return err
		}
	case "aria2-max-connection-per-server", "aria2_max_connection_per_server":
		err := setInt(&m.config.Aria2MaxConnPerServer, value)
		if err != nil {
			return err
		}
	case "aria2-min-split-size", "aria2_min_split_size":
		err := setString(&m.config.Aria2MinSplitSize, value)
		if err != nil {
			return err
		}
	case "aria2-options", "aria2_options":
		if err := setJSONValue(&m.config.Aria2Options, value); err != nil {
			return err
		}
	case "use_external_7zip":
		err := setBool(&m.config.UseExternal7Zip, value)
		if err != nil {
			return err
		}
	case "use_lessmsi":
		err := setBool(&m.config.UseLessMSI, value)
		if err != nil {
			return err
		}
	case "force_update":
		err := setBool(&m.config.ForceUpdate, value)
		if err != nil {
			return err
		}
	case "show_update_log":
		err := setBoolPtr(&m.config.ShowUpdateLog, value)
		if err != nil {
			return err
		}
	case "show_manifest":
		err := setBool(&m.config.ShowManifest, value)
		if err != nil {
			return err
		}
	case "update_nightly":
		err := setBool(&m.config.UpdateNightly, value)
		if err != nil {
			return err
		}
	case "use_sqlite_cache":
		err := setBool(&m.config.UseSQLiteCache, value)
		if err != nil {
			return err
		}
	case "no_junction":
		err := setBool(&m.config.NoJunction, value)
		if err != nil {
			return err
		}
	case "shim":
		err := setString(&m.config.Shim, value)
		if err != nil {
			return err
		}
	case "debug":
		err := setBool(&m.config.Debug, value)
		if err != nil {
			return err
		}
	case "default_architecture":
		err := setString(&m.config.DefaultArchitecture, value)
		if err != nil {
			return err
		}
	case "cat_style":
		err := setString(&m.config.CatStyle, value)
		if err != nil {
			return err
		}
	case "use_isolated_path":
		m.config.UseIsolatedPath = value
	case "private_hosts":
		if err := setJSONValue(&m.config.PrivateHosts, value); err != nil {
			return err
		}
	case "ignore_running_processes":
		err := setBool(&m.config.IgnoreRunningProcesses, value)
		if err != nil {
			return err
		}
	case "hold_update_until":
		err := setString(&m.config.HoldUpdateUntil, value)
		if err != nil {
			return err
		}
	case "autostash_on_conflict":
		err := setBool(&m.config.AutostashOnConflict, value)
		if err != nil {
			return err
		}
	case "last_update":
		err := setString(&m.config.LastUpdate, value)
		if err != nil {
			return err
		}
	case "virustotal_api_key":
		err := setString(&m.config.VTApiKey, value)
		if err != nil {
			return err
		}
	case "alias":
		if err := setAlias(&m.config.Alias, value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	// Mark key as explicitly set
	m.explicitKeys[key] = true

	// Auto-call CompleteConfigChange for keys with side effects
	// Normalize the canonical key name for the notification
	canonical := canonicalKeyName(key)
	if canonical != "" {
		prior := valueToString(priorRaw)
		m.CompleteConfigChange(canonical, valueToString(m.Get(key)))
		_ = prior // available for diff-based logic if needed in the future
	}

	return nil
}

// canonicalKeyName maps any valid key spelling to the canonical name used
// by CompleteConfigChange. Returns empty string for keys with no side effects.
func canonicalKeyName(key string) string {
	switch key {
	case "use_sqlite_cache":
		return "use_sqlite_cache"
	case "use_isolated_path":
		return "use_isolated_path"
	default:
		return ""
	}
}

// Unset removes a config key.
func (m *Manager) Unset(key string) error {
	return m.Set(key, nil)
}

// Config returns the underlying config struct.
func (m *Manager) Config() *Config {
	return m.config
}

// ConfigPath returns the config file path.
func (m *Manager) ConfigPath() string {
	return m.configPath
}

// defaultConfigPath returns the path to the config file.
// It checks for a portable config at <scoopdir>/config.json first,
// then falls back to ~/.config/scoop/config.json (or XDG_CONFIG_HOME equivalent).
func defaultConfigPath() string {
	// Check portable config first: <scoopdir>/config.json
	scoopDir := DefaultScoopDir()
	portablePath := filepath.Join(scoopDir, "config.json")
	if _, err := os.Stat(portablePath); err == nil {
		return portablePath
	}

	// Fall back to XDG config
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "scoop", "config.json")
}

// Helper: default scoop root directory
func DefaultScoopDir() string {
	if d := os.Getenv("SCOOP"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "scoop")
}

// Helper: default global scoop directory
func DefaultGlobalDir() string {
	if d := os.Getenv("SCOOP_GLOBAL"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("ProgramData"), "scoop")
}

// Type helpers
func boolPtr(b bool) *bool { return &b }

func setString(target *string, value interface{}) error {
	if value == nil {
		*target = ""
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", value)
	}
	*target = s
	return nil
}

func setBool(target *bool, value interface{}) error {
	if value == nil {
		*target = false
		return nil
	}
	switch v := value.(type) {
	case bool:
		*target = v
		return nil
	case string:
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			*target = true
			return nil
		case "false", "0", "no", "off":
			*target = false
			return nil
		default:
			return fmt.Errorf("cannot parse %q as bool", v)
		}
	}
	return fmt.Errorf("expected bool, got %T", value)
}

func setBoolPtr(target **bool, value interface{}) error {
	if value == nil {
		*target = nil
		return nil
	}
	switch v := value.(type) {
	case bool:
		*target = &v
		return nil
	case string:
		b := false
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			b = true
		case "false", "0", "no", "off":
			b = false
		default:
			return fmt.Errorf("cannot parse %q as bool", v)
		}
		*target = &b
		return nil
	}
	return fmt.Errorf("expected bool, got %T", value)
}

func setInt(target *int, value interface{}) error {
	if value == nil {
		*target = 0
		return nil
	}
	switch v := value.(type) {
	case float64:
		*target = int(v)
	case int:
		*target = v
	case string:
		n, err := fmt.Sscanf(v, "%d", target)
		if err != nil || n != 1 {
			return fmt.Errorf("cannot parse %q as integer", v)
		}
	default:
		return fmt.Errorf("expected number, got %T", value)
	}
	return nil
}

// setJSONValue marshals value as JSON into the target if it is a string,
// or assigns directly if it matches the target type.
// Handles types like []string and []PrivateHostRule.
func setJSONValue(target interface{}, value interface{}) error {
	if value == nil {
		// Clear the target via JSON zero value
		zero := "null"
		return json.Unmarshal([]byte(zero), target)
	}

	switch v := value.(type) {
	case string:
		// Treat as JSON string — unmarshal into target
		if err := json.Unmarshal([]byte(v), target); err != nil {
			return fmt.Errorf("cannot parse %q as JSON for target type: %w", v, err)
		}
		return nil
	default:
		// Try direct JSON round-trip
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("cannot marshal value %v: %w", value, err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("cannot unmarshal %s into target: %w", string(data), err)
		}
		return nil
	}
}

// setAlias handles the "alias" config key, accepting either a map[string]string
// or a JSON-encoded string representation of aliases.
func setAlias(target *map[string]string, value interface{}) error {
	if value == nil {
		*target = nil
		return nil
	}

	switch v := value.(type) {
	case map[string]string:
		*target = v
		return nil
	case string:
		// Parse as JSON
		var m map[string]string
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return fmt.Errorf("cannot parse %q as JSON alias map: %w", v, err)
		}
		*target = m
		return nil
	default:
		return fmt.Errorf("expected map[string]string or JSON string, got %T", value)
	}
}

// CompleteConfigChange handles side effects when certain config values change.
// Mirrors core.ps1 Complete-ConfigChange L141-L217.
// Uses the registered hook (set via SetConfigChangeHook) to perform
// actual side effects without creating circular imports.
func (m *Manager) CompleteConfigChange(name, value string) {
	switch name {
	case "use_sqlite_cache":
		if value == "true" || value == "1" {
			// If a hook is registered, delegate to it
			if completeConfigHook != nil {
				completeConfigHook("use_sqlite_cache", value)
			}
		}
	case "use_isolated_path":
		// Reorganize PATH between regular and isolated environment variable
		// If a hook is registered, delegate to it
		if completeConfigHook != nil {
			completeConfigHook("use_isolated_path", value)
		}
	}
}
