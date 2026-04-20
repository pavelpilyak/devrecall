package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

// wizard holds the state for the interactive onboarding wizard.
type wizard struct {
	in  *bufio.Scanner
	out io.Writer
	cfg *config.Config
	ctx context.Context

	// Injected dependencies for testing.
	tokenStore    auth.TokenStore
	detectEmails  func() []string
	slackOAuth    func(context.Context) (*auth.SlackToken, error)
	googleOAuth   func(context.Context) (*auth.GoogleToken, error)
	gitHubOAuth   func(context.Context) (*auth.GitHubToken, error)
	gitHubPAT     func(context.Context, string) (*auth.GitHubToken, error)
	gitHubGHCLI   func(context.Context) (*auth.GitHubToken, error)
	gitLabPAT     func(context.Context, string, string) (*auth.GitLabToken, error)
	bitbucketAuth func(context.Context, string, string) (*auth.BitbucketToken, error)
	storeLLMKey   func(provider string, key string) error
	openDB        func() (*storage.DB, error)
}

func newWizard(ctx context.Context, r io.Reader, w io.Writer) *wizard {
	return &wizard{
		in:  bufio.NewScanner(r),
		out: w,
		ctx: ctx,

		detectEmails: detectGitEmails,
		slackOAuth: func(ctx context.Context) (*auth.SlackToken, error) {
			return auth.SlackOAuth(ctx, auth.DefaultSlackOAuthConfig())
		},
		googleOAuth: func(ctx context.Context) (*auth.GoogleToken, error) {
			return auth.GoogleOAuth(ctx, auth.DefaultGoogleOAuthConfig())
		},
		gitHubOAuth: func(ctx context.Context) (*auth.GitHubToken, error) {
			return auth.GitHubOAuth(ctx, auth.DefaultGitHubOAuthConfig())
		},
		gitHubPAT: func(ctx context.Context, pat string) (*auth.GitHubToken, error) {
			return auth.ValidateGitHubPAT(ctx, pat, auth.DefaultGitHubPATConfig())
		},
		gitHubGHCLI: func(ctx context.Context) (*auth.GitHubToken, error) {
			return auth.GitHubFromGHCLI(ctx, auth.DefaultGitHubPATConfig())
		},
		gitLabPAT: func(ctx context.Context, pat, baseURL string) (*auth.GitLabToken, error) {
			cfg := auth.DefaultGitLabAuthConfig()
			if baseURL != "" {
				cfg.BaseURL = baseURL
			}
			return auth.ValidateGitLabPAT(ctx, pat, cfg)
		},
		bitbucketAuth: func(ctx context.Context, user, pass string) (*auth.BitbucketToken, error) {
			return auth.ValidateBitbucketAppPassword(ctx, user, pass, auth.DefaultBitbucketAuthConfig())
		},
		openDB: storage.Open,
	}
}

func (w *wizard) printf(format string, args ...any) {
	fmt.Fprintf(w.out, format, args...)
}

func (w *wizard) readLine() string {
	if w.in.Scan() {
		return strings.TrimSpace(w.in.Text())
	}
	return ""
}

// confirm asks a yes/no question. defaultYes controls what Enter alone means.
func (w *wizard) confirm(prompt string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	w.printf("  ? %s [%s] ", prompt, hint)
	ans := strings.ToLower(w.readLine())
	if ans == "" {
		return defaultYes
	}
	return ans == "y" || ans == "yes"
}

func (w *wizard) prompt(prompt string, defaultVal string) string {
	if defaultVal != "" {
		w.printf("  ? %s [%s] ", prompt, defaultVal)
	} else {
		w.printf("  ? %s ", prompt)
	}
	ans := w.readLine()
	if ans == "" {
		return defaultVal
	}
	return ans
}

// run executes the full onboarding wizard. Returns the final config.
func (w *wizard) run() error {
	w.printf("\nWelcome to DevRecall! Let's get you set up.\n\n")

	// Step 0: Load existing config or create a fresh one.
	cfg, err := config.Load()
	if err != nil {
		// No existing config — create a fresh one.
		cfg, err = config.Init()
		if err != nil {
			return fmt.Errorf("initializing config: %w", err)
		}
	}
	w.cfg = cfg

	db, err := w.openDB()
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	db.Close()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return err
	}
	w.tokenStore = store

	// Step 1: Git configuration.
	w.stepGit()

	// Step 2: Sources.
	w.printf("\nStep 2: Connect Sources\n")
	w.printf("  (You can skip any source and connect it later with 'devrecall auth <source>')\n\n")

	w.stepSlack()
	w.stepCalendar()
	w.stepGitHub()
	w.stepGitLab()
	w.stepBitbucket()

	// Step 3: LLM.
	w.stepLLM()

	// Save config.
	if err := w.cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Final status.
	w.printStatus()

	return nil
}

