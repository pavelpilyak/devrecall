package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	appDir     = ".devrecall"
	configFile = "config.json"
	dbFile     = "devrecall.db"
)

// PrivacyMode controls how much activity detail is retained for a source.
type PrivacyMode string

const (
	// PrivacyFull keeps all data: title, content, and full metadata.
	PrivacyFull PrivacyMode = "full"
	// PrivacySummary keeps title and summaries but strips raw content
	// (e.g., Slack message text, commit diffs).
	PrivacySummary PrivacyMode = "summary"
	// PrivacyMetadata keeps only source, type, timestamp, and basic counts.
	PrivacyMetadata PrivacyMode = "metadata"
)

// PrivacyConfig holds per-source privacy modes.
type PrivacyConfig struct {
	Git      PrivacyMode `json:"git,omitempty"`
	Slack    PrivacyMode `json:"slack,omitempty"`
	Calendar PrivacyMode `json:"calendar,omitempty"`
	Jira     PrivacyMode `json:"jira,omitempty"`
	Linear   PrivacyMode `json:"linear,omitempty"`
}

// ModeFor returns the privacy mode for a given source, defaulting to "full".
func (p PrivacyConfig) ModeFor(source string) PrivacyMode {
	var mode PrivacyMode
	switch source {
	case "git":
		mode = p.Git
	case "slack":
		mode = p.Slack
	case "calendar":
		mode = p.Calendar
	case "jira":
		mode = p.Jira
	case "linear":
		mode = p.Linear
	}
	if mode == "" {
		return PrivacyFull
	}
	return mode
}

type Config struct {
	Git          GitConfig      `json:"git"`
	Slack        SlackConfig    `json:"slack"`
	Calendar     CalendarConfig `json:"calendar"`
	Jira         JiraConfig     `json:"jira"`
	Linear       LinearConfig   `json:"linear"`
	LLM          LLMConfig      `json:"llm"`
	Privacy      PrivacyConfig  `json:"privacy,omitempty"`
	TokenStorage string         `json:"token_storage,omitempty"` // "keychain" or "file" (default: "file")
	filePath     string
}

type GitConfig struct {
	Enabled   bool     `json:"enabled"`
	ScanPaths []string `json:"scan_paths,omitempty"` // directories to walk for .git repos
	Repos     []string `json:"repos"`               // explicit repo paths
	Emails    []string `json:"emails"`               // author emails to match as "self"
}

type SlackConfig struct {
	Enabled  bool   `json:"enabled"`
	TeamID   string `json:"team_id,omitempty"`
	TeamName string `json:"team_name,omitempty"`
}

type CalendarConfig struct {
	Enabled bool `json:"enabled"`
}

type JiraConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url,omitempty"`
}

type LinearConfig struct {
	Enabled bool `json:"enabled"`
}

type LLMConfig struct {
	Provider string `json:"provider"` // "ollama", "openai", "anthropic"
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
}

// Dir returns the DevRecall data directory (~/.devrecall).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(home, appDir), nil
}

// DBPath returns the path to the SQLite database.
func DBPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dbFile), nil
}

// Init creates a default configuration file.
func Init() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create config directory: %w", err)
	}

	home, _ := os.UserHomeDir()
	cfg := &Config{
		Git: GitConfig{
			Enabled:   true,
			ScanPaths: []string{filepath.Join(home, "Projects")},
			Repos:     []string{},
			Emails:    []string{},
		},
		LLM: LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
		},
		filePath: filepath.Join(dir, configFile),
	}

	return cfg, cfg.Save()
}

// Load reads the configuration from disk.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, configFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config not found — run 'devrecall config init' first: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	cfg.filePath = path
	return &cfg, nil
}

// Save writes the configuration to disk.
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.filePath, data, 0o644)
}

// Path returns the config file path.
func (c *Config) Path() string {
	return c.filePath
}

// String returns a pretty-printed JSON representation.
func (c *Config) String() string {
	data, _ := json.MarshalIndent(c, "", "  ")
	return string(data)
}
