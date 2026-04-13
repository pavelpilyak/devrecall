package main

import (
	"fmt"
	"time"
)

func seedSlack(opts *Options) error {
	botToken := env("SLACK_BOT_TOKEN")
	channelName := envOr("SLACK_CHANNEL_NAME", "devrecall-test")

	api := newAPI("https://slack.com/api", map[string]string{
		"Authorization": "Bearer " + botToken,
	})

	if opts.Clean {
		fmt.Println("Slack cleanup: archive or delete the test channel manually.")
		return nil
	}

	// 1. Create channel (or get existing)
	fmt.Printf("Creating channel #%s...\n", channelName)

	var channelID string
	if !opts.DryRun {
		var createResp struct {
			OK      bool `json:"ok"`
			Channel struct {
				ID string `json:"id"`
			} `json:"channel"`
			Error string `json:"error"`
		}
		api.post("/conversations.create", map[string]any{
			"name":       channelName,
			"is_private": false,
		}, &createResp)

		if createResp.OK {
			channelID = createResp.Channel.ID
		} else if createResp.Error == "name_taken" {
			// Find existing channel
			var listResp struct {
				Channels []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"channels"`
			}
			api.get("/conversations.list?limit=200&types=public_channel", &listResp)
			for _, ch := range listResp.Channels {
				if ch.Name == channelName {
					channelID = ch.ID
					break
				}
			}
		}

		if channelID == "" {
			return fmt.Errorf("could not create or find channel #%s", channelName)
		}
		fmt.Printf("  Channel ID: %s\n", channelID)

		// Join the channel
		api.post("/conversations.join", map[string]any{
			"channel": channelID,
		}, nil)
	}

	// 2. Post standalone messages
	messages := []struct {
		text string
	}{
		{"Pushed the auth refactor to `feat/auth-v2`. Ready for review."},
		{"FYI — the staging deploy is broken. Looking into it now."},
		{"Found the issue: the config migration script missed the new `pool_size` field. Fixing."},
		{"Staging is back up. The fix was a one-liner in `migrate.go`."},
		{"Anyone have opinions on URL-based vs header-based API versioning? Writing an RFC."},
	}

	var threadTS string // we'll start a thread from the first message
	for i, msg := range messages {
		fmt.Printf("  Posting message: %s\n", truncate(msg.text, 60))
		if opts.DryRun {
			continue
		}

		var resp struct {
			OK bool   `json:"ok"`
			TS string `json:"ts"`
		}
		api.post("/chat.postMessage", map[string]any{
			"channel": channelID,
			"text":    msg.text,
		}, &resp)

		if i == 0 {
			threadTS = resp.TS
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 3. Post a thread (simulating a design discussion)
	fmt.Println("  Creating discussion thread...")
	if !opts.DryRun {
		threadStarter := struct {
			text string
		}{"Question: should we use pgxpool or database/sql for connection pooling?"}

		var starterResp struct {
			OK bool   `json:"ok"`
			TS string `json:"ts"`
		}
		api.post("/chat.postMessage", map[string]any{
			"channel": channelID,
			"text":    threadStarter.text,
		}, &starterResp)

		threadParentTS := starterResp.TS
		time.Sleep(300 * time.Millisecond)

		replies := []string{
			"pgxpool is more idiomatic for PostgreSQL. We get better control over pool size and connection lifetime.",
			"database/sql is stdlib and driver-agnostic. If we ever switch DBs, less to change.",
			"True, but we're committed to Postgres. pgxpool also supports LISTEN/NOTIFY which we'll need for real-time.",
			"Agreed. Let's go with pgxpool. I'll update the RFC.",
			"Sounds good. Make sure to add a health check that tests pool connectivity.",
		}

		for _, reply := range replies {
			api.post("/chat.postMessage", map[string]any{
				"channel":   channelID,
				"text":      reply,
				"thread_ts": threadParentTS,
			}, nil)
			time.Sleep(300 * time.Millisecond)
		}
	}

	// 4. Post another thread (incident discussion)
	fmt.Println("  Creating incident thread...")
	if !opts.DryRun && threadTS != "" {
		// Reply to the first message to form a thread
		incidentReplies := []string{
			"Is this related to the deploy pipeline change from yesterday?",
			"No, it's a config migration issue. The new `pool_size` field wasn't in the migration script.",
			"Ah got it. Should we add a CI check for migration completeness?",
			"Good idea. Filed a ticket: DRT-3.",
		}
		for _, reply := range incidentReplies {
			api.post("/chat.postMessage", map[string]any{
				"channel":   channelID,
				"text":      reply,
				"thread_ts": threadTS,
			}, nil)
			time.Sleep(300 * time.Millisecond)
		}
	}

	fmt.Printf("\nSlack test data ready in #%s\n", channelName)
	fmt.Println("Note: DevRecall's Slack collector uses a *user* token (search:read scope), not a bot token.")
	fmt.Println("The bot token is only used here for seeding. For collection, auth via `devrecall auth slack`.")
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
