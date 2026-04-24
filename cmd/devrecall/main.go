package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/agent"
	agenttools "github.com/pavelpilyak/devrecall/internal/agent/tools"
	"github.com/pavelpilyak/devrecall/internal/api"
	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/daemon"
	"github.com/pavelpilyak/devrecall/internal/collector/git"
	bbcollector "github.com/pavelpilyak/devrecall/internal/collector/bitbucket"
	calcollector "github.com/pavelpilyak/devrecall/internal/collector/calendar"
	ghcollector "github.com/pavelpilyak/devrecall/internal/collector/github"
	glcollector "github.com/pavelpilyak/devrecall/internal/collector/gitlab"
	confluencecollector "github.com/pavelpilyak/devrecall/internal/collector/confluence"
	jiracollector "github.com/pavelpilyak/devrecall/internal/collector/jira"
	linearcollector "github.com/pavelpilyak/devrecall/internal/collector/linear"
	slackcollector "github.com/pavelpilyak/devrecall/internal/collector/slack"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/chat"
	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/internal/identity"
	"github.com/pavelpilyak/devrecall/internal/update"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/privacy"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/summarizer"
	"github.com/pavelpilyak/devrecall/pkg/models"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:     "devrecall",
		Short:   "On-device developer activity aggregator",
		Long:    "DevRecall passively aggregates your work activity from Git, Slack, Calendar, Jira, and Linear to generate AI-powered standups, perf reviews, and work memory — all on your machine.",
		Version: version,
	}

	rootCmd.AddCommand(
		newSetupCmd(),
		newStandupCmd(),
		newWeekCmd(),
		newSyncCmd(),
		newStatusCmd(),
		newChatCmd(),
		newSummarizeCmd(),
		newBragCmd(),
		newPerfReviewCmd(),
		newSearchCmd(),
		newLogCmd(),
		newTimelineCmd(),
		newPruneCmd(),
		newConfigCmd(),
		newAuthCmd(),
		newIdentityCmd(),
		newServeCmd(),
		newDaemonCmd(),
		newUpdateCmd(),
	)

	enforceMandatoryUpdate(os.Args)
	runPassiveUpdateCheck()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runPassiveUpdateCheck prints a one-line "update available" notice when a newer
// release exists. Throttled to once per CheckInterval via cache file. Failures
// are silent — never block the user's command.
func runPassiveUpdateCheck() {
	dir, err := config.Dir()
	if err != nil {
		return
	}
	rel, err := update.PassiveCheck(dir, version, "")
	if err != nil || rel == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "ℹ Update available: %s (run `devrecall update`)\n", rel.Version)
}

// shouldSkipMandatoryCheck reports whether the kill switch should be bypassed
// for the given argv. The `update` command is the only escape hatch when a
// version is killed, so it must always be allowed to run.
func shouldSkipMandatoryCheck(args []string) bool {
	for _, a := range args[1:] {
		if a == "update" {
			return true
		}
		// Stop scanning at the first non-flag argument so e.g. `devrecall sync update`
		// is not treated as the update command.
		if len(a) > 0 && a[0] != '-' {
			return false
		}
	}
	return false
}

// enforceMandatoryUpdate consults the relay version manifest. If the running
// build is below the security minimum, the process aborts with exit code 2 so
// the user is forced to upgrade. The `update` command itself is exempt — that
// is the only escape hatch a stuck user has.
func enforceMandatoryUpdate(args []string) {
	if shouldSkipMandatoryCheck(args) {
		return
	}

	dir, err := config.Dir()
	if err != nil {
		return
	}
	if err := update.MandatoryCheck(dir, version, ""); err != nil {
		if update.IsUpdateRequired(err) {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "╳ devrecall is out of date and cannot run.")
			fmt.Fprintf(os.Stderr, "  %s\n", err.Error())
			fmt.Fprintln(os.Stderr, "  Run `devrecall update` to install the latest version.")
			fmt.Fprintln(os.Stderr, "")
			os.Exit(2)
		}
		// Soft failure (network/server error) — don't block the user.
	}
}

func newSetupCmd() *cobra.Command {
	var quick bool

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Initialize DevRecall with guided setup wizard",
		Long:  "Interactive wizard that creates config, initializes the database, and walks you through connecting your sources (Git, Slack, Calendar, GitHub, etc.).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if quick {
				cfg, err := config.Init()
				if err != nil {
					return fmt.Errorf("failed to initialize config: %w", err)
				}
				fmt.Printf("Config created: %s\n", cfg.Path())

				db, err := storage.Open()
				if err != nil {
					return fmt.Errorf("failed to initialize database: %w", err)
				}
				db.Close()

				dbPath, _ := config.DBPath()
				fmt.Printf("Database created: %s\n", dbPath)
				fmt.Println("\nDevRecall is ready! Next steps:")
				fmt.Println("  devrecall sync     — sync activity from configured sources")
				fmt.Println("  devrecall standup  — generate a standup report")
				return nil
			}

			w := newWizard(cmd.Context(), os.Stdin, os.Stdout)
			return w.run()
		},
	}
	cmd.Flags().BoolVar(&quick, "quick", false, "Skip interactive wizard, just create config + database")
	return cmd
}

func newStandupCmd() *cobra.Command {
	var dateFlag string

	cmd := &cobra.Command{
		Use:   "standup",
		Short: "Generate a standup report from recent activity",
		Long:  "Syncs git activity and generates a standup report. Use --date to specify a date (default: yesterday).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStandup(dateFlag)
		},
	}
	cmd.Flags().StringVar(&dateFlag, "date", "", "Date for standup report (YYYY-MM-DD, default: yesterday)")
	return cmd
}

