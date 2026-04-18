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
	GitHub   PrivacyMode `json:"github,omitempty"`
	GitLab    PrivacyMode `json:"gitlab,omitempty"`
	Bitbucket PrivacyMode `json:"bitbucket,omitempty"`
	Jira      PrivacyMode `json:"jira,omitempty"`
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
	case "github":
		mode = p.GitHub
	case "gitlab":
		mode = p.GitLab
	case "bitbucket":
		mode = p.Bitbucket
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
	Git          GitConfig        `json:"git"`
	Slack        SlackConfig      `json:"slack"`
	Calendar     CalendarConfig   `json:"calendar"`
	GitHub       GitHubConfig     `json:"github"`
	GitLab       GitLabConfig     `json:"gitlab"`
	Bitbucket    BitbucketConfig  `json:"bitbucket"`
	Jira         JiraConfig       `json:"jira"`
	Confluence   ConfluenceConfig `json:"confluence"`
	Linear       LinearConfig     `json:"linear"`
	LLM          LLMConfig        `json:"llm"`
	Embedding    EmbeddingConfig  `json:"embedding,omitempty"`
	Privacy      PrivacyConfig    `json:"privacy,omitempty"`
	Chat         ChatConfig       `json:"chat,omitempty"`
	Server       ServerConfig     `json:"server,omitempty"`
	TokenStorage string           `json:"token_storage,omitempty"` // "keychain" or "file" (default: "file")
	filePath     string
}

// ServerConfig controls the local HTTP API server.
type ServerConfig struct {
	// Port is the TCP port to listen on. 0 or omitted means 9147 (default).
	Port int `json:"port,omitempty"`
}

// ChatConfig holds chat-loop runtime tweaks.
type ChatConfig struct {
	SyncFreshness SyncFreshnessConfig `json:"sync_freshness,omitempty"`
}

// SyncFreshnessConfig configures the pre-agent freshness sync step. See
// docs/chat-agent-rewrite.md ("Sync freshness — pre-agent only"). Durations
// are time.ParseDuration strings, e.g. "3h", "30m".
type SyncFreshnessConfig struct {
	// Disabled turns the freshness step off entirely. The /sync slash
	// command can still force a refresh.
	Disabled bool `json:"disabled,omitempty"`
	// DefaultTTL is the freshness window applied to any source not listed
	// in PerSource. Empty falls back to freshness.DefaultTTL (3h).
	DefaultTTL string `json:"default_ttl,omitempty"`
	// PerSource overrides DefaultTTL by source name (e.g. "slack": "1h").
	PerSource map[string]string `json:"per_source,omitempty"`
	// Wait caps the total time spent blocking on in-flight syncs. Empty
	// falls back to freshness.DefaultWait (10s).
	Wait string `json:"wait,omitempty"`
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
	Enabled bool   `json:"enabled"`
	Email   string `json:"email,omitempty"`
}

type GitHubConfig struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username,omitempty"` // GitHub username for API queries
	AuthMode string `json:"auth_mode,omitempty"` // "oauth", "pat", or "gh-cli"
}

type GitLabConfig struct {
	Enabled  bool   `json:"enabled"`
	BaseURL  string `json:"base_url,omitempty"` // defaults to https://gitlab.com; set for self-hosted
	Username string `json:"username,omitempty"` // GitLab username
}

type BitbucketConfig struct {
	Enabled   bool   `json:"enabled"`
	Username  string `json:"username,omitempty"`  // Bitbucket username
	Workspace string `json:"workspace,omitempty"` // default workspace to query
}

type JiraConfig struct {
	Enabled  bool   `json:"enabled"`
	BaseURL  string `json:"base_url,omitempty"`  // Jira instance URL (e.g., "https://mycompany.atlassian.net")
	AuthMode string `json:"auth_mode,omitempty"` // "oauth" or "api-token"
	Email    string `json:"email,omitempty"`      // required for api-token auth
}

type ConfluenceConfig struct {
	Enabled bool `json:"enabled"`
	// Uses the same Atlassian auth as Jira (shared OAuth token or API token).
}

type LinearConfig struct {
	Enabled  bool   `json:"enabled"`
	AuthMode string `json:"auth_mode,omitempty"` // "oauth" or "api-key"
	Email    string `json:"email,omitempty"`     // token store key
}

type LLMConfig struct {
	Provider string `json:"provider"` // "ollama", "openai", "anthropic"
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
}

// EmbeddingConfig controls the vector embedding provider.
// If Provider is empty, defaults to the LLM provider.
type EmbeddingConfig struct {
	Provider   string `json:"provider,omitempty"`   // "ollama" or "openai"; defaults to LLM provider
	Model      string `json:"model,omitempty"`      // embedding model name; defaults to "all-minilm" (ollama) or "text-embedding-3-small" (openai)
	BaseURL    string `json:"base_url,omitempty"`   // override endpoint
	Dimensions int    `json:"dimensions,omitempty"` // vector size; 0 = model default (384 for all-minilm, 1536 for openai)
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
			Model:    "gemma4",
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
		return nil, fmt.Errorf("invalid config — please check %s: %w", path, err)
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
