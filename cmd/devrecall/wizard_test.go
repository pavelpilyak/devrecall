package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavelpiliak/devrecall/internal/auth"
	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/internal/storage"
)

// mockTokenStore is a simple in-memory token store for tests.
type mockTokenStore struct {
	saved map[string]any
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{saved: make(map[string]any)}
}

func (m *mockTokenStore) Save(vendor, key string, token any) error {
	m.saved[vendor+"/"+key] = token
	return nil
}

func (m *mockTokenStore) Load(vendor, key string, dst any) error {
	return nil
}

func (m *mockTokenStore) Delete(vendor, key string) error {
	return nil
}

// setupTestEnv sets HOME and XDG to a temp dir so config.Init doesn't touch real home.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	return tmp
}

func TestWizardSkipAll(t *testing.T) {
	tmp := setupTestEnv(t)

	// Input: accept git emails, accept ~/Projects, skip all sources, accept ollama.
	input := strings.Join([]string{
		"y",  // use detected emails
		"y",  // scan ~/Projects
		"n",  // slack
		"n",  // calendar
		"n",  // github
		"n",  // gitlab
		"n",  // bitbucket
		"",   // llm: ollama (default)
	}, "\n") + "\n"

	var out bytes.Buffer
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return []string{"test@example.com"} }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, ".devrecall", "test.db"))
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}

	output := out.String()

	// Git should be configured with the detected email.
	if !strings.Contains(output, "✓ Git configured") {
		t.Error("expected git configured message")
	}
	if !strings.Contains(output, "✓ Git") {
		t.Error("expected git status in summary")
	}
	if !strings.Contains(output, "test@example.com") {
		t.Error("expected detected email in output")
	}

	// All sources should be skipped.
	for _, source := range []string{"Slack", "Calendar", "GitHub", "GitLab", "Bitbucket"} {
		if !strings.Contains(output, "· "+source) {
			t.Errorf("expected %s shown as not connected", source)
		}
	}

	// LLM should be ollama.
	if !strings.Contains(output, "✓ LLM        ollama") {
		t.Error("expected ollama LLM in summary")
	}

	// Config should be saved on disk.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() failed: %v", err)
	}
	if !cfg.Git.Enabled {
		t.Error("expected git to be enabled")
	}
	if len(cfg.Git.Emails) == 0 || cfg.Git.Emails[0] != "test@example.com" {
		t.Error("expected detected email in config")
	}
}

func TestWizardConnectGitHub(t *testing.T) {
	setupTestEnv(t)
	tmp := t.TempDir()

	input := strings.Join([]string{
		"y",     // use detected emails
		"y",     // scan ~/Projects
		"n",     // slack
		"n",     // calendar
		"y",     // github
		"gh-cli", // method
		"n",     // gitlab
		"n",     // bitbucket
		"",      // llm: ollama
	}, "\n") + "\n"

	var out bytes.Buffer
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return []string{"dev@example.com"} }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, "test.db"))
	}
	w.gitHubGHCLI = func(ctx context.Context) (*auth.GitHubToken, error) {
		return &auth.GitHubToken{AccessToken: "gho_test", Username: "testuser"}, nil
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "✓ GitHub connected (testuser via gh-cli)") {
		t.Error("expected GitHub connected message")
	}
	if !strings.Contains(output, "✓ GitHub     connected (testuser)") {
		t.Error("expected GitHub in summary")
	}

	if !w.cfg.GitHub.Enabled {
		t.Error("expected GitHub to be enabled in config")
	}
	if w.cfg.GitHub.Username != "testuser" {
		t.Errorf("expected GitHub username testuser, got %q", w.cfg.GitHub.Username)
	}
}

func TestWizardConnectSlack(t *testing.T) {
	setupTestEnv(t)
	tmp := t.TempDir()

	input := strings.Join([]string{
		"y", // use detected emails
		"y", // scan ~/Projects
		"y", // slack
		"n", // calendar
		"n", // github
		"n", // gitlab
		"n", // bitbucket
		"",  // llm: ollama
	}, "\n") + "\n"

	var out bytes.Buffer
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return []string{"dev@example.com"} }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, "test.db"))
	}
	w.slackOAuth = func(ctx context.Context) (*auth.SlackToken, error) {
		return &auth.SlackToken{
			AccessToken: "xoxb-test",
			TeamID:      "T123",
			TeamName:    "Test Team",
		}, nil
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "✓ Slack connected (Test Team)") {
		t.Error("expected Slack connected message")
	}
	if !w.cfg.Slack.Enabled {
		t.Error("expected Slack to be enabled")
	}
}

