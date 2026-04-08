package api

import (
	"context"
	"time"

	"github.com/pavelpiliak/devrecall/internal/chat/freshness"
	"github.com/pavelpiliak/devrecall/internal/collector/git"
	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/internal/storage"
)

// BuildFreshnessChecker turns the user's chat.sync_freshness config into a
// *freshness.Checker. Empty / unset duration strings fall back to the
// freshness package defaults.
//
// The chat REPL and the SSE chat handler both call this so the freshness
// behaviour stays identical regardless of which surface initiated the
// query.
func BuildFreshnessChecker(cfg *config.Config, db *storage.DB) *freshness.Checker {
	src := cfg.Chat.SyncFreshness

	opts := freshness.Options{
		Enabled: !src.Disabled,
	}
	if d, err := time.ParseDuration(src.DefaultTTL); err == nil && d > 0 {
		opts.DefaultTTL = d
	}
	if d, err := time.ParseDuration(src.Wait); err == nil && d > 0 {
		opts.Wait = d
	}
	if len(src.PerSource) > 0 {
		opts.TTLs = make(map[string]time.Duration, len(src.PerSource))
		for source, raw := range src.PerSource {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				opts.TTLs[source] = d
			}
		}
	}
	return freshness.New(db, opts)
}

// BuildFreshnessSyncers returns the per-source incremental sync callbacks
// the freshness checker fans out. Each closure is responsible for running
// its collector, inserting activities, and updating sync_state so the
// next freshness check sees a current timestamp.
//
// Sources whose collectors require OAuth tokens or extra setup are not
// wired here yet — only Git, which works zero-config from local repo
// paths. Adding Slack/GitHub/etc. is straight refactor work but is left
// out to keep the freshness step's blast radius small. Callers can
// extend the returned map themselves.
func BuildFreshnessSyncers(cfg *config.Config, db *storage.DB) map[string]freshness.Syncer {
	syncers := make(map[string]freshness.Syncer)
	if cfg.Git.Enabled {
		syncers["git"] = gitSyncer(cfg, db)
	}
	return syncers
}

// gitSyncer mirrors Server.syncGit but returns the result as a
// freshness.Syncer so it can be plugged into a Checker. It also writes
// sync_state so subsequent freshness checks know the source is fresh.
func gitSyncer(cfg *config.Config, db *storage.DB) freshness.Syncer {
	return func(ctx context.Context) (int, error) {
		if !cfg.Git.Enabled {
			return 0, nil
		}
		repos := cfg.Git.Repos
		if len(cfg.Git.ScanPaths) > 0 {
			repos = mergeUnique(repos, git.DiscoverRepos(cfg.Git.ScanPaths))
		}
		emails := mergeUnique(cfg.Git.Emails, git.DetectEmails(repos))
		if len(repos) == 0 || len(emails) == 0 {
			return 0, nil
		}
		collector := git.New(repos, emails)
		activities, err := collector.Collect(ctx)
		if err != nil {
			return 0, err
		}
		var inserted int
		if len(activities) > 0 {
			inserted, err = db.InsertActivities(activities)
			if err != nil {
				return 0, err
			}
		}
		_ = db.SetSyncState("git", "")
		return inserted, nil
	}
}