func runStandup(dateFlag string) error {
	// Determine target date.
	targetDate := time.Now().AddDate(0, 0, -1) // yesterday
	if dateFlag != "" {
		parsed, err := time.Parse("2006-01-02", dateFlag)
		if err != nil {
			return fmt.Errorf("invalid date %q (expected YYYY-MM-DD): %w", dateFlag, err)
		}
		targetDate = parsed
	}

	dayStart := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)

	// Load config.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Open database.
	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	// Create token store (keychain or file-based, per config).
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	// Sync git if configured.
	repos := cfg.Git.Repos
	if len(cfg.Git.ScanPaths) > 0 {
		fmt.Fprintf(os.Stderr, "Scanning for repos...")
		repos = mergeUnique(repos, git.DiscoverRepos(cfg.Git.ScanPaths))
		fmt.Fprintf(os.Stderr, " found %d\n", len(repos))
	}
	fmt.Fprintf(os.Stderr, "Detecting git emails...")
	emails := mergeUnique(cfg.Git.Emails, git.DetectEmails(repos))
	fmt.Fprintf(os.Stderr, " %d email(s)\n", len(emails))

	if cfg.Git.Enabled && len(repos) > 0 && len(emails) > 0 {
		fmt.Fprintf(os.Stderr, "Collecting commits from %d repo(s)...\n", len(repos))
		collector := git.New(repos, emails)
		activities, err := collector.Collect(context.Background())
		if err != nil {
			return fmt.Errorf("git sync failed: %w", err)
		}
		if len(activities) > 0 {
			fmt.Fprintf(os.Stderr, "Storing %d activities...\n", len(activities))
			if _, err := db.InsertActivities(activities); err != nil {
				return fmt.Errorf("storing activities: %w", err)
			}
		}
	}

	// Sync Slack if configured.
	var slackUserID string
	if cfg.Slack.Enabled && cfg.Slack.TeamID != "" {
		var token auth.SlackToken
		if err := tokenStore.Load("slack", cfg.Slack.TeamID, &token); err != nil {
			fmt.Fprintf(os.Stderr, "Slack: token not found: %v (run 'devrecall auth slack')\n", err)
		} else {
			slackUserID = token.UserID
			fmt.Fprintf(os.Stderr, "Collecting Slack messages...\n")
			sc := slackcollector.New(token.AccessToken)
			activities, err := sc.CollectSince(context.Background(), dayStart)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Slack sync failed: %v\n", err)
			} else if len(activities) > 0 {
				fmt.Fprintf(os.Stderr, "Storing %d Slack activities...\n", len(activities))
				if _, err := db.InsertActivities(activities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing Slack activities: %v\n", err)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Slack: no messages found for this period\n")
			}

			// Auto-link Slack identity to Git identity via email.
			resolver := identity.NewResolver(db)
			profile, err := sc.GetUserProfile(context.Background(), token.UserID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Slack: failed to fetch user profile: %v\n", err)
			} else if profile.Email != "" {
				if linked, err := resolver.AutoLinkSlack(token.UserID, profile.Email, profile.Name); err == nil {
					fmt.Fprintf(os.Stderr, "Slack identity linked to %s <%s>\n", linked.Name, linked.Email)
				}
			}
		}
	}

	// Sync Calendar if configured.
	if cfg.Calendar.Enabled && cfg.Calendar.Email != "" {
		gtoken, err := loadGoogleToken(tokenStore, cfg.Calendar.Email)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Calendar: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting calendar events...\n")
			cc := calcollector.New(gtoken.AccessToken)
			calActivities, err := cc.CollectRange(context.Background(), dayStart, dayEnd.AddDate(0, 0, 1))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Calendar sync failed: %v\n", err)
			} else if len(calActivities) > 0 {
				// Enrich identities from calendar attendees.
				resolver := identity.NewResolver(db)
				if enriched, err := resolver.EnrichFromCalendar(calActivities); err == nil && enriched > 0 {
					fmt.Fprintf(os.Stderr, "Calendar: enriched %d identities from attendees\n", enriched)
				}
				fmt.Fprintf(os.Stderr, "Storing %d calendar activities...\n", len(calActivities))
				if _, err := db.InsertActivities(calActivities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing calendar activities: %v\n", err)
				}
			}
		}
	}

	// Sync GitHub if configured.
	if cfg.GitHub.Enabled && cfg.GitHub.Username != "" {
		var ghToken auth.GitHubToken
		if err := tokenStore.Load("github", cfg.GitHub.Username, &ghToken); err != nil {
			fmt.Fprintf(os.Stderr, "GitHub: token not found: %v (run 'devrecall auth github')\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting GitHub activity...\n")
			gc := ghcollector.New(ghToken.AccessToken, cfg.GitHub.Username)
			ghActivities, err := gc.CollectSince(context.Background(), dayStart)
			if err != nil {
				fmt.Fprintf(os.Stderr, "GitHub sync failed: %v\n", err)
			} else if len(ghActivities) > 0 {
				fmt.Fprintf(os.Stderr, "Storing %d GitHub activities...\n", len(ghActivities))
				if _, err := db.InsertActivities(ghActivities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing GitHub activities: %v\n", err)
				}
			}
		}
	}

	// Sync GitLab if configured.
	if cfg.GitLab.Enabled && cfg.GitLab.Username != "" {
		var glToken auth.GitLabToken
		if err := tokenStore.Load("gitlab", cfg.GitLab.Username, &glToken); err != nil {
			fmt.Fprintf(os.Stderr, "GitLab: token not found: %v (run 'devrecall auth gitlab')\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting GitLab activity...\n")
			gc := glcollector.New(glToken.AccessToken, cfg.GitLab.Username, cfg.GitLab.BaseURL)
			glActivities, err := gc.CollectSince(context.Background(), dayStart)
			if err != nil {
				fmt.Fprintf(os.Stderr, "GitLab sync failed: %v\n", err)
			} else if len(glActivities) > 0 {
				fmt.Fprintf(os.Stderr, "Storing %d GitLab activities...\n", len(glActivities))
				if _, err := db.InsertActivities(glActivities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing GitLab activities: %v\n", err)
				}
			}
		}
	}

	// Sync Bitbucket if configured.
	if cfg.Bitbucket.Enabled && cfg.Bitbucket.Username != "" {
		var bbToken auth.BitbucketToken
		if err := tokenStore.Load("bitbucket", cfg.Bitbucket.Username, &bbToken); err != nil {
			fmt.Fprintf(os.Stderr, "Bitbucket: token not found: %v (run 'devrecall auth bitbucket')\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting Bitbucket activity...\n")
			bc := bbcollector.New(bbToken.Username, bbToken.AppPassword, bbToken.UUID, cfg.Bitbucket.Workspace)
			bbActivities, err := bc.CollectSince(context.Background(), dayStart)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Bitbucket sync failed: %v\n", err)
			} else if len(bbActivities) > 0 {
				fmt.Fprintf(os.Stderr, "Storing %d Bitbucket activities...\n", len(bbActivities))
				if _, err := db.InsertActivities(bbActivities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing Bitbucket activities: %v\n", err)
				}
			}
		}
	}

	// Sync Jira if configured.
	if cfg.Jira.Enabled && cfg.Jira.Email != "" {
		var atlToken auth.AtlassianToken
		if err := tokenStore.Load("jira", cfg.Jira.Email, &atlToken); err != nil {
			fmt.Fprintf(os.Stderr, "Jira: token not found: %v (run 'devrecall auth jira')\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting Jira activity...\n")
			var jc *jiracollector.Collector
			if cfg.Jira.AuthMode == "api-token" && cfg.Jira.BaseURL != "" {
				jc = jiracollector.NewWithAPIToken(cfg.Jira.Email, atlToken.AccessToken, cfg.Jira.BaseURL)
			} else if len(atlToken.CloudSites) > 0 {
				jc = jiracollector.New(atlToken.AccessToken, atlToken.CloudSites[0].ID)
			}
			if jc != nil {
				jaActivities, err := jc.CollectSince(context.Background(), dayStart)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Jira sync failed: %v\n", err)
				} else if len(jaActivities) > 0 {
					fmt.Fprintf(os.Stderr, "Storing %d Jira activities...\n", len(jaActivities))
					if _, err := db.InsertActivities(jaActivities); err != nil {
						fmt.Fprintf(os.Stderr, "Storing Jira activities: %v\n", err)
					}
				}
			}
		}
	}

	// Sync Linear if configured.
	if cfg.Linear.Enabled && cfg.Linear.Email != "" {
		var lt auth.LinearToken
		if err := tokenStore.Load("linear", cfg.Linear.Email, &lt); err != nil {
			fmt.Fprintf(os.Stderr, "Linear: token not found: %v (run 'devrecall auth linear')\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Collecting Linear activity...\n")
			lc := linearcollector.New(lt.AccessToken)
			liActivities, err := lc.CollectSince(context.Background(), dayStart)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Linear sync failed: %v\n", err)
			} else if len(liActivities) > 0 {
				fmt.Fprintf(os.Stderr, "Storing %d Linear activities...\n", len(liActivities))
				if _, err := db.InsertActivities(liActivities); err != nil {
					fmt.Fprintf(os.Stderr, "Storing Linear activities: %v\n", err)
				}
			}
		}
	}

	// Query activities for the target date (all sources).
	fmt.Fprintf(os.Stderr, "Generating standup for %s...\n", targetDate.Format("2006-01-02"))
	activities, err := db.ListActivities(storage.ActivityFilter{
		After:  dayStart,
		Before: dayEnd,
	})
	if err != nil {
		return fmt.Errorf("querying activities: %w", err)
	}

	// Resolve LLM provider (used for thread summarization and standup generation).
	var llmProvider llm.Provider
	if p, llmErr := llm.FromConfig(cfg, tokenStore); llmErr == nil {
		llmProvider = p
	}

	// Summarize Slack threads via LLM if available.
	if hasSlackThreads(activities) && llmProvider != nil {
		ts := summarizer.NewThreadSummarizer(llmProvider)
		updated, count, err := ts.SummarizeThreads(context.Background(), activities)
		if err == nil && count > 0 {
			activities = updated
			fmt.Fprintf(os.Stderr, "Summarized %d thread(s)\n", count)
			// Persist summaries back to DB so they're not re-generated.
			if _, err := db.InsertActivities(activities); err != nil {
				fmt.Fprintf(os.Stderr, "Persisting thread summaries: %v\n", err)
			}
		}
	}

	// Apply privacy filters before generating output.
	activities = privacy.Apply(activities, cfg.Privacy)

	// Generate standup — use LLM if available, otherwise fall back to template.
	var s summarizer.Summarizer
	if llmProvider != nil {
		fmt.Fprintf(os.Stderr, "Generating standup with %s...\n", llmProvider.Name())
		ls := summarizer.NewLLMSummarizer(llmProvider).WithPromptLoader(promptLoader())
		if slackUserID != "" {
			ls.WithSelfUIDs(slackUserID)
		}
		s = ls
	} else {
		s = summarizer.NewTemplateSummarizer()
	}
	activities = summarizer.DeduplicateActivities(activities)
	report, err := s.Standup(activities)
	if err != nil {
		return fmt.Errorf("generating standup: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Println(report)
	return nil
}

func newWeekCmd() *cobra.Command {
	var weeksBack int

	cmd := &cobra.Command{
		Use:   "week",
		Short: "Generate a weekly summary with meeting time breakdown",
		Long:  "Generates a summary of the past week's activity including commits, messages, and meeting time breakdown by type.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWeek(weeksBack)
		},
	}
	cmd.Flags().IntVar(&weeksBack, "weeks-back", 0, "Number of weeks back (0 = current week, 1 = last week)")
	return cmd
}

func runWeek(weeksBack int) error {
	// Calculate the week range (Monday to Sunday).
	now := time.Now()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	// Start of current week (Monday).
	monday := now.AddDate(0, 0, -(weekday - 1))
	monday = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)

	// Shift back by weeksBack.
	monday = monday.AddDate(0, 0, -7*weeksBack)
	sunday := monday.AddDate(0, 0, 7)

	// Load config.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Open database.
	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	// Query activities for the week.
	fmt.Fprintf(os.Stderr, "Generating weekly summary for %s to %s...\n",
		monday.Format("2006-01-02"), sunday.AddDate(0, 0, -1).Format("2006-01-02"))

	activities, err := db.ListActivities(storage.ActivityFilter{
		After:  monday,
		Before: sunday,
	})
	if err != nil {
		return fmt.Errorf("querying activities: %w", err)
	}

	// Apply privacy filters.
	activities = privacy.Apply(activities, cfg.Privacy)

	// Resolve LLM provider.
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	var s summarizer.Summarizer
	if p, llmErr := llm.FromConfig(cfg, tokenStore); llmErr == nil {
		fmt.Fprintf(os.Stderr, "Generating weekly summary with %s...\n", p.Name())
		s = summarizer.NewLLMSummarizer(p).WithPromptLoader(promptLoader()).WithPromptLoader(promptLoader())
	} else {
		s = summarizer.NewTemplateSummarizer()
	}

	activities = summarizer.DeduplicateActivities(activities)
	report, err := s.WeeklySummary(activities)
	if err != nil {
		return fmt.Errorf("generating weekly summary: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Println(report)
	return nil
}

// loadGoogleToken loads the Google token and refreshes it if expired.
// On refresh, the updated token is persisted to the store.
func loadGoogleToken(store auth.TokenStore, email string) (*auth.GoogleToken, error) {
	var token auth.GoogleToken
	if err := store.Load("google", email, &token); err != nil {
		return nil, err
	}

	if !token.IsExpired() {
		return &token, nil
	}

	if token.RefreshToken == "" {
		return nil, fmt.Errorf("access token expired and no refresh token available (run 'devrecall auth google')")
	}

	fmt.Fprintf(os.Stderr, "Google token expired, refreshing...\n")
	refreshed, err := auth.RefreshGoogleToken(context.Background(), auth.RelayBaseURL, token.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}

	refreshed.Email = token.Email
	if err := store.Save("google", email, refreshed); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to persist refreshed token: %v\n", err)
	}

	return refreshed, nil
}

func defaultModelForProvider(provider string) string {
	switch provider {
	case "openai":
		return "gpt-5.4-mini"
	case "anthropic":
		return "claude-sonnet-4-6"
	case "ollama":
		return "gemma4"
	default:
		return ""
	}
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	result := append([]string{}, a...)
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// hasSlackThreads checks if any activities are Slack thread parents with messages to summarize.
func hasSlackThreads(activities []models.Activity) bool {
	for _, a := range activities {
		if a.Source == models.SourceSlack && strings.Contains(a.Metadata, `"thread_msgs"`) && !strings.Contains(a.Metadata, `"summary"`) {
			return true
		}
	}
	return false
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync activity from all configured sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd.Context())
		},
	}
}

