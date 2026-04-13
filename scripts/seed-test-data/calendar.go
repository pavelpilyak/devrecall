package main

import (
	"fmt"
	"time"
)

func seedCalendar(opts *Options) error {
	token := env("GOOGLE_CALENDAR_ACCESS_TOKEN")
	calendarID := envOr("GOOGLE_CALENDAR_ID", "primary")

	api := newAPI("https://www.googleapis.com/calendar/v3", map[string]string{
		"Authorization": "Bearer " + token,
	})

	if opts.Clean {
		fmt.Println("Calendar cleanup: delete test events manually or re-run seed to create fresh ones.")
		fmt.Println("  Events are prefixed with [TEST] for easy identification.")
		return nil
	}

	// Create a variety of meeting types to test classification
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	events := []struct {
		summary     string
		description string
		start       time.Time
		duration    time.Duration
		attendees   []string
		meetURL     string
	}{
		{
			summary:     "[TEST] Daily Standup",
			description: "Daily sync — what did you do, what will you do, blockers?",
			start:       today.Add(9 * time.Hour),
			duration:    15 * time.Minute,
			attendees:   []string{"alice@example.com", "bob@example.com", "charlie@example.com"},
			meetURL:     "https://meet.google.com/abc-defg-hij",
		},
		{
			summary:     "[TEST] 1:1 with Alice",
			description: "Weekly 1:1 — career growth, blockers, feedback.",
			start:       today.Add(10 * time.Hour),
			duration:    30 * time.Minute,
			attendees:   []string{"alice@example.com"},
			meetURL:     "https://meet.google.com/klm-nopq-rst",
		},
		{
			summary:     "[TEST] Sprint Planning",
			description: "Plan Sprint 2: review backlog, estimate stories, commit to sprint goal.",
			start:       today.Add(11 * time.Hour),
			duration:    1 * time.Hour,
			attendees:   []string{"alice@example.com", "bob@example.com", "charlie@example.com", "diana@example.com", "eve@example.com"},
			meetURL:     "https://zoom.us/j/1234567890",
		},
		{
			summary:     "[TEST] Architecture Review: API Versioning",
			description: "Review RFC for API versioning strategy. Decision needed.",
			start:       today.Add(14 * time.Hour),
			duration:    45 * time.Minute,
			attendees:   []string{"alice@example.com", "bob@example.com", "frank@example.com"},
			meetURL:     "",
		},
		{
			summary:     "[TEST] Focus Time",
			description: "Blocked time for deep work. No meetings.",
			start:       today.Add(15 * time.Hour),
			duration:    2 * time.Hour,
			attendees:   []string{}, // solo event
			meetURL:     "",
		},
		{
			// Yesterday event — test date range boundaries
			summary:     "[TEST] Code Review Session",
			description: "Review PRs from this week: auth refactor, metrics, handler cleanup.",
			start:       today.Add(-24*time.Hour + 13*time.Hour),
			duration:    30 * time.Minute,
			attendees:   []string{"bob@example.com"},
			meetURL:     "https://meet.google.com/uvw-xyza-bcd",
		},
		{
			// All-day event — test all-day parsing
			summary:     "[TEST] Team Offsite",
			description: "Q2 planning offsite. Full day.",
			start:       today.Add(48 * time.Hour), // 2 days from now
			duration:    0,                          // signals all-day
			attendees:   []string{"alice@example.com", "bob@example.com", "charlie@example.com", "diana@example.com"},
			meetURL:     "",
		},
	}

	for _, ev := range events {
		fmt.Printf("  Creating event: %s\n", ev.summary)
		if opts.DryRun {
			continue
		}

		event := map[string]any{
			"summary":     ev.summary,
			"description": ev.description,
		}

		// Attendees
		if len(ev.attendees) > 0 {
			attendees := make([]map[string]string, len(ev.attendees))
			for i, email := range ev.attendees {
				attendees[i] = map[string]string{"email": email}
			}
			event["attendees"] = attendees
		}

		// Time — all-day vs timed
		if ev.duration == 0 {
			dateStr := ev.start.Format("2006-01-02")
			event["start"] = map[string]string{"date": dateStr}
			event["end"] = map[string]string{"date": ev.start.Add(24 * time.Hour).Format("2006-01-02")}
		} else {
			event["start"] = map[string]string{"dateTime": ev.start.Format(time.RFC3339)}
			event["end"] = map[string]string{"dateTime": ev.start.Add(ev.duration).Format(time.RFC3339)}
		}

		// Conference data
		if ev.meetURL != "" {
			event["conferenceData"] = map[string]any{
				"entryPoints": []map[string]string{
					{"entryPointType": "video", "uri": ev.meetURL},
				},
			}
		}

		err := api.post("/calendars/"+calendarID+"/events?conferenceDataVersion=1", event, nil)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}
		time.Sleep(300 * time.Millisecond)
	}

	fmt.Println("\nGoogle Calendar test data created.")
	fmt.Println("Events are prefixed with [TEST] for easy identification and cleanup.")
	return nil
}
