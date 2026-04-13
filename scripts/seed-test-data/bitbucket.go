package main

import (
	"encoding/base64"
	"fmt"
	"time"
)

func seedBitbucket(opts *Options) error {
	username := env("BITBUCKET_USERNAME")
	appPass := env("BITBUCKET_APP_PASSWORD")
	workspace := env("BITBUCKET_WORKSPACE")
	repo := envOr("BITBUCKET_TEST_REPO", "devrecall-test-data")

	api := newAPI("https://api.bitbucket.org/2.0", map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+appPass)),
	})

	fullRepo := workspace + "/" + repo

	if opts.Clean {
		fmt.Printf("Deleting repo %s...\n", fullRepo)
		if opts.DryRun {
			return nil
		}
		return api.delete("/repositories/" + fullRepo)
	}

	// 1. Create repo
	fmt.Printf("Creating repo %s...\n", fullRepo)
	if !opts.DryRun {
		err := api.post("/repositories/"+fullRepo, map[string]any{
			"scm":         "git",
			"is_private":  false,
			"description": "DevRecall E2E test data — auto-generated",
		}, nil)
		if err != nil {
			fmt.Printf("  (repo may already exist: %v)\n", err)
		}
		time.Sleep(2 * time.Second)
	}

	// 2. Create branches + PRs
	prs := []struct {
		branch string
		title  string
		desc   string
	}{
		{"feat/api-v2", "Add API v2 endpoints", "New versioned API endpoints with better error handling."},
		{"fix/timeout", "Fix request timeout handling", "Increases default timeout from 5s to 30s and adds context cancellation."},
	}

	for _, pr := range prs {
		fmt.Printf("  Creating PR: %s\n", pr.title)
		if opts.DryRun {
			continue
		}

		// Create branch from main
		api.post("/repositories/"+fullRepo+"/refs/branches", map[string]any{
			"name": pr.branch,
			"target": map[string]string{
				"hash": "main",
			},
		}, nil)

		// Create PR
		api.post("/repositories/"+fullRepo+"/pullrequests", map[string]any{
			"title":       pr.title,
			"description": pr.desc,
			"source":      map[string]any{"branch": map[string]string{"name": pr.branch}},
			"destination": map[string]any{"branch": map[string]string{"name": "main"}},
		}, nil)

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("\nBitbucket test data ready at: https://bitbucket.org/%s\n", fullRepo)
	return nil
}