func runSync(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	totalStored := 0

	// Sync git.
	if cfg.Git.Enabled {
		repos := cfg.Git.Repos
		if len(cfg.Git.ScanPaths) > 0 {
			fmt.Fprintf(os.Stderr, "Scanning for repos...")
			repos = mergeUnique(repos, git.DiscoverRepos(cfg.Git.ScanPaths))
			fmt.Fprintf(os.Stderr, " found %d\n", len(repos))
		}
		fmt.Fprintf(os.Stderr, "Detecting git emails...")
		emails := mergeUnique(cfg.Git.Emails, git.DetectEmails(repos))
		fmt.Fprintf(os.Stderr, " %d email(s)\n", len(emails))

		if len(repos) > 0 && len(emails) > 0 {
			fmt.Fprintf(os.Stderr, "Collecting commits from %d repo(s)...\n", len(repos))
			collector := git.New(repos, emails)
			activities, err := collector.Collect(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Git sync warning: %v\n", err)
				db.SetSyncError("git", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing git activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "Git: %d activities stored (%d new)\n", len(activities), n)
				}
				db.SetSyncState("git", "")
			} else {
				fmt.Fprintf(os.Stderr, "Git: no new activities\n")
				db.SetSyncState("git", "")
			}
		}
	}

	// Sync Slack.
	if cfg.Slack.Enabled && cfg.Slack.TeamID != "" {
		var token auth.SlackToken
		if err := tokenStore.Load("slack", cfg.Slack.TeamID, &token); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting Slack messages...\n")
			sc := slackcollector.New(token.AccessToken)
			activities, err := sc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Slack sync warning: %v\n", err)
				db.SetSyncError("slack", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing Slack activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "Slack: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Slack: no new activities\n")
			}

			db.SetSyncState("slack", "")

			// Auto-link Slack identity.
			resolver := identity.NewResolver(db)
			profile, err := sc.GetUserProfile(ctx, token.UserID)
			if err == nil && profile.Email != "" {
				if linked, err := resolver.AutoLinkSlack(token.UserID, profile.Email, profile.Name); err == nil {
					fmt.Fprintf(os.Stderr, "Slack identity linked to %s <%s>\n", linked.Name, linked.Email)
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Slack: token not found (run 'devrecall auth slack')\n")
		}
	}

	// Sync Google Calendar.
	if cfg.Calendar.Enabled && cfg.Calendar.Email != "" {
		gtoken, gtokenErr := loadGoogleToken(tokenStore, cfg.Calendar.Email)
		if gtokenErr == nil {
			fmt.Fprintf(os.Stderr, "Collecting calendar events...\n")
			cc := calcollector.New(gtoken.AccessToken)

			var activities []models.Activity
			syncState, _ := db.GetSyncState("calendar")
			if syncState != nil && syncState.Cursor != "" {
				acts, newToken, err := cc.CollectWithSyncToken(ctx, syncState.Cursor)
				if err != nil {
					// Sync token expired — do initial sync.
					fmt.Fprintf(os.Stderr, "Calendar: sync token expired, doing full sync...\n")
					acts, newToken, err = cc.InitialSync(ctx, 7*24*time.Hour)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Calendar sync failed: %v\n", err)
						db.SetSyncError("calendar", err.Error())
					} else {
						activities = acts
						db.SetSyncState("calendar", newToken)
					}
				} else {
					activities = acts
					db.SetSyncState("calendar", newToken)
				}
			} else {
				acts, newToken, err := cc.InitialSync(ctx, 7*24*time.Hour)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Calendar sync failed: %v\n", err)
					db.SetSyncError("calendar", err.Error())
				} else {
					activities = acts
					db.SetSyncState("calendar", newToken)
				}
			}

			if len(activities) > 0 {
				// Enrich identities from calendar attendees.
				resolver := identity.NewResolver(db)
				if enriched, err := resolver.EnrichFromCalendar(activities); err == nil && enriched > 0 {
					fmt.Fprintf(os.Stderr, "Calendar: enriched %d identities from attendees\n", enriched)
				}

				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing calendar activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "Calendar: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Calendar: no new events\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "Calendar: %v\n", gtokenErr)
		}
	}

	// Sync GitHub.
	if cfg.GitHub.Enabled && cfg.GitHub.Username != "" {
		var ghToken auth.GitHubToken
		if err := tokenStore.Load("github", cfg.GitHub.Username, &ghToken); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting GitHub activity...\n")
			gc := ghcollector.New(ghToken.AccessToken, cfg.GitHub.Username)
			activities, err := gc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
			if err != nil {
				fmt.Fprintf(os.Stderr, "GitHub sync warning: %v\n", err)
				db.SetSyncError("github", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing GitHub activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "GitHub: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "GitHub: no new activities\n")
			}
			db.SetSyncState("github", "")
		} else {
			fmt.Fprintf(os.Stderr, "GitHub: token not found (run 'devrecall auth github')\n")
		}
	}

	// Sync GitLab.
	if cfg.GitLab.Enabled && cfg.GitLab.Username != "" {
		var glToken auth.GitLabToken
		if err := tokenStore.Load("gitlab", cfg.GitLab.Username, &glToken); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting GitLab activity...\n")
			gc := glcollector.New(glToken.AccessToken, cfg.GitLab.Username, cfg.GitLab.BaseURL)
			activities, err := gc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
			if err != nil {
				fmt.Fprintf(os.Stderr, "GitLab sync warning: %v\n", err)
				db.SetSyncError("gitlab", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing GitLab activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "GitLab: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "GitLab: no new activities\n")
			}
			db.SetSyncState("gitlab", "")
		} else {
			fmt.Fprintf(os.Stderr, "GitLab: token not found (run 'devrecall auth gitlab')\n")
		}
	}

	// Sync Bitbucket.
	if cfg.Bitbucket.Enabled && cfg.Bitbucket.Username != "" {
		var bbToken auth.BitbucketToken
		if err := tokenStore.Load("bitbucket", cfg.Bitbucket.Username, &bbToken); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting Bitbucket activity...\n")
			bc := bbcollector.New(bbToken.Username, bbToken.AppPassword, bbToken.UUID, cfg.Bitbucket.Workspace)
			activities, err := bc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Bitbucket sync warning: %v\n", err)
				db.SetSyncError("bitbucket", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing Bitbucket activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "Bitbucket: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Bitbucket: no new activities\n")
			}
			db.SetSyncState("bitbucket", "")
		} else {
			fmt.Fprintf(os.Stderr, "Bitbucket: token not found (run 'devrecall auth bitbucket')\n")
		}
	}

	// Sync Jira.
	if cfg.Jira.Enabled && cfg.Jira.Email != "" {
		var atlToken auth.AtlassianToken
		if err := tokenStore.Load("jira", cfg.Jira.Email, &atlToken); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting Jira activity...\n")
			var jc *jiracollector.Collector
			if cfg.Jira.AuthMode == "api-token" && cfg.Jira.BaseURL != "" {
				jc = jiracollector.NewWithAPIToken(cfg.Jira.Email, atlToken.AccessToken, cfg.Jira.BaseURL)
			} else if len(atlToken.CloudSites) > 0 {
				jc = jiracollector.New(atlToken.AccessToken, atlToken.CloudSites[0].ID)
			} else {
				fmt.Fprintf(os.Stderr, "Jira: no base URL or cloud site in token\n")
			}
			if jc != nil {
				activities, err := jc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Jira sync warning: %v\n", err)
					db.SetSyncError("jira", err.Error())
				} else if len(activities) > 0 {
					n, err := db.InsertActivities(activities)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Storing Jira activities: %v\n", err)
					} else {
						totalStored += n
						fmt.Fprintf(os.Stderr, "Jira: %d activities stored (%d new)\n", len(activities), n)
					}
				} else {
					fmt.Fprintf(os.Stderr, "Jira: no new activities\n")
				}
				db.SetSyncState("jira", "")
			}
		} else {
			fmt.Fprintf(os.Stderr, "Jira: token not found (run 'devrecall auth jira')\n")
		}
	}

	// Sync Linear.
	if cfg.Linear.Enabled && cfg.Linear.Email != "" {
		var lt auth.LinearToken
		if err := tokenStore.Load("linear", cfg.Linear.Email, &lt); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting Linear activity...\n")
			lc := linearcollector.New(lt.AccessToken)
			activities, err := lc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Linear sync warning: %v\n", err)
				db.SetSyncError("linear", err.Error())
			} else if len(activities) > 0 {
				n, err := db.InsertActivities(activities)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Storing Linear activities: %v\n", err)
				} else {
					totalStored += n
					fmt.Fprintf(os.Stderr, "Linear: %d activities stored (%d new)\n", len(activities), n)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Linear: no new activities\n")
			}
			db.SetSyncState("linear", "")
		} else {
			fmt.Fprintf(os.Stderr, "Linear: token not found (run 'devrecall auth linear')\n")
		}
	}

	// Sync Confluence (reuses Atlassian/Jira auth).
	if cfg.Confluence.Enabled {
		var atlToken auth.AtlassianToken
		tokenKey := cfg.Jira.Email // reuse Jira token key
		if tokenKey == "" {
			tokenKey = "default"
		}
		if err := tokenStore.Load("jira", tokenKey, &atlToken); err == nil {
			fmt.Fprintf(os.Stderr, "Collecting Confluence pages...\n")
			var cc *confluencecollector.Collector
			if cfg.Jira.AuthMode == "api-token" && cfg.Jira.Email != "" {
				cc = confluencecollector.NewWithAPIToken(cfg.Jira.Email, atlToken.AccessToken, cfg.Jira.BaseURL+"/wiki")
			} else if len(atlToken.CloudSites) > 0 {
				cc = confluencecollector.New(atlToken.AccessToken, atlToken.CloudSites[0].ID)
			} else {
				fmt.Fprintf(os.Stderr, "Confluence: no cloud site found in Jira token\n")
				cc = nil
			}
			if cc != nil {
				activities, err := cc.CollectSince(ctx, time.Now().AddDate(0, 0, -7))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Confluence sync warning: %v\n", err)
					db.SetSyncError("confluence", err.Error())
				} else if len(activities) > 0 {
					n, err := db.InsertActivities(activities)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Storing Confluence activities: %v\n", err)
					} else {
						totalStored += n
						fmt.Fprintf(os.Stderr, "Confluence: %d activities stored (%d new)\n", len(activities), n)
					}
				} else {
					fmt.Fprintf(os.Stderr, "Confluence: no new pages\n")
				}
				db.SetSyncState("confluence", "")
			}
		} else {
			fmt.Fprintf(os.Stderr, "Confluence: Jira token not found (run 'devrecall auth jira')\n")
		}
	}

	fmt.Fprintf(os.Stderr, "\nSync complete. %d new activities stored.\n", totalStored)

	// Embed unembedded activities for vector search.
	if err := runEmbedMissing(ctx, db, cfg, tokenStore); err != nil {
		fmt.Fprintf(os.Stderr, "Embedding warning: %v\n", err)
	}

	// Quarterly auto-snapshot: check if any completed quarters lack a summary.
	if err := runQuarterlyAutoSnapshot(ctx, db, cfg, tokenStore, dir); err != nil {
		fmt.Fprintf(os.Stderr, "Quarterly snapshot warning: %v\n", err)
	}

	return nil
}

