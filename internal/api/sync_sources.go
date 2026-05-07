package api

import (
	"context"
	"fmt"
	"time"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/chat/freshness"
	bbcollector "github.com/pavelpilyak/devrecall/internal/collector/bitbucket"
	calcollector "github.com/pavelpilyak/devrecall/internal/collector/calendar"
	confluencecollector "github.com/pavelpilyak/devrecall/internal/collector/confluence"
	ghcollector "github.com/pavelpilyak/devrecall/internal/collector/github"
	glcollector "github.com/pavelpilyak/devrecall/internal/collector/gitlab"
	jiracollector "github.com/pavelpilyak/devrecall/internal/collector/jira"
	linearcollector "github.com/pavelpilyak/devrecall/internal/collector/linear"
	slackcollector "github.com/pavelpilyak/devrecall/internal/collector/slack"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/identity"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// syncWindow matches the lookback the CLI `devrecall sync` uses for
// remote collectors so the API and CLI ingest the same window.
const syncWindow = 7 * 24 * time.Hour

// BuildAllSyncers returns a freshness.Syncer for every source the user
// has enabled and authenticated. Sources missing tokens or required
// config are silently omitted — callers see this through the absence of
// a "syncing" event for that source. This is the wide-blast-radius
// counterpart to BuildFreshnessSyncers used by /api/sync/stream when
// the user has explicitly asked for a full sync.
func BuildAllSyncers(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) map[string]freshness.Syncer {
	syncers := make(map[string]freshness.Syncer)
	if cfg.Git.Enabled {
		syncers["git"] = gitSyncer(cfg, db)
	}
	if s := slackSyncer(cfg, db, tokenStore); s != nil {
		syncers["slack"] = s
	}
	if s := calendarSyncer(cfg, db, tokenStore); s != nil {
		syncers["calendar"] = s
	}
	if s := githubSyncer(cfg, db, tokenStore); s != nil {
		syncers["github"] = s
	}
	if s := gitlabSyncer(cfg, db, tokenStore); s != nil {
		syncers["gitlab"] = s
	}
	if s := bitbucketSyncer(cfg, db, tokenStore); s != nil {
		syncers["bitbucket"] = s
	}
	if s := jiraSyncer(cfg, db, tokenStore); s != nil {
		syncers["jira"] = s
	}
	if s := linearSyncer(cfg, db, tokenStore); s != nil {
		syncers["linear"] = s
	}
	if s := confluenceSyncer(cfg, db, tokenStore); s != nil {
		syncers["confluence"] = s
	}
	return syncers
}

func slackSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Slack.Enabled || cfg.Slack.TeamID == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.SlackToken
		if err := tokenStore.Load("slack", cfg.Slack.TeamID, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth slack')")
		}
		sc := slackcollector.New(token.AccessToken)
		activities, err := sc.CollectSince(ctx, time.Now().Add(-syncWindow))
		if err != nil {
			_ = db.SetSyncError("slack", err.Error())
			return 0, err
		}
		var inserted int
		if len(activities) > 0 {
			inserted, err = db.InsertActivities(activities)
			if err != nil {
				return 0, err
			}
		}
		_ = db.SetSyncState("slack", "")
		// Identity link is best-effort.
		if profile, profErr := sc.GetUserProfile(ctx, token.UserID); profErr == nil && profile.Email != "" {
			_, _ = identity.NewResolver(db).AutoLinkSlack(token.UserID, profile.Email, profile.Name)
		}
		return inserted, nil
	}
}

func calendarSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Calendar.Enabled || cfg.Calendar.Email == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		token, err := auth.LoadGoogleToken(ctx, tokenStore, cfg.Calendar.Email)
		if err != nil {
			return 0, err
		}
		cc := calcollector.New(token.AccessToken)

		var (
			activities []models.Activity
			newToken   string
		)
		st, _ := db.GetSyncState("calendar")
		if st != nil && st.Cursor != "" {
			activities, newToken, err = cc.CollectWithSyncToken(ctx, st.Cursor)
			if err != nil {
				// Cursor expired — fall back to a full window sync.
				activities, newToken, err = cc.InitialSync(ctx, syncWindow)
			}
		} else {
			activities, newToken, err = cc.InitialSync(ctx, syncWindow)
		}
		if err != nil {
			_ = db.SetSyncError("calendar", err.Error())
			return 0, err
		}
		_ = db.SetSyncState("calendar", newToken)

		if len(activities) == 0 {
			return 0, nil
		}
		_, _ = identity.NewResolver(db).EnrichFromCalendar(activities)
		return db.InsertActivities(activities)
	}
}

func githubSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.GitHub.Enabled || cfg.GitHub.Username == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.GitHubToken
		if err := tokenStore.Load("github", cfg.GitHub.Username, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth github')")
		}
		gc := ghcollector.New(token.AccessToken, cfg.GitHub.Username)
		return runRemoteSync(ctx, db, "github", func(ctx context.Context) ([]models.Activity, error) {
			return gc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

func gitlabSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.GitLab.Enabled || cfg.GitLab.Username == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.GitLabToken
		if err := tokenStore.Load("gitlab", cfg.GitLab.Username, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth gitlab')")
		}
		gc := glcollector.New(token.AccessToken, cfg.GitLab.Username, cfg.GitLab.BaseURL)
		return runRemoteSync(ctx, db, "gitlab", func(ctx context.Context) ([]models.Activity, error) {
			return gc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

func bitbucketSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Bitbucket.Enabled || cfg.Bitbucket.Username == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.BitbucketToken
		if err := tokenStore.Load("bitbucket", cfg.Bitbucket.Username, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth bitbucket')")
		}
		bc := bbcollector.New(token.Username, token.AppPassword, token.UUID, cfg.Bitbucket.Workspace)
		return runRemoteSync(ctx, db, "bitbucket", func(ctx context.Context) ([]models.Activity, error) {
			return bc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

func jiraSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Jira.Enabled || cfg.Jira.Email == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.AtlassianToken
		if err := tokenStore.Load("jira", cfg.Jira.Email, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth jira')")
		}
		var jc *jiracollector.Collector
		switch {
		case cfg.Jira.AuthMode == "api-token" && cfg.Jira.BaseURL != "":
			jc = jiracollector.NewWithAPIToken(cfg.Jira.Email, token.AccessToken, cfg.Jira.BaseURL)
		case len(token.CloudSites) > 0:
			jc = jiracollector.New(token.AccessToken, token.CloudSites[0].ID)
		default:
			return 0, fmt.Errorf("no base URL or cloud site in token")
		}
		return runRemoteSync(ctx, db, "jira", func(ctx context.Context) ([]models.Activity, error) {
			return jc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

func linearSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Linear.Enabled || cfg.Linear.Email == "" {
		return nil
	}
	return func(ctx context.Context) (int, error) {
		var token auth.LinearToken
		if err := tokenStore.Load("linear", cfg.Linear.Email, &token); err != nil {
			return 0, fmt.Errorf("token not found (run 'devrecall auth linear')")
		}
		lc := linearcollector.New(token.AccessToken)
		return runRemoteSync(ctx, db, "linear", func(ctx context.Context) ([]models.Activity, error) {
			return lc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

func confluenceSyncer(cfg *config.Config, db *storage.DB, tokenStore auth.TokenStore) freshness.Syncer {
	if !cfg.Confluence.Enabled {
		return nil
	}
	// Confluence reuses the Atlassian token stored under "jira".
	tokenKey := cfg.Jira.Email
	if tokenKey == "" {
		tokenKey = "default"
	}
	return func(ctx context.Context) (int, error) {
		var token auth.AtlassianToken
		if err := tokenStore.Load("jira", tokenKey, &token); err != nil {
			return 0, fmt.Errorf("Jira token not found (run 'devrecall auth jira')")
		}
		var cc *confluencecollector.Collector
		switch {
		case cfg.Jira.AuthMode == "api-token" && cfg.Jira.Email != "":
			cc = confluencecollector.NewWithAPIToken(cfg.Jira.Email, token.AccessToken, cfg.Jira.BaseURL+"/wiki")
		case len(token.CloudSites) > 0:
			cc = confluencecollector.New(token.AccessToken, token.CloudSites[0].ID)
		default:
			return 0, fmt.Errorf("no cloud site in Jira token")
		}
		return runRemoteSync(ctx, db, "confluence", func(ctx context.Context) ([]models.Activity, error) {
			return cc.CollectSince(ctx, time.Now().Add(-syncWindow))
		})
	}
}

// runRemoteSync is the shared collect-then-insert path used by remote
// collectors that don't carry a sync cursor (everything except calendar).
// It records sync_state on success and sync_error on failure so the
// status endpoint reflects what just happened.
func runRemoteSync(ctx context.Context, db *storage.DB, source string, collect func(context.Context) ([]models.Activity, error)) (int, error) {
	activities, err := collect(ctx)
	if err != nil {
		_ = db.SetSyncError(source, err.Error())
		return 0, err
	}
	var inserted int
	if len(activities) > 0 {
		inserted, err = db.InsertActivities(activities)
		if err != nil {
			return 0, err
		}
	}
	_ = db.SetSyncState(source, "")
	return inserted, nil
}
