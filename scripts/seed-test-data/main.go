// seed-test-data populates real sandbox accounts with realistic developer activity
// for end-to-end testing of DevRecall collectors.
//
// Usage:
//
//	go run . --all              # seed everything
//	go run . --github --jira    # seed specific services
//	go run . --clean            # tear down test data
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func main() {
	var (
		all        = flag.Bool("all", false, "Seed all services")
		github     = flag.Bool("github", false, "Seed GitHub (repo + PRs + issues + reviews)")
		gitlab     = flag.Bool("gitlab", false, "Seed GitLab (project + MRs + issues)")
		bitbucket  = flag.Bool("bitbucket", false, "Seed Bitbucket (repo + PRs)")
		jira       = flag.Bool("jira", false, "Seed Jira (sprint + issues + transitions + comments)")
		confluence = flag.Bool("confluence", false, "Seed Confluence (space + pages)")
		linear     = flag.Bool("linear", false, "Seed Linear (issues + state changes + comments)")
		slack      = flag.Bool("slack", false, "Seed Slack (channel + messages + threads)")
		calendar   = flag.Bool("calendar", false, "Seed Google Calendar (mixed meeting types)")
		clean      = flag.Bool("clean", false, "Tear down test data instead of creating it")
		dryRun     = flag.Bool("dry-run", false, "Print what would be created without doing it")
	)
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		// .env is optional — env vars can come from shell
		log.Printf("Note: no .env file found, using environment variables")
	}

	if !*all && !*github && !*gitlab && !*bitbucket && !*jira && !*confluence && !*linear && !*slack && !*calendar {
		fmt.Println("Usage: go run . --all | --github --jira --linear ...")
		fmt.Println("\nFlags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	opts := &Options{Clean: *clean, DryRun: *dryRun}

	type seedFunc struct {
		name    string
		enabled bool
		fn      func(*Options) error
	}

	seeds := []seedFunc{
		{"GitHub", *all || *github, seedGitHub},
		{"GitLab", *all || *gitlab, seedGitLab},
		{"Bitbucket", *all || *bitbucket, seedBitbucket},
		{"Jira", *all || *jira, seedJira},
		{"Confluence", *all || *confluence, seedConfluence},
		{"Linear", *all || *linear, seedLinear},
		{"Slack", *all || *slack, seedSlack},
		{"Google Calendar", *all || *calendar, seedCalendar},
	}

	var failed []string
	for _, s := range seeds {
		if !s.enabled {
			continue
		}
		action := "Seeding"
		if *clean {
			action = "Cleaning"
		}
		fmt.Printf("\n=== %s %s ===\n", action, s.name)
		if err := s.fn(opts); err != nil {
			log.Printf("ERROR [%s]: %v", s.name, err)
			failed = append(failed, s.name)
		} else {
			fmt.Printf("=== %s: OK ===\n", s.name)
		}
	}

	fmt.Println()
	if len(failed) > 0 {
		log.Fatalf("Failed: %s", strings.Join(failed, ", "))
	}
	fmt.Println("All done!")
}

// Options shared across all seed functions.
type Options struct {
	Clean  bool
	DryRun bool
}

func env(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Required env var %s is not set. See .env.example", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