func runEmbedMissing(ctx context.Context, db *storage.DB, cfg *config.Config, tokenStore auth.TokenStore) error {
	embedder, err := embedding.FromConfig(cfg, tokenStore)
	if err != nil {
		// Embedding not configured — skip silently.
		return nil
	}

	const batchSize = 50
	totalEmbedded := 0

	for {
		ids, err := db.ListUnembeddedActivityIDs(batchSize)
		if err != nil {
			return fmt.Errorf("list unembedded: %w", err)
		}
		if len(ids) == 0 {
			break
		}

		if totalEmbedded == 0 {
			// Count total unembedded for progress on first batch.
			allIDs, _ := db.ListUnembeddedActivityIDs(0)
			fmt.Fprintf(os.Stderr, "Embedding %d activities...\n", len(allIDs))
		}

		activities, err := db.GetActivitiesByIDs(ids)
		if err != nil {
			return fmt.Errorf("get activities: %w", err)
		}

		// Build texts for embedding.
		texts := make([]string, len(activities))
		for i, a := range activities {
			text := a.Title
			if a.Content != "" {
				text += " " + a.Content
			}
			texts[i] = text
		}

		vectors, err := embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for i, a := range activities {
			if err := db.InsertEmbedding(a.ID, embedder.Name(), vectors[i]); err != nil {
				return fmt.Errorf("store embedding for activity %d: %w", a.ID, err)
			}
		}

		totalEmbedded += len(activities)
		fmt.Fprintf(os.Stderr, "  Embedded %d activities...\n", totalEmbedded)
	}

	if totalEmbedded > 0 {
		fmt.Fprintf(os.Stderr, "Embedding complete: %d activities.\n", totalEmbedded)
	}

	return nil
}

func runQuarterlyAutoSnapshot(ctx context.Context, db *storage.DB, cfg *config.Config, tokenStore auth.TokenStore, dir string) error {
	llmProvider, err := llm.FromConfig(cfg, tokenStore)
	if err != nil {
		// LLM not configured — skip auto-snapshot silently.
		return nil
	}

	sum := summarizer.NewLLMSummarizer(llmProvider).WithPromptLoader(promptLoader())
	gen := summarizer.NewPeriodicGenerator(db, sum, llmProvider)

	now := time.Now().UTC()
	generated, err := summarizer.AutoSnapshot(ctx, gen, now)
	if err != nil {
		return err
	}

	if generated > 0 {
		fmt.Fprintf(os.Stderr, "Auto-snapshot: generated %d quarterly %s.\n",
			generated, pluralize(generated, "summary", "summaries"))
	}

	return nil
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync health per source",
		Long:  "Displays the last sync time and activity count for each configured source.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
}

func runStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	counts, err := db.CountActivitiesBySource()
	if err != nil {
		return fmt.Errorf("counting activities: %w", err)
	}

	// All sources with their enabled state from config.
	type sourceInfo struct {
		name    string
		enabled bool
	}
	sources := []sourceInfo{
		{"git", cfg.Git.Enabled},
		{"slack", cfg.Slack.Enabled},
		{"calendar", cfg.Calendar.Enabled},
		{"github", cfg.GitHub.Enabled},
		{"gitlab", cfg.GitLab.Enabled},
		{"bitbucket", cfg.Bitbucket.Enabled},
		{"jira", cfg.Jira.Enabled},
		{"confluence", cfg.Confluence.Enabled},
		{"linear", cfg.Linear.Enabled},
	}

	fmt.Println("DevRecall Status")
	fmt.Println("================")
	fmt.Println()

	for _, src := range sources {
		if !src.enabled {
			fmt.Printf("  %-12s  disabled\n", src.name)
			continue
		}

		state, err := db.GetSyncState(src.name)
		if err != nil {
			fmt.Printf("  %-12s  enabled · error reading sync state\n", src.name)
			continue
		}

		count := counts[src.name]

		if state == nil {
			fmt.Printf("  %-12s  enabled · never synced · %d activities\n", src.name, count)
			continue
		}

		ago := formatTimeAgo(time.Since(state.SyncedAt))
		if state.LastError != "" {
			fmt.Printf("  %-12s  enabled · last sync failed %s · %d activities\n", src.name, ago, count)
			fmt.Printf("  %-12s  ⚠ %s\n", "", state.LastError)
		} else {
			fmt.Printf("  %-12s  enabled · synced %s · %d activities\n", src.name, ago, count)
		}
	}

	fmt.Println()

	// Show database path.
	if dbPath, err := config.DBPath(); err == nil {
		fmt.Printf("Database: %s\n", dbPath)
	}

	return nil
}

// formatTimeAgo formats a duration as a human-readable "X ago" string.
func formatTimeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func newChatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Interactive chat with your work history",
		Long:  "Start an interactive REPL that lets you ask natural language questions about your work history using RAG retrieval.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat()
		},
	}
}

func runChat() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	// LLM provider (required for chat). The agent loop needs tool calling.
	llmProvider, err := llm.FromConfig(cfg, tokenStore)
	if err != nil {
		return fmt.Errorf("LLM not configured — run 'devrecall auth' for your provider first: %w", err)
	}
	toolProvider, ok := llmProvider.(llm.ToolCallingProvider)
	if !ok {
		return fmt.Errorf("LLM provider %q does not support tool calling — chat requires Anthropic, OpenAI, or a tool-capable Ollama model", llmProvider.Name())
	}

	// Embedder is optional — semantic_search_activities will return an
	// error if it's nil, but the rest of the catalogue still works.
	embedder, err := embedding.FromConfig(cfg, tokenStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: embedding provider unavailable, semantic search disabled: %v\n", err)
		embedder = nil
	}

	registry := agenttools.NewRegistry(agenttools.Deps{
		DB:       db,
		Embedder: embedder,
	})
	loop := agent.NewLoop(toolProvider, registry, agent.LoopOptions{})

	// Pre-agent freshness sync: keep stale local sources up to date
	// before each query. Built from cfg.Chat.SyncFreshness.
	checker := api.BuildFreshnessChecker(cfg, db)
	syncers := api.BuildFreshnessSyncers(cfg, db)

	session := chat.NewSession(os.Stdin, os.Stdout, loop, db).
		WithFreshness(checker, syncers)

	return session.Run(context.Background())
}

func newSummarizeCmd() *cobra.Command {
	var periodFlag string
	var sinceFlag string

	cmd := &cobra.Command{
		Use:   "summarize",
		Short: "Generate periodic summaries of your work",
		Long:  "Generates missing daily, weekly, monthly, or quarterly summaries. Summaries are stored in the database and used by chat and brag/perf-review commands.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSummarize(periodFlag, sinceFlag)
		},
	}
	cmd.Flags().StringVar(&periodFlag, "period", "", "Period type to generate: daily, weekly, monthly, quarterly, or 'all' (default: all)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "Start date (YYYY-MM-DD, default: 30 days ago for daily, 90 days for others)")
	return cmd
}

func runSummarize(periodFlag, sinceFlag string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	llmProvider, err := llm.FromConfig(cfg, tokenStore)
	if err != nil {
		return fmt.Errorf("LLM not configured — run 'devrecall auth' for your provider first: %w", err)
	}

	sum := summarizer.NewLLMSummarizer(llmProvider).WithPromptLoader(promptLoader())
	gen := summarizer.NewPeriodicGenerator(db, sum, llmProvider)

	now := time.Now().UTC()
	ctx := context.Background()

	periods := []string{
		summarizer.PeriodDaily,
		summarizer.PeriodWeekly,
		summarizer.PeriodMonthly,
		summarizer.PeriodQuarterly,
	}

	if periodFlag != "" && periodFlag != "all" {
		periods = []string{periodFlag}
	}

	for _, period := range periods {
		since := defaultSince(period, now)
		if sinceFlag != "" {
			parsed, err := time.Parse("2006-01-02", sinceFlag)
			if err != nil {
				return fmt.Errorf("invalid --since date %q: %w", sinceFlag, err)
			}
			since = parsed
		}

		fmt.Fprintf(os.Stderr, "Checking %s summaries since %s...\n", period, since.Format("2006-01-02"))
		n, err := gen.GenerateMissing(ctx, period, since, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating %s summaries: %v\n", period, err)
			continue
		}
		if n > 0 {
			fmt.Printf("Generated %d %s %s.\n", n, period, pluralize(n, "summary", "summaries"))
		} else {
			fmt.Printf("No missing %s summaries.\n", period)
		}
	}

	return nil
}

func defaultSince(period string, now time.Time) time.Time {
	switch period {
	case summarizer.PeriodDaily:
		return now.AddDate(0, 0, -30)
	case summarizer.PeriodWeekly:
		return now.AddDate(0, -3, 0)
	case summarizer.PeriodMonthly:
		return now.AddDate(-1, 0, 0)
	case summarizer.PeriodQuarterly:
		return now.AddDate(-1, 0, 0)
	default:
		return now.AddDate(0, -1, 0)
	}
}

// promptLoader returns a PromptLoader that checks ~/.devrecall/prompts/ for custom templates.
func promptLoader() *summarizer.PromptLoader {
	dir, err := config.Dir()
	if err != nil {
		return summarizer.NewPromptLoader("")
	}
	return summarizer.NewPromptLoader(filepath.Join(dir, "prompts"))
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func newBragCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "brag <period>",
		Short: "Generate a brag document for a date range",
		Long: `Generate a brag document highlighting your key accomplishments.

Period formats:
  Q1-2026, Q2-2026         Quarterly (Jan-Mar, Apr-Jun, etc.)
  2026-03                   Monthly (March 2026)
  2026-03-01..2026-03-31    Date range
  last-month                Relative period
  last-quarter              Relative period`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrag(args[0])
		},
	}
}