func TestWizardLLMOpenAI(t *testing.T) {
	setupTestEnv(t)
	tmp := t.TempDir()

	input := strings.Join([]string{
		"y",       // use detected emails
		"y",       // scan ~/Projects
		"n",       // slack
		"n",       // calendar
		"n",       // github
		"n",       // gitlab
		"n",       // bitbucket
		"openai",  // llm provider
		"sk-test", // api key
	}, "\n") + "\n"

	var out bytes.Buffer
	store := newMockTokenStore()
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return []string{"dev@example.com"} }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, "test.db"))
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}
	_ = store // The real token store from config.Init is used; just check config.

	if w.cfg.LLM.Provider != "openai" {
		t.Errorf("expected openai provider, got %q", w.cfg.LLM.Provider)
	}
	if w.cfg.LLM.Model == "" {
		t.Error("expected a default model for openai, got empty")
	}

	output := out.String()
	if !strings.Contains(output, "✓ LLM        openai") {
		t.Error("expected openai LLM in summary")
	}
}

func TestWizardNoDetectedEmails(t *testing.T) {
	setupTestEnv(t)
	tmp := t.TempDir()

	input := strings.Join([]string{
		"user@custom.com", // manual email entry
		"y",               // scan ~/Projects
		"n",               // slack
		"n",               // calendar
		"n",               // github
		"n",               // gitlab
		"n",               // bitbucket
		"",                // llm: ollama
	}, "\n") + "\n"

	var out bytes.Buffer
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return nil }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, "test.db"))
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}

	if len(w.cfg.Git.Emails) == 0 || w.cfg.Git.Emails[0] != "user@custom.com" {
		t.Errorf("expected manual email in config, got %v", w.cfg.Git.Emails)
	}
}

func TestWizardCustomScanPath(t *testing.T) {
	setupTestEnv(t)
	tmp := t.TempDir()

	input := strings.Join([]string{
		"y",           // use detected emails
		"n",           // don't scan ~/Projects
		"/my/repos",   // custom path
		"n",           // slack
		"n",           // calendar
		"n",           // github
		"n",           // gitlab
		"n",           // bitbucket
		"",            // llm: ollama
	}, "\n") + "\n"

	var out bytes.Buffer
	w := newWizard(context.Background(), strings.NewReader(input), &out)
	w.detectEmails = func() []string { return []string{"dev@example.com"} }
	w.openDB = func() (*storage.DB, error) {
		return storage.OpenPath(filepath.Join(tmp, "test.db"))
	}

	if err := w.run(); err != nil {
		t.Fatalf("wizard.run() failed: %v", err)
	}

	if len(w.cfg.Git.ScanPaths) != 1 || w.cfg.Git.ScanPaths[0] != "/my/repos" {
		t.Errorf("expected custom scan path, got %v", w.cfg.Git.ScanPaths)
	}
}

func TestConfirmDefaultYes(t *testing.T) {
	var out bytes.Buffer
	w := &wizard{
		in:  bufio.NewScanner(strings.NewReader("\n")),
		out: &out,
	}
	if !w.confirm("Test?", true) {
		t.Error("expected default yes on empty input")
	}
}

func TestConfirmDefaultNo(t *testing.T) {
	var out bytes.Buffer
	w := &wizard{
		in:  bufio.NewScanner(strings.NewReader("\n")),
		out: &out,
	}
	if w.confirm("Test?", false) {
		t.Error("expected default no on empty input")
	}
}

func TestConfirmExplicitYes(t *testing.T) {
	var out bytes.Buffer
	w := &wizard{
		in:  bufio.NewScanner(strings.NewReader("y\n")),
		out: &out,
	}
	if !w.confirm("Test?", false) {
		t.Error("expected yes on 'y' input")
	}
}

func TestDetectGitEmails(t *testing.T) {
	// This test just ensures detectGitEmails doesn't panic.
	// Actual email detection depends on git config.
	emails := detectGitEmails()
	_ = emails // May be nil if no global git config.
}

// Ensure unused import is satisfied.
var _ = os.DevNull