func (w *wizard) stepGit() {
	w.printf("Step 1: Git Configuration\n")

	emails := w.detectEmails()
	if len(emails) > 0 {
		w.printf("  Detected git emails: %s\n", strings.Join(emails, ", "))
		if w.confirm("Use these emails?", true) {
			w.cfg.Git.Emails = mergeUnique(w.cfg.Git.Emails, emails)
		}
	} else {
		w.printf("  No git emails detected.\n")
		email := w.prompt("Enter your git email:", "")
		if email != "" {
			w.cfg.Git.Emails = []string{email}
		}
	}

	if w.confirm("Scan ~/Projects for git repos?", true) {
		// Already the default in config.Init, nothing to change.
	} else {
		w.cfg.Git.ScanPaths = []string{}
		path := w.prompt("Enter path to scan (or leave empty):", "")
		if path != "" {
			w.cfg.Git.ScanPaths = []string{path}
		}
	}

	w.printf("  ✓ Git configured\n")
}

func (w *wizard) stepSlack() {
	if !w.confirm("Connect Slack?", false) {
		w.printf("    → Skipped\n\n")
		return
	}

	w.printf("    Opening browser for Slack authorization...\n")
	token, err := w.slackOAuth(w.ctx)
	if err != nil {
		w.printf("    ✗ Slack auth failed: %v\n\n", err)
		return
	}

	if err := w.tokenStore.Save("slack", token.TeamID, token); err != nil {
		w.printf("    ✗ Failed to save token: %v\n\n", err)
		return
	}

	w.cfg.Slack.Enabled = true
	w.cfg.Slack.TeamID = token.TeamID
	w.cfg.Slack.TeamName = token.TeamName
	w.printf("    ✓ Slack connected (%s)\n\n", token.TeamName)
}

func (w *wizard) stepCalendar() {
	if !w.confirm("Connect Google Calendar?", false) {
		w.printf("    → Skipped\n\n")
		return
	}

	w.printf("    Opening browser for Google authorization...\n")
	token, err := w.googleOAuth(w.ctx)
	if err != nil {
		w.printf("    ✗ Google auth failed: %v\n\n", err)
		return
	}

	if err := w.tokenStore.Save("google", token.Email, token); err != nil {
		w.printf("    ✗ Failed to save token: %v\n\n", err)
		return
	}

	w.cfg.Calendar.Enabled = true
	w.cfg.Calendar.Email = token.Email
	w.printf("    ✓ Google Calendar connected (%s)\n\n", token.Email)
}

func (w *wizard) stepGitHub() {
	if !w.confirm("Connect GitHub?", false) {
		w.printf("    → Skipped\n\n")
		return
	}

	method := w.prompt("Auth method — oauth, pat, or gh-cli?", "oauth")

	var token *auth.GitHubToken
	var err error

	switch method {
	case "oauth":
		w.printf("    Opening browser for GitHub authorization...\n")
		token, err = w.gitHubOAuth(w.ctx)
	case "pat":
		pat := w.prompt("Enter GitHub personal access token:", "")
		if pat == "" {
			w.printf("    ✗ Token cannot be empty\n\n")
			return
		}
		w.printf("    Validating token...\n")
		token, err = w.gitHubPAT(w.ctx, pat)
	case "gh-cli":
		w.printf("    Reading token from gh CLI...\n")
		token, err = w.gitHubGHCLI(w.ctx)
	default:
		w.printf("    ✗ Unknown method %q\n\n", method)
		return
	}

	if err != nil {
		w.printf("    ✗ GitHub auth failed: %v\n\n", err)
		return
	}

	if err := w.tokenStore.Save("github", token.Username, token); err != nil {
		w.printf("    ✗ Failed to save token: %v\n\n", err)
		return
	}

	w.cfg.GitHub.Enabled = true
	w.cfg.GitHub.Username = token.Username
	w.cfg.GitHub.AuthMode = method
	w.printf("    ✓ GitHub connected (%s via %s)\n\n", token.Username, method)
}

func (w *wizard) stepGitLab() {
	if !w.confirm("Connect GitLab?", false) {
		w.printf("    → Skipped\n\n")
		return
	}

	baseURL := w.prompt("GitLab instance URL (leave empty for gitlab.com):", "")
	pat := w.prompt("Enter GitLab personal access token:", "")
	if pat == "" {
		w.printf("    ✗ Token cannot be empty\n\n")
		return
	}

	w.printf("    Validating token...\n")
	token, err := w.gitLabPAT(w.ctx, pat, baseURL)
	if err != nil {
		w.printf("    ✗ GitLab auth failed: %v\n\n", err)
		return
	}

	if err := w.tokenStore.Save("gitlab", token.Username, token); err != nil {
		w.printf("    ✗ Failed to save token: %v\n\n", err)
		return
	}

	w.cfg.GitLab.Enabled = true
	w.cfg.GitLab.Username = token.Username
	w.cfg.GitLab.BaseURL = token.BaseURL
	w.printf("    ✓ GitLab connected (%s @ %s)\n\n", token.Username, token.BaseURL)
}