func runBrag(period string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}

	after, before, err := parsePeriodArg(period)
	if err != nil {
		return fmt.Errorf("invalid period %q: %w", period, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(os.Stderr, "Generating brag document for %s to %s...\n",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))

	// Gather activities.
	activities, err := db.ListActivities(storage.ActivityFilter{
		After:  after,
		Before: before,
	})
	if err != nil {
		return fmt.Errorf("querying activities: %w", err)
	}

	activities = privacy.Apply(activities, cfg.Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	// Gather existing summaries for the period.
	var childSummaries []models.Summary
	for _, pt := range []string{"daily", "weekly", "monthly"} {
		sums, _ := db.ListSummariesInRange(pt, after, before)
		childSummaries = append(childSummaries, sums...)
	}

	fmt.Fprintf(os.Stderr, "Found %d activities and %d summaries.\n",
		len(activities), len(childSummaries))

	// Resolve LLM provider.
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	var s summarizer.Summarizer
	if p, llmErr := llm.FromConfig(cfg, tokenStore); llmErr == nil {
		fmt.Fprintf(os.Stderr, "Generating with %s...\n", p.Name())
		s = summarizer.NewLLMSummarizer(p).WithPromptLoader(promptLoader())
	} else {
		s = summarizer.NewTemplateSummarizer()
	}

	text, err := s.BragDoc(activities, childSummaries)
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("brag-%s-to-%s.md",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))
	return saveReport(text, filename)
}

func newPerfReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "perf-review <period>",
		Short: "Generate a structured performance review document",
		Long: `Generate a performance review document with evidence-based sections.

Period formats: Q1-2026, 2026-03, 2026-03-01..2026-03-31, last-month, last-quarter`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPerfReview(args[0])
		},
	}
}

func runPerfReview(period string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}

	after, before, err := parsePeriodArg(period)
	if err != nil {
		return fmt.Errorf("invalid period %q: %w", period, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(os.Stderr, "Generating performance review for %s to %s...\n",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))

	activities, err := db.ListActivities(storage.ActivityFilter{
		After:  after,
		Before: before,
	})
	if err != nil {
		return fmt.Errorf("querying activities: %w", err)
	}

	activities = privacy.Apply(activities, cfg.Privacy)
	activities = summarizer.DeduplicateActivities(activities)

	var childSummaries []models.Summary
	for _, pt := range []string{"daily", "weekly", "monthly"} {
		sums, _ := db.ListSummariesInRange(pt, after, before)
		childSummaries = append(childSummaries, sums...)
	}

	fmt.Fprintf(os.Stderr, "Found %d activities and %d summaries.\n",
		len(activities), len(childSummaries))

	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	var s summarizer.Summarizer
	if p, llmErr := llm.FromConfig(cfg, tokenStore); llmErr == nil {
		fmt.Fprintf(os.Stderr, "Generating with %s...\n", p.Name())
		s = summarizer.NewLLMSummarizer(p).WithPromptLoader(promptLoader())
	} else {
		s = summarizer.NewTemplateSummarizer()
	}

	text, err := s.PerfReview(activities, childSummaries)
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("perf-review-%s-to-%s.md",
		after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))
	return saveReport(text, filename)
}

func newSearchCmd() *cobra.Command {
	var sourceFlag string
	var limitFlag int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search activities using full-text search (no LLM)",
		Long:  "Performs FTS5 keyword search across all activities. Fast, offline, no LLM required.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			return runSearch(query, sourceFlag, limitFlag)
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "", "Filter by source (git, slack, calendar, github, gitlab, bitbucket, jira, linear)")
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum number of results")
	return cmd
}

func runSearch(query, sourceFilter string, limit int) error {
	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	filter := storage.ActivityFilter{}
	if sourceFilter != "" {
		filter.Source = models.Source(sourceFilter)
	}

	results, err := db.SearchFTS(query, filter, limit)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d results for %q:\n\n", len(results), query)
	for i, r := range results {
		a := r.Activity
		repo := repoFromMetadata(a.Metadata)
		label := fmt.Sprintf("%s | %s", a.Source, a.Type)
		if repo != "" {
			label = fmt.Sprintf("%s | %s | %s", a.Source, repo, a.Type)
		}
		fmt.Printf("%d. [%s] %s | %s\n", i+1, a.Timestamp.Format("2006-01-02"), label, a.Title)
		if a.Content != "" {
			content := a.Content
			if len(content) > 120 {
				content = content[:120] + "..."
			}
			fmt.Printf("   %s\n", content)
		}
	}

	return nil
}

