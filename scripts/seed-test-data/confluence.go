package main

import (
	"encoding/base64"
	"fmt"
	"time"
)

func seedConfluence(opts *Options) error {
	baseURL := env("JIRA_BASE_URL") // shared Atlassian instance
	email := env("JIRA_EMAIL")
	apiToken := env("JIRA_API_TOKEN")
	spaceKey := envOr("CONFLUENCE_SPACE_KEY", "DRT")

	api := newAPI(baseURL+"/wiki/rest/api", map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+apiToken)),
	})

	if opts.Clean {
		fmt.Println("Confluence cleanup: delete the space manually from Confluence settings.")
		fmt.Printf("  Space: %s at %s/wiki\n", spaceKey, baseURL)
		return nil
	}

	// 1. Create space
	fmt.Printf("Creating space %s...\n", spaceKey)
	if !opts.DryRun {
		err := api.post("/space", map[string]any{
			"key":  spaceKey,
			"name": "DevRecall Test",
			"description": map[string]any{
				"plain": map[string]string{
					"value":          "Test space for DevRecall E2E testing",
					"representation": "plain",
				},
			},
		}, nil)
		if err != nil {
			fmt.Printf("  (space may already exist: %v)\n", err)
		}
	}

	// 2. Create pages (RFCs, design docs, meeting notes)
	pages := []struct {
		title   string
		content string
	}{
		{
			title: "RFC: API Versioning Strategy",
			content: `<h2>Background</h2>
<p>Our API is growing and we need a consistent versioning strategy before we ship v2.</p>
<h2>Proposal</h2>
<p>Use URL-based versioning (<code>/v1/</code>, <code>/v2/</code>) for simplicity. Header-based versioning
adds complexity without clear benefits for our use case.</p>
<h2>Decision</h2>
<p>Approved in architecture review on 2026-04-07. Implement URL-based versioning.</p>`,
		},
		{
			title: "RFC: Database Migration Strategy",
			content: `<h2>Problem</h2>
<p>We currently run migrations manually. This is error-prone and doesn't scale.</p>
<h2>Proposal</h2>
<p>Adopt golang-migrate with sequential numbered migrations. Run automatically on startup
with a lock to prevent concurrent runs.</p>
<h2>Status</h2>
<p>In review. Feedback welcome by EOW.</p>`,
		},
		{
			title: "Sprint 1 Retrospective",
			content: `<h2>What went well</h2>
<ul>
<li>Auth endpoint shipped on time</li>
<li>Good test coverage on critical paths</li>
</ul>
<h2>What could improve</h2>
<ul>
<li>PR review turnaround was slow (avg 2 days)</li>
<li>Need better error handling standards</li>
</ul>
<h2>Action items</h2>
<ul>
<li>Set up PR review SLA of 24 hours</li>
<li>Write error handling ADR</li>
</ul>`,
		},
		{
			title: "Architecture Decision: Error Handling",
			content: `<h2>Context</h2>
<p>Inconsistent error handling across services. Some return raw errors, others wrap them.</p>
<h2>Decision</h2>
<p>All errors will use a structured error type with: code, message, and optional details.
Public APIs return RFC 7807 Problem Details format.</p>
<h2>Consequences</h2>
<p>Need to refactor existing handlers. Estimated 2-3 days of work.</p>`,
		},
		{
			title: "Onboarding: Dev Environment Setup",
			content: `<h2>Prerequisites</h2>
<ul>
<li>Go 1.23+</li>
<li>Docker</li>
<li>PostgreSQL 16</li>
</ul>
<h2>Steps</h2>
<ol>
<li>Clone the repo</li>
<li>Run <code>make setup</code></li>
<li>Run <code>make dev</code></li>
</ol>
<p>Ask in #dev-help on Slack if you get stuck.</p>`,
		},
	}

	for _, page := range pages {
		fmt.Printf("  Creating page: %s\n", page.title)
		if opts.DryRun {
			continue
		}

		var created struct {
			ID string `json:"id"`
		}
		err := api.post("/content", map[string]any{
			"type":  "page",
			"title": page.title,
			"space": map[string]string{"key": spaceKey},
			"body": map[string]any{
				"storage": map[string]string{
					"value":          page.content,
					"representation": "storage",
				},
			},
		}, &created)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}

		// Edit the page once to create a version history
		if created.ID != "" {
			time.Sleep(300 * time.Millisecond)
			api.put("/content/"+created.ID, map[string]any{
				"type":    "page",
				"title":   page.title,
				"version": map[string]int{"number": 2},
				"body": map[string]any{
					"storage": map[string]string{
						"value":          page.content + "\n<p><em>Updated with review feedback.</em></p>",
						"representation": "storage",
					},
				},
			}, nil)
		}
	}

	fmt.Printf("\nConfluence test data ready at: %s/wiki/spaces/%s\n", baseURL, spaceKey)
	return nil
}
