# Seed Test Data

Populates real sandbox accounts with realistic developer activity for end-to-end testing of DevRecall collectors.

## What it creates

| Service | Data |
|---------|------|
| **GitHub** | Repo with 5 commits, 3 issues (bug/enhancement/perf), 3 PRs (feat/fix/refactor), 1 review, 1 merged PR |
| **GitLab** | Project with 2 commits, 2 MRs, 2 issues |
| **Bitbucket** | Repo with 2 PRs |
| **Jira** | Scrum project with sprint, 5 issues (Story/Bug/Task), status transitions, comments in ADF format |
| **Confluence** | Space with 5 pages (RFCs, retro, ADR, onboarding), each with 2 versions |
| **Linear** | 5 issues with labels, state transitions, comments, varied priorities |
| **Slack** | Channel with 5 standalone messages, 2 threads (design discussion + incident) |
| **Google Calendar** | 7 events: standup, 1:1, sprint planning, architecture review, focus time, code review, all-day offsite |

## Setup

### 1. Create sandbox accounts

All services have free tiers. You need:

- **GitHub**: any account — the script creates a new test repo
- **GitLab**: any account — creates a new project
- **Bitbucket**: free Cloud account + workspace
- **Jira + Confluence**: free Atlassian Cloud (same account for both)
- **Linear**: free workspace — create a team with key `DRT` (or set `LINEAR_TEAM_KEY`)
- **Slack**: free workspace — create a Slack app with bot token (scopes: `chat:write`, `channels:manage`, `channels:join`)
- **Google Calendar**: personal Gmail works. Get an access token via `devrecall auth google`

### 2. Configure credentials

```bash
cp .env.example .env
# Fill in your tokens/keys
```

### 3. Run

```bash
# Seed everything
go run . --all

# Seed specific services
go run . --github --jira --linear

# Preview what would be created (no API calls)
go run . --all --dry-run

# Clean up (deletes repos/projects where supported)
go run . --all --clean
```

## After seeding: configure DevRecall

Once seed data exists, point DevRecall at the sandbox accounts:

```bash
devrecall setup  # or edit ~/.devrecall/config.json directly
```

Key config fields per service:

```jsonc
{
  "github": { "enabled": true, "username": "YOUR_USERNAME", "auth_mode": "pat" },
  "gitlab": { "enabled": true, "username": "YOUR_USERNAME" },
  "bitbucket": { "enabled": true, "username": "YOUR_USERNAME", "workspace": "YOUR_WORKSPACE" },
  "jira": { "enabled": true, "base_url": "https://YOUR.atlassian.net", "auth_mode": "api-token", "email": "YOU@EXAMPLE.COM" },
  "confluence": { "enabled": true },
  "linear": { "enabled": true, "auth_mode": "api-key" },
  "slack": { "enabled": true },
  "calendar": { "enabled": true, "email": "YOU@GMAIL.COM" }
}
```

Then authenticate and sync:

```bash
devrecall auth github   # or each service
devrecall sync
devrecall status        # verify all sources show data
```

## Notes

- **Slack seeding** uses a bot token, but DevRecall's collector uses a user token (`search:read` scope). The bot creates the messages; your user token finds them via `search.messages`. Auth separately with `devrecall auth slack`.
- **Google Calendar** tokens are short-lived. If the seed script gets a 401, refresh via `devrecall auth google` and copy the new token.
- **Jira/Confluence cleanup** must be done manually (API doesn't support project deletion on free tier).
- **Linear** requires the team to exist before seeding. Create a team with the configured key (default: `DRT`) in the Linear UI first.
- All created data uses realistic names/descriptions to test LLM summarization quality.