func newLogCmd() *cobra.Command {
	var atFlag string
	var tagsFlag string
	var peopleFlag string

	cmd := &cobra.Command{
		Use:   "log <text>",
		Short: "Log a manual activity (in-person chat, call, decision, etc.)",
		Long: `Records a manual activity that the automatic collectors can't see.

Useful for capturing in-person conversations, whiteboard sessions, calls,
decisions, or any work event that doesn't show up in Git/Slack/Calendar/etc.

Manual events are stored as first-class activities (source=manual) and are
included in standup, search, chat, and RAG retrieval just like any other
activity.

Examples:
  devrecall log "Talked to mobile team about API contract"
  devrecall log "Whiteboarded retry strategy with Anna" --people anna@example.com
  devrecall log "Decided to ship gradual rollout" --tags decision,deploy
  devrecall log "Quick call with PM" --at "2026-04-08 14:30"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			return runLog(text, atFlag, tagsFlag, peopleFlag)
		},
	}
	cmd.Flags().StringVar(&atFlag, "at", "", "Event timestamp (YYYY-MM-DD or YYYY-MM-DD HH:MM, default: now)")
	cmd.Flags().StringVar(&tagsFlag, "tags", "", "Comma-separated tags (e.g. decision,deploy)")
	cmd.Flags().StringVar(&peopleFlag, "people", "", "Comma-separated people involved (names or emails)")
	return cmd
}

func runLog(text, atFlag, tagsFlag, peopleFlag string) error {
	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	activity, err := buildManualActivity(text, atFlag, tagsFlag, peopleFlag, time.Now())
	if err != nil {
		return err
	}

	if self, err := db.GetSelfIdentity(); err == nil && self != nil {
		activity.IdentityID = self.ID
	}

	id, err := db.InsertActivity(activity)
	if err != nil {
		return fmt.Errorf("failed to log activity: %w", err)
	}

	fmt.Printf("Logged manual activity #%d at %s\n", id, activity.Timestamp.Format("2006-01-02 15:04"))
	fmt.Printf("  %s\n", activity.Title)
	return nil
}

// buildManualActivity constructs a manual activity from CLI inputs.
// Exposed for testing. `now` is the fallback timestamp when --at is empty.
func buildManualActivity(text, atFlag, tagsFlag, peopleFlag string, now time.Time) (models.Activity, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return models.Activity{}, fmt.Errorf("activity text cannot be empty")
	}

	ts := now
	if atFlag != "" {
		parsed, err := parseManualTimestamp(atFlag, now.Location())
		if err != nil {
			return models.Activity{}, err
		}
		ts = parsed
	}

	tags := splitCSV(tagsFlag)
	people := splitCSV(peopleFlag)

	title := text
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	if len(title) > 200 {
		title = title[:200]
	}

	metadata := map[string]any{}
	if len(tags) > 0 {
		metadata["tags"] = tags
	}
	if len(people) > 0 {
		metadata["people"] = people
	}
	var metadataStr string
	if len(metadata) > 0 {
		b, _ := json.Marshal(metadata)
		metadataStr = string(b)
	}

	// Stable source_id derived from timestamp + text to allow re-running idempotently.
	sourceID := fmt.Sprintf("manual-%d-%s", ts.UnixNano(), shortHash(text))

	return models.Activity{
		Source:    models.SourceManual,
		SourceID:  sourceID,
		Type:      models.TypeNote,
		Title:     title,
		Content:   text,
		Metadata:  metadataStr,
		Timestamp: ts,
	}, nil
}

func parseManualTimestamp(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q (use YYYY-MM-DD or YYYY-MM-DD HH:MM)", s)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// shortHash returns a short, deterministic identifier for a string.
// Not cryptographic — just to give manual entries unique source IDs.
func shortHash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

func newTimelineCmd() *cobra.Command {
	var sourceFlag string

	cmd := &cobra.Command{
		Use:   "timeline <period>",
		Short: "Show chronological activity view for a period",
		Long: `Display activities in chronological order, grouped by day.

Period formats: Q1-2026, 2026-03, 2026-03-01..2026-03-31, last-month, last-quarter`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTimeline(args[0], sourceFlag)
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "", "Filter by source (git, slack, calendar, github, gitlab, bitbucket, jira, linear)")
	return cmd
}

func runTimeline(period, sourceFilter string) error {
	after, before, err := parsePeriodArg(period)
	if err != nil {
		return fmt.Errorf("invalid period %q: %w", period, err)
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	filter := storage.ActivityFilter{
		After:  after,
		Before: before,
	}
	if sourceFilter != "" {
		filter.Source = models.Source(sourceFilter)
	}

	activities, err := db.ListActivities(filter)
	if err != nil {
		return fmt.Errorf("querying activities: %w", err)
	}

	if len(activities) == 0 {
		fmt.Printf("No activities found for %s to %s.\n",
			after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))
		return nil
	}

	fmt.Fprintf(os.Stderr, "%d activities from %s to %s\n\n",
		len(activities), after.Format("2006-01-02"), before.AddDate(0, 0, -1).Format("2006-01-02"))

	// Activities come sorted DESC from ListActivities; reverse to chronological.
	for i, j := 0, len(activities)-1; i < j; i, j = i+1, j-1 {
		activities[i], activities[j] = activities[j], activities[i]
	}

	// Group by day and print.
	var currentDay string
	for _, a := range activities {
		day := a.Timestamp.Format("2006-01-02 (Monday)")
		if day != currentDay {
			if currentDay != "" {
				fmt.Println()
			}
			fmt.Printf("── %s ──\n", day)
			currentDay = day
		}

		ts := a.Timestamp.Format("15:04")
		repo := repoFromMetadata(a.Metadata)
		if repo != "" {
			fmt.Printf("  %s  %-10s %-16s %-14s %s\n", ts, a.Source, repo, a.Type, a.Title)
		} else {
			fmt.Printf("  %s  %-10s %-14s %s\n", ts, a.Source, a.Type, a.Title)
		}
	}
	fmt.Println()

	return nil
}

func newPruneCmd() *cobra.Command {
	var olderThan string
	var keepSummaries bool
	var dryRun bool
	var force bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old activity data to reclaim space",
		Long:  "Removes activities (and their embeddings) older than the specified duration. Summaries are preserved by default with --keep-summaries.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrune(olderThan, keepSummaries, dryRun, force)
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "1y", "Delete data older than this duration (e.g., 6m, 1y, 2y)")
	cmd.Flags().BoolVar(&keepSummaries, "keep-summaries", true, "Keep all summaries (only delete raw activities)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be deleted without actually deleting")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// parseOlderThan parses duration strings like "6m", "1y", "2y", "90d".
func parseOlderThan(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q — use e.g. 6m, 1y, 90d", s)
	}

	numStr := s[:len(s)-1]
	unit := s[len(s)-1]

	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid duration %q — number must be positive", s)
	}

	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'm':
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	case 'y':
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration unit %q — use d (days), m (months), or y (years)", string(unit))
	}
}

func runPrune(olderThan string, keepSummaries, dryRun, force bool) error {
	dur, err := parseOlderThan(olderThan)
	if err != nil {
		return err
	}

	cutoff := time.Now().UTC().Add(-dur)

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	// Count what would be deleted.
	actCount, err := db.CountActivitiesBefore(cutoff)
	if err != nil {
		return err
	}

	var keepTypes []string
	if keepSummaries {
		keepTypes = []string{"daily", "weekly", "monthly", "quarterly"}
	}

	sumCount := 0
	if !keepSummaries {
		sumCount, err = db.CountSummariesBefore(cutoff, keepTypes)
		if err != nil {
			return err
		}
	}

	if actCount == 0 && sumCount == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}

	fmt.Printf("Data older than %s (before %s):\n", olderThan, cutoff.Format("2006-01-02"))
	fmt.Printf("  Activities to delete: %d\n", actCount)
	if keepSummaries {
		fmt.Println("  Summaries: kept (--keep-summaries)")
	} else {
		fmt.Printf("  Summaries to delete: %d\n", sumCount)
	}
	fmt.Println("  Embeddings: cleaned automatically (CASCADE)")

	if dryRun {
		fmt.Println("\nDry run — no data deleted.")
		return nil
	}

	if !force {
		fmt.Print("\nProceed? (y/n): ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	deleted, err := db.PruneActivities(cutoff)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted %d %s.\n", deleted, pluralize(deleted, "activity", "activities"))

	if !keepSummaries {
		delSum, err := db.PruneSummaries(cutoff, keepTypes)
		if err != nil {
			return err
		}
		if delSum > 0 {
			fmt.Printf("Deleted %d %s.\n", delSum, pluralize(delSum, "summary", "summaries"))
		}
	}

	fmt.Println("Done. Run 'devrecall status' to verify.")
	return nil
}

// saveReport writes text to ~/.devrecall/reports/<filename> and opens it.
func saveReport(text, filename string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}

	reportsDir := filepath.Join(dir, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return fmt.Errorf("create reports dir: %w", err)
	}

	path := filepath.Join(reportsDir, filename)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Saved to %s\n", path)

	// Open in default app (macOS: open, Linux: xdg-open).
	var openCmd string
	switch {
	case fileExists("/usr/bin/open"):
		openCmd = "open"
	case fileExists("/usr/bin/xdg-open"):
		openCmd = "xdg-open"
	}
	if openCmd != "" {
		exec.Command(openCmd, path).Start()
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// repoFromMetadata extracts the repo name from an activity's JSON metadata.
func repoFromMetadata(metadata string) string {
	if metadata == "" {
		return ""
	}
	var m struct {
		Repo string `json:"repo"`
	}
	if json.Unmarshal([]byte(metadata), &m) == nil && m.Repo != "" {
		return m.Repo
	}
	return ""
}

func formatBytes(b int) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
}

// parsePeriodArg parses period strings like Q1-2026, 2026-03, last-month, etc.
func parsePeriodArg(s string) (after, before time.Time, err error) {
	now := time.Now().UTC()

	switch strings.ToLower(s) {
	case "last-month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
		return first, first.AddDate(0, 1, 0), nil
	case "this-month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return first, first.AddDate(0, 1, 0), nil
	case "last-quarter":
		quarter := (int(now.Month()) - 1) / 3
		qStart := time.Date(now.Year(), time.Month(quarter*3+1), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -3, 0)
		return qStart, qStart.AddDate(0, 3, 0), nil
	case "this-quarter":
		quarter := (int(now.Month()) - 1) / 3
		qStart := time.Date(now.Year(), time.Month(quarter*3+1), 1, 0, 0, 0, 0, time.UTC)
		return qStart, qStart.AddDate(0, 3, 0), nil
	}

	// Try Q1-2026, Q2-2026, etc.
	if len(s) >= 7 && (s[0] == 'Q' || s[0] == 'q') && s[2] == '-' {
		q := int(s[1] - '0')
		if q < 1 || q > 4 {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid quarter: Q%d", q)
		}
		year, parseErr := strconv.Atoi(s[3:])
		if parseErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid year in %q", s)
		}
		qStart := time.Date(year, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
		return qStart, qStart.AddDate(0, 3, 0), nil
	}

	// Try range: 2026-03-01..2026-03-31
	if idx := strings.Index(s, ".."); idx > 0 {
		a, errA := time.Parse("2006-01-02", s[:idx])
		b, errB := time.Parse("2006-01-02", s[idx+2:])
		if errA != nil {
			return time.Time{}, time.Time{}, errA
		}
		if errB != nil {
			return time.Time{}, time.Time{}, errB
		}
		return a, b.AddDate(0, 0, 1), nil
	}

	// Try YYYY-MM
	if t, parseErr := time.Parse("2006-01", s); parseErr == nil {
		return t, t.AddDate(0, 1, 0), nil
	}

	return time.Time{}, time.Time{}, fmt.Errorf("unrecognized period format")
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage DevRecall configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize DevRecall configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Init()
			if err != nil {
				return fmt.Errorf("failed to initialize config: %w", err)
			}
			fmt.Printf("Configuration initialized at %s\n", cfg.Path())
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			fmt.Println(cfg)
			return nil
		},
	})

	promptsCmd := &cobra.Command{
		Use:   "prompts",
		Short: "Manage prompt templates",
	}

	promptsCmd.AddCommand(&cobra.Command{
		Use:   "export",
		Short: "Export default prompt templates for customization",
		Long:  "Writes built-in prompt templates to ~/.devrecall/prompts/ so you can edit them. Existing customizations are not overwritten.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.Dir()
			if err != nil {
				return err
			}
			loader := summarizer.NewPromptLoader(filepath.Join(dir, "prompts"))
			if err := loader.ExportDefaults(); err != nil {
				return err
			}
			fmt.Printf("Prompt templates exported to %s/prompts/\n", dir)
			fmt.Println("Edit any .txt file to customize. Delete a file to revert to the built-in default.")
			return nil
		},
	})

	promptsCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show which prompts are customized",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.Dir()
			if err != nil {
				return err
			}
			loader := summarizer.NewPromptLoader(filepath.Join(dir, "prompts"))
			for _, pt := range summarizer.AllPromptTypes() {
				status := "built-in"
				if loader.IsCustom(pt) {
					status = "custom"
				}
				fmt.Printf("  %-14s %s\n", pt, status)
			}
			return nil
		},
	})

	cmd.AddCommand(promptsCmd)

	return cmd
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication for external services",
	}

	cmd.AddCommand(newAuthSlackCmd())
	cmd.AddCommand(newAuthGoogleCmd())
	cmd.AddCommand(newAuthGitHubCmd())
	cmd.AddCommand(newAuthGitLabCmd())
	cmd.AddCommand(newAuthBitbucketCmd())
	cmd.AddCommand(newAuthJiraCmd())
	cmd.AddCommand(newAuthConfluenceCmd())
	cmd.AddCommand(newAuthLinearCmd())
	cmd.AddCommand(newAuthOpenAICmd())
	cmd.AddCommand(newAuthAnthropicCmd())
	cmd.AddCommand(newAuthStatusCmd())

	return cmd
}

func newAuthSlackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "slack",
		Short: "Connect your Slack workspace via OAuth",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			fmt.Println("Opening browser for Slack authorization...")
			fmt.Println("Waiting for approval (up to 2 minutes)...")

			token, err := auth.SlackOAuth(cmd.Context(), auth.DefaultSlackOAuthConfig())
			if err != nil {
				return fmt.Errorf("slack auth failed: %w", err)
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("slack", token.TeamID, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.Slack.Enabled = true
			cfg.Slack.TeamID = token.TeamID
			cfg.Slack.TeamName = token.TeamName
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nSlack connected! Workspace: %s (%s)\n", token.TeamName, token.TeamID)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch Slack activity.")
			return nil
		},
	}
}

func newAuthGoogleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "google",
		Short: "Connect your Google account for Calendar access",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			fmt.Println("Opening browser for Google authorization...")
			fmt.Println("Waiting for approval (up to 2 minutes)...")

			token, err := auth.GoogleOAuth(cmd.Context(), auth.DefaultGoogleOAuthConfig())
			if err != nil {
				return fmt.Errorf("google auth failed: %w", err)
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("google", token.Email, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.Calendar.Enabled = true
			cfg.Calendar.Email = token.Email
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nGoogle connected! Account: %s\n", token.Email)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch calendar events.")
			return nil
		},
	}
}

func newAuthGitHubCmd() *cobra.Command {
	var method string

	cmd := &cobra.Command{
		Use:   "github",
		Short: "Connect your GitHub account (OAuth, PAT, or gh CLI)",
		Long:  "Authenticates with GitHub using OAuth (default), a personal access token (--method pat), or the gh CLI (--method gh-cli).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}

			var token *auth.GitHubToken

			switch method {
			case "oauth":
				fmt.Println("Opening browser for GitHub authorization...")
				fmt.Println("Waiting for approval (up to 2 minutes)...")
				token, err = auth.GitHubOAuth(cmd.Context(), auth.DefaultGitHubOAuthConfig())
				if err != nil {
					return fmt.Errorf("github oauth failed: %w", err)
				}

			case "pat":
				fmt.Print("Enter your GitHub personal access token: ")
				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					return fmt.Errorf("no input received")
				}
				pat := strings.TrimSpace(scanner.Text())
				if pat == "" {
					return fmt.Errorf("token cannot be empty")
				}
				fmt.Println("Validating token...")
				token, err = auth.ValidateGitHubPAT(cmd.Context(), pat, auth.DefaultGitHubPATConfig())
				if err != nil {
					return fmt.Errorf("invalid token: %w", err)
				}

			case "gh-cli":
				fmt.Println("Reading token from gh CLI...")
				token, err = auth.GitHubFromGHCLI(cmd.Context(), auth.DefaultGitHubPATConfig())
				if err != nil {
					return fmt.Errorf("gh cli auth failed: %w", err)
				}

			default:
				return fmt.Errorf("unknown method %q (use oauth, pat, or gh-cli)", method)
			}

			if err := store.Save("github", token.Username, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.GitHub.Enabled = true
			cfg.GitHub.Username = token.Username
			cfg.GitHub.AuthMode = method
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nGitHub connected! User: %s (via %s)\n", token.Username, method)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch GitHub activity.")
			return nil
		},
	}
	cmd.Flags().StringVar(&method, "method", "oauth", "Auth method: oauth, pat, or gh-cli")
	return cmd
}

func newAuthGitLabCmd() *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "gitlab",
		Short: "Connect your GitLab account via personal access token",
		Long:  "Authenticates with GitLab using a personal access token (PAT). Use --base-url for self-hosted instances.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			fmt.Print("Enter your GitLab personal access token (Settings → Access Tokens): ")
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			pat := strings.TrimSpace(scanner.Text())
			if pat == "" {
				return fmt.Errorf("token cannot be empty")
			}

			fmt.Println("Validating token...")
			authCfg := auth.DefaultGitLabAuthConfig()
			if baseURL != "" {
				authCfg.BaseURL = baseURL
			}

			token, err := auth.ValidateGitLabPAT(cmd.Context(), pat, authCfg)
			if err != nil {
				return fmt.Errorf("invalid token: %w", err)
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("gitlab", token.Username, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.GitLab.Enabled = true
			cfg.GitLab.Username = token.Username
			cfg.GitLab.BaseURL = token.BaseURL
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nGitLab connected! User: %s (%s)\n", token.Username, token.BaseURL)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch GitLab activity.")
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "", "GitLab instance URL (default: https://gitlab.com)")
	return cmd
}

func newAuthBitbucketCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bitbucket",
		Short: "Connect your Bitbucket account via app password",
		Long:  "Authenticates with Bitbucket using your username and an app password. Create one at Bitbucket Settings → App passwords.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			scanner := bufio.NewScanner(os.Stdin)

			fmt.Print("Enter your Bitbucket username: ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			username := strings.TrimSpace(scanner.Text())
			if username == "" {
				return fmt.Errorf("username cannot be empty")
			}

			fmt.Print("Enter your Bitbucket app password: ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			appPassword := strings.TrimSpace(scanner.Text())
			if appPassword == "" {
				return fmt.Errorf("app password cannot be empty")
			}

			fmt.Print("Enter your default workspace (e.g. your-team): ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			workspace := strings.TrimSpace(scanner.Text())
			if workspace == "" {
				return fmt.Errorf("workspace cannot be empty")
			}

			fmt.Println("Validating credentials...")
			token, err := auth.ValidateBitbucketAppPassword(cmd.Context(), username, appPassword, auth.DefaultBitbucketAuthConfig())
			if err != nil {
				return fmt.Errorf("invalid credentials: %w", err)
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("bitbucket", token.Username, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.Bitbucket.Enabled = true
			cfg.Bitbucket.Username = token.Username
			cfg.Bitbucket.Workspace = workspace
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nBitbucket connected! User: %s, workspace: %s\n", token.Username, workspace)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch Bitbucket activity.")
			return nil
		},
	}
}

func newAuthJiraCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jira",
		Short: "Connect your Jira account via API token",
		Long:  "Authenticates with Jira using your email and an API token. Create one at id.atlassian.com/manage-profile/security/api-tokens.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			scanner := bufio.NewScanner(os.Stdin)

			fmt.Print("Enter your Jira base URL (e.g. https://mycompany.atlassian.net): ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			baseURL := strings.TrimRight(strings.TrimSpace(scanner.Text()), "/")
			if baseURL == "" {
				return fmt.Errorf("base URL cannot be empty")
			}

			fmt.Print("Enter your Atlassian account email: ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			email := strings.TrimSpace(scanner.Text())
			if email == "" {
				return fmt.Errorf("email cannot be empty")
			}

			fmt.Print("Enter your Jira API token: ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			apiToken := strings.TrimSpace(scanner.Text())
			if apiToken == "" {
				return fmt.Errorf("API token cannot be empty")
			}

			fmt.Println("Validating credentials...")
			token, err := auth.ValidateJiraAPIToken(cmd.Context(), email, apiToken, baseURL, auth.DefaultJiraAPITokenConfig())
			if err != nil {
				return fmt.Errorf("invalid credentials: %w", err)
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("jira", email, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.Jira.Enabled = true
			cfg.Jira.BaseURL = baseURL
			cfg.Jira.AuthMode = "api-token"
			cfg.Jira.Email = email
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nJira connected! Account: %s @ %s\n", token.Email, baseURL)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch Jira activity.")
			return nil
		},
	}
}

func newAuthConfluenceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "confluence",
		Short: "Enable Confluence (reuses Jira API token)",
		Long:  "Confluence shares the Atlassian API token stored by 'devrecall auth jira'. This command only flips the enabled flag in config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if !cfg.Jira.Enabled || cfg.Jira.Email == "" {
				return fmt.Errorf("Jira is not connected — run 'devrecall auth jira' first (Confluence reuses the Atlassian token)")
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			var atlToken auth.AtlassianToken
			if err := store.Load("jira", cfg.Jira.Email, &atlToken); err != nil {
				return fmt.Errorf("loading Jira token: %w (run 'devrecall auth jira' first)", err)
			}

			cfg.Confluence.Enabled = true
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Println("Confluence enabled. Run 'devrecall sync' to fetch Confluence pages.")
			return nil
		},
	}
}

func newAuthLinearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "linear",
		Short: "Connect your Linear account via API key",
		Long:  "Authenticates with Linear using a personal API key. Create one at linear.app/settings/api.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			scanner := bufio.NewScanner(os.Stdin)
			fmt.Print("Enter your Linear API key: ")
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			apiKey := strings.TrimSpace(scanner.Text())
			if apiKey == "" {
				return fmt.Errorf("API key cannot be empty")
			}

			fmt.Println("Validating credentials...")
			token, err := auth.ValidateLinearAPIKey(cmd.Context(), apiKey, auth.DefaultLinearAPIKeyConfig())
			if err != nil {
				return fmt.Errorf("invalid credentials: %w", err)
			}

			tokenKey := token.Email
			if tokenKey == "" {
				tokenKey = token.UserID
			}
			if tokenKey == "" {
				tokenKey = "default"
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}
			if err := store.Save("linear", tokenKey, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			cfg.Linear.Enabled = true
			cfg.Linear.AuthMode = "api-key"
			cfg.Linear.Email = tokenKey
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("\nLinear connected! User: %s <%s>\n", token.UserName, token.Email)
			fmt.Println("Token stored securely. Run 'devrecall sync' to fetch Linear activity.")
			return nil
		},
	}
}

func newAuthOpenAICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "openai",
		Short: "Store your OpenAI API key",
		Long:  "Saves an OpenAI API key for LLM-powered features. Get your key at platform.openai.com.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return storeLLMKey("openai", "OpenAI", "platform.openai.com")
		},
	}
}

func newAuthAnthropicCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "anthropic",
		Short: "Store your Anthropic API key",
		Long:  "Saves an Anthropic API key for LLM-powered features. Get your key at console.anthropic.com.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return storeLLMKey("anthropic", "Anthropic", "console.anthropic.com")
		},
	}
}

func storeLLMKey(provider, displayName, keyURL string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Printf("Enter your %s API key (from %s): ", displayName, keyURL)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input received")
	}
	apiKey := scanner.Text()
	if apiKey == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return err
	}

	if err := store.Save("llm", provider, llm.APIKeyToken{APIKey: apiKey}); err != nil {
		return fmt.Errorf("saving API key: %w", err)
	}

	cfg.LLM.Provider = provider
	cfg.LLM.Model = defaultModelForProvider(provider)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("\n%s API key stored. Provider set to %q, model set to %q.\n", displayName, provider, cfg.LLM.Model)
	return nil
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for all services",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			store, err := auth.NewTokenStore(cfg.TokenStorage, dir)
			if err != nil {
				return err
			}

			fmt.Println("Authentication status:")
			fmt.Println()

			// Git — always local, no auth needed.
			status := "disabled"
			if cfg.Git.Enabled {
				status = "enabled (local, no auth needed)"
			}
			fmt.Printf("  Git:      %s\n", status)

			// Slack
			if cfg.Slack.Enabled && cfg.Slack.TeamID != "" {
				var token auth.SlackToken
				if err := store.Load("slack", cfg.Slack.TeamID, &token); err == nil {
					fmt.Printf("  Slack:    connected (%s)\n", cfg.Slack.TeamName)
				} else {
					fmt.Println("  Slack:    configured but token missing (run 'devrecall auth slack')")
				}
			} else {
				fmt.Println("  Slack:    not connected")
			}

			// LLM
			switch cfg.LLM.Provider {
			case "ollama", "":
				model := cfg.LLM.Model
				if model == "" {
					model = "gemma4"
				}
				fmt.Printf("  LLM:      ollama (model: %s)\n", model)
			case "openai":
				var token llm.APIKeyToken
				if err := store.Load("llm", "openai", &token); err == nil {
					fmt.Printf("  LLM:      openai (model: %s)\n", cfg.LLM.Model)
				} else {
					fmt.Println("  LLM:      openai configured but key missing (run 'devrecall auth openai')")
				}
			case "anthropic":
				var token llm.APIKeyToken
				if err := store.Load("llm", "anthropic", &token); err == nil {
					fmt.Printf("  LLM:      anthropic (model: %s)\n", cfg.LLM.Model)
				} else {
					fmt.Println("  LLM:      anthropic configured but key missing (run 'devrecall auth anthropic')")
				}
			default:
				fmt.Printf("  LLM:      %s (unknown provider)\n", cfg.LLM.Provider)
			}

			// Calendar
			if cfg.Calendar.Enabled && cfg.Calendar.Email != "" {
				var gtoken auth.GoogleToken
				if err := store.Load("google", cfg.Calendar.Email, &gtoken); err == nil {
					fmt.Printf("  Calendar: connected (%s)\n", cfg.Calendar.Email)
				} else {
					fmt.Println("  Calendar: configured but token missing (run 'devrecall auth google')")
				}
			} else {
				fmt.Println("  Calendar: not connected")
			}

			// GitHub
			if cfg.GitHub.Enabled && cfg.GitHub.Username != "" {
				var ghToken auth.GitHubToken
				if err := store.Load("github", cfg.GitHub.Username, &ghToken); err == nil {
					fmt.Printf("  GitHub:   connected (%s, via %s)\n", cfg.GitHub.Username, cfg.GitHub.AuthMode)
				} else {
					fmt.Println("  GitHub:   configured but token missing (run 'devrecall auth github')")
				}
			} else {
				fmt.Println("  GitHub:   not connected")
			}

			// GitLab
			if cfg.GitLab.Enabled && cfg.GitLab.Username != "" {
				var glToken auth.GitLabToken
				if err := store.Load("gitlab", cfg.GitLab.Username, &glToken); err == nil {
					fmt.Printf("  GitLab:   connected (%s @ %s)\n", cfg.GitLab.Username, cfg.GitLab.BaseURL)
				} else {
					fmt.Println("  GitLab:   configured but token missing (run 'devrecall auth gitlab')")
				}
			} else {
				fmt.Println("  GitLab:   not connected")
			}

			// Bitbucket
			if cfg.Bitbucket.Enabled && cfg.Bitbucket.Username != "" {
				var bbToken auth.BitbucketToken
				if err := store.Load("bitbucket", cfg.Bitbucket.Username, &bbToken); err == nil {
					fmt.Printf("  Bitbucket: connected (%s, workspace: %s)\n", cfg.Bitbucket.Username, cfg.Bitbucket.Workspace)
				} else {
					fmt.Println("  Bitbucket: configured but token missing (run 'devrecall auth bitbucket')")
				}
			} else {
				fmt.Println("  Bitbucket: not connected")
			}

			// Jira
			if cfg.Jira.Enabled && cfg.Jira.Email != "" {
				var atlToken auth.AtlassianToken
				if err := store.Load("jira", cfg.Jira.Email, &atlToken); err == nil {
					fmt.Printf("  Jira:     connected (%s @ %s)\n", cfg.Jira.Email, cfg.Jira.BaseURL)
				} else {
					fmt.Println("  Jira:     configured but token missing (run 'devrecall auth jira')")
				}
			} else {
				fmt.Println("  Jira:     not connected")
			}

			// Confluence (reuses Jira/Atlassian token)
			if cfg.Confluence.Enabled {
				if cfg.Jira.Email != "" {
					var atlToken auth.AtlassianToken
					if err := store.Load("jira", cfg.Jira.Email, &atlToken); err == nil {
						fmt.Printf("  Confluence: connected (shared with Jira: %s)\n", cfg.Jira.Email)
					} else {
						fmt.Println("  Confluence: enabled but Jira token missing (run 'devrecall auth jira')")
					}
				} else {
					fmt.Println("  Confluence: enabled but Jira not connected (run 'devrecall auth jira')")
				}
			} else {
				fmt.Println("  Confluence: not connected")
			}

			// Linear
			if cfg.Linear.Enabled && cfg.Linear.Email != "" {
				var lt auth.LinearToken
				if err := store.Load("linear", cfg.Linear.Email, &lt); err == nil {
					label := lt.Email
					if label == "" {
						label = lt.UserName
					}
					if label == "" {
						label = cfg.Linear.Email
					}
					fmt.Printf("  Linear:   connected (%s)\n", label)
				} else {
					fmt.Println("  Linear:   configured but token missing (run 'devrecall auth linear')")
				}
			} else {
				fmt.Println("  Linear:   not connected")
			}

			return nil
		},
	}
}

func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage cross-source identities",
	}

	cmd.AddCommand(newIdentityListCmd())
	cmd.AddCommand(newIdentityMergeCmd())
	cmd.AddCommand(newIdentityDeleteCmd())

	return cmd
}

func newIdentityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all identities and their linked sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return err
			}
			defer db.Close()

			resolver := identity.NewResolver(db)
			identities, err := resolver.ListAll()
			if err != nil {
				return err
			}

			if len(identities) == 0 {
				fmt.Println("No identities found. Run 'devrecall setup' or 'devrecall standup' first.")
				return nil
			}

			for _, id := range identities {
				selfTag := ""
				if id.Identity.IsSelf {
					selfTag = " (self)"
				}
				fmt.Printf("#%d  %s <%s>%s\n", id.Identity.ID, id.Identity.Name, id.Identity.Email, selfTag)
				if len(id.Links) > 0 {
					for _, link := range id.Links {
						fmt.Printf("     %s: %s\n", link.Source, link.SourceUID)
					}
				}
			}

			return nil
		},
	}
}

func newIdentityMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <primary-id> <secondary-id> [secondary-id...]",
		Short: "Merge identities into a primary one",
		Long:  "Reassigns all links and activities from secondary identities to the primary, then deletes the secondaries.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			primaryID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid primary ID %q: %w", args[0], err)
			}

			var secondaryIDs []int64
			for _, arg := range args[1:] {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid ID %q: %w", arg, err)
				}
				secondaryIDs = append(secondaryIDs, id)
			}

			db, err := storage.Open()
			if err != nil {
				return err
			}
			defer db.Close()

			resolver := identity.NewResolver(db)
			if err := resolver.MergeIdentities(primaryID, secondaryIDs); err != nil {
				return err
			}

			fmt.Printf("Merged %d identity/identities into #%d.\n", len(secondaryIDs), primaryID)
			return nil
		},
	}
}

func newIdentityDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an identity and unlink its activities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid ID %q: %w", args[0], err)
			}

			db, err := storage.Open()
			if err != nil {
				return err
			}
			defer db.Close()

			resolver := identity.NewResolver(db)
			if err := resolver.DeleteIdentity(id); err != nil {
				return err
			}

			fmt.Printf("Identity #%d deleted.\n", id)
			return nil
		},
	}
}

func newServeCmd() *cobra.Command {
	var portFlag int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the local HTTP API server",
		Long:  "Starts a localhost-only HTTP API for desktop app and integrations. Port defaults to 3725 but can be overridden via --port flag or server.port in config.json.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(portFlag)
		},
	}
	cmd.Flags().IntVar(&portFlag, "port", 0, "Port to listen on (default: 3725)")
	return cmd
}

func runServe(port int) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// --port flag takes priority; if unset, fall back to config, then default.
	if port == 0 && cfg.Server.Port != 0 {
		port = cfg.Server.Port
	}

	db, err := storage.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	tokenStore, err := auth.NewTokenStore(cfg.TokenStorage, dir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	srv := api.NewServer(port, db, cfg, tokenStore)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "DevRecall API listening on http://127.0.0.1:%d\n", srv.Port())
	return srv.Start(ctx)
}

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage background sync daemon (launchd/systemd)",
		Long:  "Install, uninstall, or check status of the background sync daemon that periodically runs 'devrecall sync'.",
	}

	var intervalFlag int

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start the background sync daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := daemon.Config{IntervalSec: intervalFlag}
			if err := daemon.Install(cfg); err != nil {
				return err
			}
			minutes := intervalFlag / 60
			if minutes == 0 {
				minutes = daemon.DefaultIntervalSec / 60
			}
			fmt.Printf("Daemon installed. Syncing every %d minutes.\n", minutes)
			fmt.Println("Logs: ~/.devrecall/daemon.log")
			return nil
		},
	}
	installCmd.Flags().IntVar(&intervalFlag, "interval", 0, "Sync interval in seconds (default: 900 = 15min)")

	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the background sync daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Uninstall(); err != nil {
				return err
			}
			fmt.Println("Daemon uninstalled.")
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := daemon.GetStatus()
			if err != nil {
				return err
			}
			fmt.Printf("Platform:  %s\n", s.Platform)
			fmt.Printf("Installed: %v\n", s.Installed)
			fmt.Printf("Running:   %v\n", s.Running)
			if s.PID > 0 {
				fmt.Printf("PID:       %d\n", s.PID)
			}
			fmt.Printf("Path:      %s\n", s.Path)
			return nil
		},
	}

	cmd.AddCommand(installCmd, uninstallCmd, statusCmd)
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for and install the latest devrecall release",
		Long:  "Checks GitHub Releases for a newer version, verifies its SHA-256 checksum, and atomically replaces the running binary.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Checking for updates...\n")
			rel, err := update.Check()
			if err != nil {
				return fmt.Errorf("checking for updates: %w", err)
			}
			if !update.IsNewer(version, rel.Version) {
				fmt.Printf("Already up to date (%s).\n", version)
				return nil
			}
			fmt.Printf("%s available (current: %s)\n", rel.Version, version)
			if rel.Changelog != "" {
				fmt.Printf("Changelog:\n%s\n\n", rel.Changelog)
			}

			if !yes {
				fmt.Print("Download and install? [y/N]: ")
				var resp string
				fmt.Scanln(&resp)
				resp = strings.ToLower(strings.TrimSpace(resp))
				if resp != "y" && resp != "yes" {
					fmt.Println("Update cancelled.")
					return nil
				}
			}

			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating current binary: %w", err)
			}
			// Resolve symlinks so we replace the real file, not a symlink target.
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				exe = resolved
			}

			fmt.Println("Downloading and verifying...")
			if err := update.Apply(rel, exe); err != nil {
				return fmt.Errorf("applying update: %w", err)
			}
			fmt.Printf("Updated to %s.\n", rel.Version)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	return cmd
}
