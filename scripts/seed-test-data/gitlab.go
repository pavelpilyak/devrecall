package main

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"time"
)

func seedGitLab(opts *Options) error {
	token := env("GITLAB_TOKEN")
	username := env("GITLAB_USERNAME")
	project := envOr("GITLAB_TEST_PROJECT", "devrecall-test-data")

	api := newAPI("https://gitlab.com/api/v4", map[string]string{
		"PRIVATE-TOKEN": token,
	})

	projectPath := url.PathEscape(username + "/" + project)

	if opts.Clean {
		fmt.Printf("Deleting project %s/%s...\n", username, project)
		if opts.DryRun {
			return nil
		}
		return api.delete("/projects/" + projectPath)
	}

	// 1. Create project
	fmt.Printf("Creating project %s/%s...\n", username, project)
	var proj struct {
		ID int `json:"id"`
	}
	if !opts.DryRun {
		err := api.post("/projects", map[string]any{
			"name":                 project,
			"description":         "DevRecall E2E test data — auto-generated, safe to delete",
			"visibility":          "public",
			"initialize_with_readme": true,
		}, &proj)
		if err != nil {
			fmt.Printf("  (project may already exist: %v)\n", err)
			// Try to get existing project ID
			api.get("/projects/"+projectPath, &proj)
		}
		time.Sleep(2 * time.Second)
	}

	idPath := fmt.Sprintf("/projects/%s", projectPath)

	// 2. Create files via commits
	files := []struct {
		path    string
		content string
		message string
	}{
		{"src/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n", "feat: initial main.go"},
		{"src/config.go", "package main\n\ntype Config struct {\n\tPort int\n\tHost string\n}\n", "feat: add config struct"},
	}

	for _, f := range files {
		fmt.Printf("  Committing %s: %s\n", f.path, f.message)
		if opts.DryRun {
			continue
		}
		api.post(idPath+"/repository/files/"+url.PathEscape(f.path), map[string]any{
			"branch":         "main",
			"content":        f.content,
			"commit_message": f.message,
		}, nil)
		time.Sleep(500 * time.Millisecond)
	}

	// 3. Create MR branches + merge requests
	mrs := []struct {
		branch  string
		file    string
		content string
		commit  string
		title   string
		desc    string
	}{
		{
			branch: "feat/logging", file: "src/logger.go",
			content: "package main\n\nimport \"log\"\n\nfunc Info(msg string) { log.Println(\"INFO:\", msg) }\n",
			commit: "feat: add structured logging", title: "Add structured logging",
			desc: "Adds a simple logging module with Info level.",
		},
		{
			branch: "fix/config-defaults", file: "src/config_fix.go",
			content: "package main\n\nfunc DefaultConfig() Config {\n\treturn Config{Port: 8080, Host: \"localhost\"}\n}\n",
			commit: "fix: add default config values", title: "Fix: config defaults missing",
			desc: "Port and host were zero-valued when not set. Now defaults to 8080/localhost.",
		},
	}

	for _, mr := range mrs {
		fmt.Printf("  Creating MR: %s\n", mr.title)
		if opts.DryRun {
			continue
		}

		// Create branch
		api.post(idPath+"/repository/branches", map[string]any{
			"branch": mr.branch,
			"ref":    "main",
		}, nil)

		// Commit to branch
		api.post(idPath+"/repository/files/"+url.PathEscape(mr.file), map[string]any{
			"branch":         mr.branch,
			"content":        base64.StdEncoding.EncodeToString([]byte(mr.content)),
			"encoding":       "base64",
			"commit_message": mr.commit,
		}, nil)

		// Create MR
		api.post(idPath+"/merge_requests", map[string]any{
			"source_branch": mr.branch,
			"target_branch": "main",
			"title":         mr.title,
			"description":   mr.desc,
		}, nil)

		time.Sleep(500 * time.Millisecond)
	}

	// 4. Create issues
	issues := []struct {
		title string
		desc  string
	}{
		{"Investigate memory leak in worker pool", "RSS grows by 50MB/hour under load."},
		{"Add graceful shutdown", "Need to drain connections before exit."},
	}

	for _, iss := range issues {
		fmt.Printf("  Creating issue: %s\n", iss.title)
		if opts.DryRun {
			continue
		}
		api.post(idPath+"/issues", map[string]any{
			"title":       iss.title,
			"description": iss.desc,
		}, nil)
	}

	fmt.Printf("\nGitLab test data ready at: https://gitlab.com/%s/%s\n", username, project)
	return nil
}
