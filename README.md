# DevRecall

**Your developer activity, aggregated on-device.** No cloud sync. No telemetry.
Your data never leaves your machine.

DevRecall pulls from Git, Slack, Google Calendar, Jira, Linear, Confluence,
and GitHub/GitLab/Bitbucket; stores it in a local SQLite database; and turns
it into standups, weekly reports, brag docs, and a chat that actually knows
what you worked on.

Also ships an [MCP server](https://docs.devrecall.dev/integrations/mcp/) so
Claude Code, Cursor, Codex, Continue, and Zed gain memory of everything
you've shipped — `/devrecall:recall what auth bug did I fix in February`
returns cited commits, PRs, and tickets inline.

<p align="center">
  <img src=".github/assets/chat.jpg" alt="DevRecall desktop app — chat over your local work history" width="820">
</p>

📚 **[docs.devrecall.dev](https://docs.devrecall.dev)** — install, configure, integrations, CLI reference.

## Why

- **Local-first.** SQLite on your laptop. Tokens in `~/.devrecall/tokens/` (`0600`). The Cloudflare Worker relay is a pass-through for OAuth callbacks only — it never sees your data.
- **LLM-optional.** Bundled embeddings run offline (ONNX + `all-MiniLM-L6-v2`). Use local Ollama for chat, or bring your own OpenAI/Anthropic key.
- **Open source.** MIT-licensed. Audit it, fork it, build it from source.

## Sources

| Source            | What gets collected                            |
| ----------------- | ---------------------------------------------- |
| Git (local)       | Commits, branch activity, files changed        |
| GitHub / GitLab / Bitbucket | PRs/MRs, reviews, issues, comments   |
| Slack             | Your messages, threads you participated in     |
| Google Calendar   | Meetings attended, organized, declined         |
| Jira / Linear     | Issue transitions, comments, sprint membership |
| Confluence        | Pages, blogposts, and comments you authored    |

## Install

Homebrew ships with the first tagged release. Until then:

```bash
git clone https://github.com/pavelpilyak/devrecall.git
cd devrecall
make build          # → bin/devrecall
```

Requires Go 1.22+ with CGO enabled (for SQLite FTS5).

Full install + setup walkthrough at **[docs.devrecall.dev/install](https://docs.devrecall.dev/install/)**.

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────────┐
│  Collectors │ →   │  SQLite     │ →   │  Summarizer /   │
│  (OAuth +   │     │  + FTS5 +   │     │  RAG (vector    │
│   APIs)     │     │  embeddings │     │  + FTS + LLM)   │
└─────────────┘     └─────────────┘     └─────────────────┘
      ↑                   ↑                      ↓
  ~/.devrecall/     ~/.devrecall/           standup,
    tokens/          devrecall.db          brag, chat,
                                           perf review
```

Module overview: [`CLAUDE.md`](CLAUDE.md). Architecture deep-dive: [docs.devrecall.dev/architecture](https://docs.devrecall.dev/architecture/).

## Development

```bash
make build              # bin/devrecall
make test               # tests with race detector
make lint               # golangci-lint
```

Build tags: `fts5` enables SQLite full-text search; `GO` enables hugot's pure
Go ONNX backend for embeddings.

The desktop app (Tauri + Svelte) lives in [`desktop/`](desktop/);
the OAuth callback relay (Cloudflare Worker) in [`relay/`](relay/).

## Contributing

Bug reports and collector contributions are especially welcome.
See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

[SECURITY.md](SECURITY.md) for responsible disclosure.