func (w *wizard) stepBitbucket() {
	if !w.confirm("Connect Bitbucket?", false) {
		w.printf("    → Skipped\n\n")
		return
	}

	username := w.prompt("Bitbucket email (use username only for legacy app passwords):", "")
	if username == "" {
		w.printf("    ✗ Email/username cannot be empty\n\n")
		return
	}
	appPass := w.prompt("Bitbucket app password or API token:", "")
	if appPass == "" {
		w.printf("    ✗ App password cannot be empty\n\n")
		return
	}
	workspace := w.prompt("Default workspace:", "")
	if workspace == "" {
		w.printf("    ✗ Workspace cannot be empty\n\n")
		return
	}

	w.printf("    Validating credentials...\n")
	token, err := w.bitbucketAuth(w.ctx, username, appPass)
	if err != nil {
		w.printf("    ✗ Bitbucket auth failed: %v\n\n", err)
		return
	}

	if err := w.tokenStore.Save("bitbucket", token.Username, token); err != nil {
		w.printf("    ✗ Failed to save token: %v\n\n", err)
		return
	}

	w.cfg.Bitbucket.Enabled = true
	w.cfg.Bitbucket.Username = token.Username
	w.cfg.Bitbucket.Workspace = workspace
	w.printf("    ✓ Bitbucket connected (%s, workspace: %s)\n\n", token.Username, workspace)
}

func (w *wizard) stepLLM() {
	w.printf("\nStep 3: LLM Provider\n")
	provider := w.prompt("LLM provider — ollama (local, free), openai, or anthropic?", "ollama")

	switch provider {
	case "openai", "anthropic":
		key := w.prompt(fmt.Sprintf("Enter your %s API key:", provider), "")
		if key == "" {
			w.printf("  ✗ API key cannot be empty, falling back to ollama\n")
			provider = "ollama"
		} else {
			if err := w.tokenStore.Save("llm", provider, llm.APIKeyToken{APIKey: key}); err != nil {
				w.printf("  ✗ Failed to save API key: %v — falling back to ollama\n", err)
				provider = "ollama"
			}
		}
	case "ollama":
		// Default, nothing to do.
	default:
		w.printf("  Unknown provider %q, using ollama\n", provider)
		provider = "ollama"
	}

	w.cfg.LLM.Provider = provider
	w.cfg.LLM.Model = defaultModelForProvider(provider)
	w.printf("  ✓ LLM: %s (model: %s)\n", provider, w.cfg.LLM.Model)
}

func (w *wizard) printStatus() {
	w.printf("\n━━━ Setup Complete ━━━\n\n")

	// Git
	if w.cfg.Git.Enabled {
		emails := "no emails"
		if len(w.cfg.Git.Emails) > 0 {
			emails = strings.Join(w.cfg.Git.Emails, ", ")
		}
		paths := "no scan paths"
		if len(w.cfg.Git.ScanPaths) > 0 {
			paths = "scanning " + strings.Join(w.cfg.Git.ScanPaths, ", ")
		}
		w.printf("  ✓ Git        %s · %s\n", emails, paths)
	} else {
		w.printf("  ✗ Git        not configured\n")
	}

	// Slack
	if w.cfg.Slack.Enabled {
		w.printf("  ✓ Slack      connected (%s)\n", w.cfg.Slack.TeamName)
	} else {
		w.printf("  · Slack      not connected\n")
	}

	// Calendar
	if w.cfg.Calendar.Enabled {
		w.printf("  ✓ Calendar   connected (%s)\n", w.cfg.Calendar.Email)
	} else {
		w.printf("  · Calendar   not connected\n")
	}

	// GitHub
	if w.cfg.GitHub.Enabled {
		w.printf("  ✓ GitHub     connected (%s)\n", w.cfg.GitHub.Username)
	} else {
		w.printf("  · GitHub     not connected\n")
	}

	// GitLab
	if w.cfg.GitLab.Enabled {
		w.printf("  ✓ GitLab     connected (%s)\n", w.cfg.GitLab.Username)
	} else {
		w.printf("  · GitLab     not connected\n")
	}

	// Bitbucket
	if w.cfg.Bitbucket.Enabled {
		w.printf("  ✓ Bitbucket  connected (%s)\n", w.cfg.Bitbucket.Username)
	} else {
		w.printf("  · Bitbucket  not connected\n")
	}

	// Jira / Linear (auth not implemented yet)
	w.printf("  · Jira       not connected\n")
	w.printf("  · Linear     not connected\n")

	// LLM
	w.printf("  ✓ LLM        %s (model: %s)\n", w.cfg.LLM.Provider, w.cfg.LLM.Model)

	w.printf("\nNext steps:\n")
	w.printf("  devrecall sync      — sync activity from configured sources\n")
	w.printf("  devrecall standup   — generate a standup report\n")
	w.printf("  devrecall auth <source> — connect additional sources\n\n")
}

// detectGitEmails finds unique user.email values from git config.
func detectGitEmails() []string {
	// Global git config email.
	out, err := exec.Command("git", "config", "--global", "user.email").Output()
	if err != nil {
		return nil
	}
	email := strings.TrimSpace(string(out))
	if email == "" {
		return nil
	}
	return []string{email}
}
