package main

import (
	"encoding/base64"
	"fmt"
	"time"
)

func seedGitHub(opts *Options) error {
	token := env("GITHUB_TOKEN")
	username := env("GITHUB_USERNAME")
	repo := envOr("GITHUB_TEST_REPO", "devrecall-test-data")

	api := newAPI("https://api.github.com", map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	})

	fullRepo := username + "/" + repo

	if opts.Clean {
		fmt.Printf("Deleting repo %s...\n", fullRepo)
		if opts.DryRun {
			return nil
		}
		return api.delete("/repos/" + fullRepo)
	}

	// 1. Create repo (or skip if exists)
	fmt.Printf("Creating repo %s...\n", fullRepo)
	if !opts.DryRun {
		err := api.post("/user/repos", map[string]any{
			"name":        repo,
			"description": "DevRecall E2E test data — auto-generated, safe to delete",
			"private":     false,
			"auto_init":   true,
		}, nil)
		if err != nil {
			fmt.Printf("  (repo may already exist: %v)\n", err)
		}
		time.Sleep(2 * time.Second) // wait for repo init
	}

	// 2. Create a few files via commits (so git collector also picks them up)
	files := []struct {
		path    string
		content string
		message string
	}{
		{"src/auth.go", "package auth\n\n// Authenticate validates user credentials.\nfunc Authenticate(user, pass string) bool {\n\treturn user != \"\" && pass != \"\"\n}\n", "feat: add auth module"},
		{"src/handler.go", "package handler\n\nimport \"net/http\"\n\nfunc Health(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusOK)\n}\n", "feat: add health endpoint"},
		{"src/middleware.go", "package middleware\n\nimport \"net/http\"\n\nfunc Logger(next http.Handler) http.Handler {\n\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {\n\t\tnext.ServeHTTP(w, r)\n\t})\n}\n", "feat: add logging middleware"},
		{"docs/api.md", "# API Docs\n\n## Endpoints\n\n- `GET /health` — health check\n- `POST /auth` — authenticate\n", "docs: add API documentation"},
		{"src/auth.go", "package auth\n\nimport \"errors\"\n\nvar ErrInvalidCredentials = errors.New(\"invalid credentials\")\n\n// Authenticate validates user credentials.\nfunc Authenticate(user, pass string) error {\n\tif user == \"\" || pass == \"\" {\n\t\treturn ErrInvalidCredentials\n\t}\n\treturn nil\n}\n", "refactor: auth returns error instead of bool"},
	}

	for _, f := range files {
		fmt.Printf("  Committing %s: %s\n", f.path, f.message)
		if opts.DryRun {
			continue
		}

		// Get current SHA if file exists (needed for updates)
		var existing struct {
			SHA string `json:"sha"`
		}
		_ = api.get(fmt.Sprintf("/repos/%s/contents/%s", fullRepo, f.path), &existing)

		body := map[string]any{
			"message": f.message,
			"content": base64.StdEncoding.EncodeToString([]byte(f.content)),
		}
		if existing.SHA != "" {
			body["sha"] = existing.SHA
		}

		err := api.put(fmt.Sprintf("/repos/%s/contents/%s", fullRepo, f.path), body, nil)
		if err != nil {
			return fmt.Errorf("create file %s: %w", f.path, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 3. Create issues
	issues := []struct {
		title  string
		body   string
		labels []string
	}{
		{"Bug: auth returns 500 on empty username", "When username is empty the auth handler panics instead of returning 401.", []string{"bug"}},
		{"Add rate limiting to API endpoints", "We should add rate limiting middleware to prevent abuse.", []string{"enhancement"}},
		{"Investigate slow query on /search endpoint", "P95 latency is 2s on search. Need to add an index.", []string{"performance", "bug"}},
	}

	for _, iss := range issues {
		fmt.Printf("  Creating issue: %s\n", iss.title)
		if opts.DryRun {
			continue
		}
		err := api.post(fmt.Sprintf("/repos/%s/issues", fullRepo), map[string]any{
			"title":  iss.title,
			"body":   iss.body,
			"labels": iss.labels,
		}, nil)
		if err != nil {
			return fmt.Errorf("create issue: %w", err)
		}
	}

	// 4. Create branches + PRs
	// Get default branch SHA
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if !opts.DryRun {
		if err := api.get(fmt.Sprintf("/repos/%s/git/ref/heads/main", fullRepo), &ref); err != nil {
			return fmt.Errorf("get main ref: %w", err)
		}
	}

	prs := []struct {
		branch  string
		file    string
		content string
		commit  string
		title   string
		body    string
	}{
		{
			branch:  "feat/add-metrics",
			file:    "src/metrics.go",
			content: "package metrics\n\nimport \"sync/atomic\"\n\nvar requestCount int64\n\nfunc Increment() { atomic.AddInt64(&requestCount, 1) }\nfunc Count() int64 { return atomic.LoadInt64(&requestCount) }\n",
			commit:  "feat: add request metrics counter",
			title:   "Add request metrics",
			body:    "Adds a simple atomic counter for request metrics.\n\n## Changes\n- New `metrics` package with `Increment()` and `Count()`\n- Thread-safe via `sync/atomic`",
		},
		{
			branch:  "fix/auth-nil-check",
			file:    "src/auth_fix.go",
			content: "package auth\n\n// ValidateToken checks the token is not empty and has correct format.\nfunc ValidateToken(token string) bool {\n\treturn len(token) > 10\n}\n",
			commit:  "fix: add nil check for auth token validation",
			title:   "Fix nil pointer in auth token validation",
			body:    "Fixes #1.\n\nAdds a nil/empty check before parsing the auth token, preventing the 500 error.",
		},
		{
			branch:  "refactor/handler-cleanup",
			file:    "src/handler_v2.go",
			content: "package handler\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n)\n\ntype Response struct {\n\tStatus  string `json:\"status\"`\n\tMessage string `json:\"message,omitempty\"`\n}\n\nfunc JSON(w http.ResponseWriter, status int, resp Response) {\n\tw.Header().Set(\"Content-Type\", \"application/json\")\n\tw.WriteHeader(status)\n\tjson.NewEncoder(w).Encode(resp)\n}\n",
			commit:  "refactor: extract JSON response helper",
			title:   "Refactor: extract JSON response helper",
			body:    "Extracts common JSON response pattern into a reusable `JSON()` helper.\n\nReduces duplication across 5 handlers.",
		},
	}

	for _, pr := range prs {
		fmt.Printf("  Creating PR: %s\n", pr.title)
		if opts.DryRun {
			continue
		}

		// Create branch
		api.post(fmt.Sprintf("/repos/%s/git/refs", fullRepo), map[string]any{
			"ref": "refs/heads/" + pr.branch,
			"sha": ref.Object.SHA,
		}, nil)

		// Commit file to branch
		api.put(fmt.Sprintf("/repos/%s/contents/%s", fullRepo, pr.file), map[string]any{
			"message": pr.commit,
			"content": base64.StdEncoding.EncodeToString([]byte(pr.content)),
			"branch":  pr.branch,
		}, nil)

		// Create PR
		var created struct {
			Number int `json:"number"`
		}
		err := api.post(fmt.Sprintf("/repos/%s/pulls", fullRepo), map[string]any{
			"title": pr.title,
			"body":  pr.body,
			"head":  pr.branch,
			"base":  "main",
		}, &created)
		if err != nil {
			fmt.Printf("    PR creation may have failed: %v\n", err)
			continue
		}

		// Add a review comment on the first PR
		if created.Number == 1 {
			api.post(fmt.Sprintf("/repos/%s/pulls/%d/reviews", fullRepo, created.Number), map[string]any{
				"body":  "Looks good! One nit: consider adding a `Reset()` function for testing.",
				"event": "COMMENT",
			}, nil)
		}

		time.Sleep(500 * time.Millisecond)
	}

	// 5. Merge one PR to test merged state detection
	if !opts.DryRun {
		fmt.Println("  Merging PR #1 (feat/add-metrics)...")
		api.put(fmt.Sprintf("/repos/%s/pulls/1/merge", fullRepo), map[string]any{
			"merge_method": "squash",
		}, nil)
	}

	fmt.Printf("\nGitHub test data ready at: https://github.com/%s\n", fullRepo)
	return nil
}
