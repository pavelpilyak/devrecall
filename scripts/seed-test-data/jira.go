package main

import (
	"encoding/base64"
	"fmt"
	"time"
)

func seedJira(opts *Options) error {
	baseURL := env("JIRA_BASE_URL")
	email := env("JIRA_EMAIL")
	apiToken := env("JIRA_API_TOKEN")
	projectKey := envOr("JIRA_PROJECT_KEY", "DRT")

	api := newAPI(baseURL+"/rest/api/3", map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+apiToken)),
	})

	if opts.Clean {
		fmt.Println("Jira cleanup: delete the project manually from Jira settings.")
		fmt.Printf("  Project: %s at %s\n", projectKey, baseURL)
		return nil
	}

	// 1. Get current user's account ID
	var myself struct {
		AccountID string `json:"accountId"`
	}
	if !opts.DryRun {
		if err := api.get("/myself", &myself); err != nil {
			return fmt.Errorf("get myself: %w", err)
		}
		fmt.Printf("  Authenticated as account: %s\n", myself.AccountID)
	}

	// 2. Create project (Scrum)
	fmt.Printf("Creating project %s...\n", projectKey)
	if !opts.DryRun {
		err := api.post("/project", map[string]any{
			"key":              projectKey,
			"name":             "DevRecall Test",
			"projectTypeKey":   "software",
			"projectTemplateKey": "com.pyxis.greenhopper.jira:gh-scrum-template",
			"leadAccountId":    myself.AccountID,
		}, nil)
		if err != nil {
			fmt.Printf("  (project may already exist: %v)\n", err)
		}
	}

	// 3. Get board ID for sprint creation
	boardAPI := newAPI(baseURL+"/rest/agile/1.0", map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+apiToken)),
	})

	var boards struct {
		Values []struct {
			ID int `json:"id"`
		} `json:"values"`
	}
	var boardID int
	if !opts.DryRun {
		boardAPI.get(fmt.Sprintf("/board?projectKeyOrId=%s", projectKey), &boards)
		if len(boards.Values) > 0 {
			boardID = boards.Values[0].ID
		}
	}

	// 4. Create sprint
	if boardID > 0 && !opts.DryRun {
		fmt.Println("  Creating sprint...")
		now := time.Now()
		boardAPI.post("/sprint", map[string]any{
			"name":          "Sprint 1 — Test Data",
			"startDate":     now.AddDate(0, 0, -7).Format(time.RFC3339),
			"endDate":       now.AddDate(0, 0, 7).Format(time.RFC3339),
			"originBoardId": boardID,
			"goal":          "Validate DevRecall data collection",
		}, nil)
	}

	// 5. Create issues with various states and metadata
	issues := []struct {
		summary  string
		desc     string
		issueType string
		priority string
		labels   []string
	}{
		{
			summary:   "Implement user authentication endpoint",
			desc:      "Create POST /api/auth endpoint that validates JWT tokens and returns user profile.",
			issueType: "Story",
			priority:  "High",
			labels:    []string{"backend", "auth"},
		},
		{
			summary:   "Fix: login page 500 error on empty password",
			desc:      "Submitting the login form with an empty password field causes a 500 error instead of a validation message.",
			issueType: "Bug",
			priority:  "Highest",
			labels:    []string{"bug", "frontend"},
		},
		{
			summary:   "Add database connection pooling",
			desc:      "Current implementation creates a new connection per request. Need to add connection pooling with configurable pool size.",
			issueType: "Task",
			priority:  "Medium",
			labels:    []string{"backend", "performance"},
		},
		{
			summary:   "Design review: API versioning strategy",
			desc:      "Decide between URL-based (/v1/, /v2/) and header-based (Accept: application/vnd.api.v2+json) versioning.",
			issueType: "Story",
			priority:  "Low",
			labels:    []string{"architecture", "api"},
		},
		{
			summary:   "Upgrade Go to 1.23",
			desc:      "New version has range-over-func and better error handling. Update go.mod and CI.",
			issueType: "Task",
			priority:  "Medium",
			labels:    []string{"maintenance"},
		},
	}

	var createdKeys []string

	for _, iss := range issues {
		fmt.Printf("  Creating issue: %s\n", iss.summary)
		if opts.DryRun {
			continue
		}

		var created struct {
			Key string `json:"key"`
		}
		err := api.post("/issue", map[string]any{
			"fields": map[string]any{
				"project":   map[string]string{"key": projectKey},
				"summary":   iss.summary,
				"description": map[string]any{
					"type":    "doc",
					"version": 1,
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{"type": "text", "text": iss.desc},
							},
						},
					},
				},
				"issuetype": map[string]string{"name": iss.issueType},
				"priority":  map[string]string{"name": iss.priority},
				"labels":    iss.labels,
				"assignee":  map[string]string{"accountId": myself.AccountID},
			},
		}, &created)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}
		createdKeys = append(createdKeys, created.Key)
		fmt.Printf("    Created: %s\n", created.Key)
	}

	// 6. Transition some issues through workflow states
	// Standard Scrum workflow: To Do (11) → In Progress (21) → Done (31)
	// (IDs vary by project — we'll use the generic transition API)
	if !opts.DryRun && len(createdKeys) >= 3 {
		transitions := []struct {
			key        string
			transition string // transition name
			comment    string
		}{
			{createdKeys[0], "In Progress", "Starting work on auth endpoint. Pairing with Alice."},
			{createdKeys[0], "Done", "Auth endpoint implemented and reviewed. PR merged."},
			{createdKeys[1], "In Progress", "Investigating — looks like a nil pointer in the validation layer."},
			{createdKeys[2], "In Progress", "Researching pgxpool vs database/sql pooling options."},
		}

		for _, t := range transitions {
			fmt.Printf("  Transitioning %s → %s\n", t.key, t.transition)

			// Get available transitions
			var trans struct {
				Transitions []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"transitions"`
			}
			api.get(fmt.Sprintf("/issue/%s/transitions", t.key), &trans)

			for _, tr := range trans.Transitions {
				if tr.Name == t.transition {
					api.post(fmt.Sprintf("/issue/%s/transitions", t.key), map[string]any{
						"transition": map[string]string{"id": tr.ID},
					}, nil)
					break
				}
			}

			// Add comment
			api.post(fmt.Sprintf("/issue/%s/comment", t.key), map[string]any{
				"body": map[string]any{
					"type":    "doc",
					"version": 1,
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{"type": "text", "text": t.comment},
							},
						},
					},
				},
			}, nil)

			time.Sleep(300 * time.Millisecond)
		}
	}

	fmt.Printf("\nJira test data ready at: %s/jira/software/projects/%s/board\n", baseURL, projectKey)
	return nil
}
