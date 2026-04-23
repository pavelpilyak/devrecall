# DevRecall

On-device developer activity aggregator — generates AI-powered standups, perf reviews, and work memory from Git, Slack, Calendar, Jira, and Linear. All data stays local.

## Tech Stack

- **Language:** Go
- **Database:** SQLite (WAL mode, FTS5 for search)
- **CLI framework:** cobra
- **LLM:** Ollama (local) + BYOK (OpenAI/Anthropic)
- **Embeddings:** all-MiniLM-L6-v2 via ONNX (bundled)

## Project Structure

```
cmd/devrecall/          CLI entrypoint
internal/
  api/                  Local HTTP API (localhost:9147) for desktop app + integrations
  auth/                 OAuth flows + token storage (keychain/file-based)
  chat/                 Interactive chat REPL with conversation memory
  collector/            Source integrations (git, slack, calendar, jira, linear)
    collector.go        Collector interface
    git/                Git log parsing
    slack/              Slack API
    calendar/           Google Calendar API
    jira/               Jira API
    linear/             Linear GraphQL API
  config/               App configuration (~/.devrecall/config.json)
  embedding/            Vector embeddings (ONNX bundled, Ollama, OpenAI)
  identity/             Cross-source identity resolution (email-based)
  rag/                  Hybrid retrieval pipeline (vector + FTS5 + filters + re-ranking)
  storage/              SQLite database layer (includes FTS5 virtual table)
  summarizer/           LLM-powered summary generation (standup, weekly, brag, perf review)
pkg/models/             Shared domain types (Activity, Identity, Summary)
relay/                  Cloudflare Worker — OAuth callback relay (TypeScript)
```

## Commands

```bash
make build              # Build binary to bin/devrecall (tags: fts5 GO)
make test               # Run tests with race detector (tags: fts5 GO)
make lint               # Run golangci-lint
make relay-deploy       # Deploy Cloudflare Worker
make relay-test         # Run relay tests (vitest)
```

Build tags: `fts5` enables SQLite FTS5 full-text search, `GO` enables hugot's pure Go ONNX backend for embeddings.

**Node.js:** This project uses nvm. Always prefix JS/TS commands (`npx`, `npm`, `yarn`, `node`, `pnpm`) with `source ~/.nvm/nvm.sh && nvm use &&` to ensure the correct Node version from `.nvmrc` is used.

## Testing

- Every new feature or module should have tests. Test the meaningful behavior, not every line — focus on logic, edge cases, and integration points.
- Tests live next to the code they test (`foo_test.go` alongside `foo.go`).
- Use `make test` to run the full suite with the race detector.
- For storage/DB tests, use an in-memory SQLite (`:memory:`) or a temp file — never touch the real `~/.devrecall/` directory.
- Prefer table-driven tests for functions with multiple input/output scenarios.

## Key Design Decisions

- **Privacy-first:** All data stored on-device in SQLite. No raw user data sent to cloud.
- **Collector interface:** Each source implements `collector.Collector` — `Name()` + `Collect(ctx)`.
- **Identity resolution:** Email is the primary key for merging identities across Git, Slack, Calendar, Jira, Linear.
- **LLM strategy:** Local Ollama for fast tasks, BYOK for quality tasks. Fallback chain: primary → secondary → local → template.
- **Config location:** `~/.devrecall/config.json` for settings, `~/.devrecall/devrecall.db` for data.
- **Server port:** Default 9147 ("DRCL" on phone keypad). Override via `server.port` in config.json or `--port` flag on `devrecall serve`.
- **OAuth tokens:** Stored in `~/.devrecall/tokens/` (0600 permissions). OS keychain backend planned.

## Domain & Infrastructure

- **Domain:** `devrecall.dev` (owned)
- **Cloud relay:** `relay.devrecall.dev` — Cloudflare Worker, handles OAuth callbacks only.
- **Slack OAuth app:** registered at api.slack.com, redirect URI `https://relay.devrecall.dev/oauth/slack/callback`
