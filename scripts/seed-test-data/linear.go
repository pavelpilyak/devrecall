package main

import (
	"fmt"
	"time"
)

func seedLinear(opts *Options) error {
	apiKey := env("LINEAR_API_KEY")
	teamKey := envOr("LINEAR_TEAM_KEY", "DRT")

	api := newAPI("https://api.linear.app", map[string]string{
		"Authorization": apiKey,
	})

	if opts.Clean {
		fmt.Println("Linear cleanup: delete the team manually from Linear settings.")
		return nil
	}

	// 0. Get viewer ID (to assign issues to current user)
	var viewerResp struct {
		Data struct {
			Viewer struct {
				ID string `json:"id"`
			} `json:"viewer"`
		} `json:"data"`
	}
	var viewerID string
	if !opts.DryRun {
		if err := api.post("/graphql", map[string]any{
			"query": `query { viewer { id } }`,
		}, &viewerResp); err != nil {
			return fmt.Errorf("get viewer: %w", err)
		}
		viewerID = viewerResp.Data.Viewer.ID
		fmt.Printf("  Authenticated as viewer: %s\n", viewerID)
	}

	// 1. Get team ID from key
	var teamResp struct {
		Data struct {
			Teams struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Key  string `json:"key"`
				} `json:"nodes"`
			} `json:"teams"`
		} `json:"data"`
	}

	fmt.Printf("Looking up team %s...\n", teamKey)
	if !opts.DryRun {
		err := api.post("/graphql", map[string]any{
			"query": `query { teams { nodes { id name key } } }`,
		}, &teamResp)
		if err != nil {
			return fmt.Errorf("get teams: %w", err)
		}
	}

	var teamID string
	for _, t := range teamResp.Data.Teams.Nodes {
		if t.Key == teamKey {
			teamID = t.ID
			fmt.Printf("  Found team: %s (%s)\n", t.Name, t.ID)
			break
		}
	}
	if teamID == "" && !opts.DryRun {
		return fmt.Errorf("team with key %q not found — create it in Linear first", teamKey)
	}

	// 2. Get workflow states for transitions
	var statesResp struct {
		Data struct {
			WorkflowStates struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"nodes"`
			} `json:"workflowStates"`
		} `json:"data"`
	}

	stateIDs := map[string]string{} // name → ID
	if !opts.DryRun {
		api.post("/graphql", map[string]any{
			"query": fmt.Sprintf(`query {
				workflowStates(filter: { team: { id: { eq: "%s" } } }) {
					nodes { id name type }
				}
			}`, teamID),
		}, &statesResp)

		for _, s := range statesResp.Data.WorkflowStates.Nodes {
			stateIDs[s.Name] = s.ID
			fmt.Printf("  State: %s (%s)\n", s.Name, s.Type)
		}
	}

	// 3. Create labels
	labels := []string{"backend", "frontend", "bug", "performance", "infrastructure"}
	labelIDs := map[string]string{}
	for _, name := range labels {
		if opts.DryRun {
			continue
		}
		var resp struct {
			Data struct {
				IssueLabelCreate struct {
					IssueLabel struct {
						ID string `json:"id"`
					} `json:"issueLabel"`
				} `json:"issueLabelCreate"`
			} `json:"data"`
		}
		api.post("/graphql", map[string]any{
			"query": `mutation($input: IssueLabelCreateInput!) { issueLabelCreate(input: $input) { issueLabel { id } } }`,
			"variables": map[string]any{
				"input": map[string]any{
					"name":   name,
					"teamId": teamID,
				},
			},
		}, &resp)
		if resp.Data.IssueLabelCreate.IssueLabel.ID != "" {
			labelIDs[name] = resp.Data.IssueLabelCreate.IssueLabel.ID
		}
	}

	// 4. Create issues
	issues := []struct {
		title       string
		desc        string
		priority    int // 0=none, 1=urgent, 2=high, 3=medium, 4=low
		labels      []string
		targetState string // state to transition to after creation
		comment     string
	}{
		{
			title:       "Implement OAuth2 PKCE flow for mobile",
			desc:        "Mobile app needs PKCE flow since it can't store client secrets securely. Use RFC 7636 spec.",
			priority:    2,
			labels:      []string{"backend"},
			targetState: "Done",
			comment:     "Implemented and tested on both iOS and Android. PR #42 merged.",
		},
		{
			title:       "Fix: websocket reconnection drops messages",
			desc:        "When the websocket reconnects after a network blip, messages sent during the gap are lost. Need a message queue with replay.",
			priority:    1,
			labels:      []string{"bug", "backend"},
			targetState: "In Progress",
			comment:     "Root cause found — the reconnect handler resets the sequence counter. Working on fix.",
		},
		{
			title:       "Migrate CI from CircleCI to GitHub Actions",
			desc:        "CircleCI free tier is too limited. GitHub Actions gives us 2000 min/month free and better integration.",
			priority:    3,
			labels:      []string{"infrastructure"},
			targetState: "In Progress",
			comment:     "Draft workflow passing on feature branch. Need to add caching and matrix builds.",
		},
		{
			title:       "Add request tracing with OpenTelemetry",
			desc:        "Need distributed tracing to debug latency issues. Add OTEL SDK + Jaeger exporter.",
			priority:    3,
			labels:      []string{"backend", "performance"},
			targetState: "",
			comment:     "",
		},
		{
			title:       "Dashboard: add latency percentile chart",
			desc:        "Add P50/P95/P99 latency chart to the monitoring dashboard using the existing metrics data.",
			priority:    4,
			labels:      []string{"frontend"},
			targetState: "",
			comment:     "",
		},
	}

	for _, iss := range issues {
		fmt.Printf("  Creating issue: %s\n", iss.title)
		if opts.DryRun {
			continue
		}

		// Build label IDs
		var issueLabelIDs []string
		for _, l := range iss.labels {
			if id, ok := labelIDs[l]; ok {
				issueLabelIDs = append(issueLabelIDs, id)
			}
		}

		input := map[string]any{
			"title":       iss.title,
			"description": iss.desc,
			"teamId":      teamID,
			"priority":    iss.priority,
		}
		if viewerID != "" {
			input["assigneeId"] = viewerID
		}
		if len(issueLabelIDs) > 0 {
			input["labelIds"] = issueLabelIDs
		}

		var resp struct {
			Data struct {
				IssueCreate struct {
					Issue struct {
						ID         string `json:"id"`
						Identifier string `json:"identifier"`
					} `json:"issue"`
				} `json:"issueCreate"`
			} `json:"data"`
		}
		err := api.post("/graphql", map[string]any{
			"query":     `mutation($input: IssueCreateInput!) { issueCreate(input: $input) { issue { id identifier } } }`,
			"variables": map[string]any{"input": input},
		}, &resp)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}

		issueID := resp.Data.IssueCreate.Issue.ID
		fmt.Printf("    Created: %s\n", resp.Data.IssueCreate.Issue.Identifier)

		// Transition to target state
		if iss.targetState != "" {
			if stateID, ok := stateIDs[iss.targetState]; ok {
				api.post("/graphql", map[string]any{
					"query": `mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { issue { id } } }`,
					"variables": map[string]any{
						"id":    issueID,
						"input": map[string]any{"stateId": stateID},
					},
				}, nil)
			}
		}

		// Add comment
		if iss.comment != "" {
			api.post("/graphql", map[string]any{
				"query": `mutation($input: CommentCreateInput!) { commentCreate(input: $input) { comment { id } } }`,
				"variables": map[string]any{
					"input": map[string]any{
						"issueId": issueID,
						"body":    iss.comment,
					},
				},
			}, nil)
		}

		time.Sleep(300 * time.Millisecond)
	}

	fmt.Println("\nLinear test data ready in your Linear workspace.")
	return nil
}
