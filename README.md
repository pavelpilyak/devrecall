# DevRecall

**Your developer activity, aggregated on-device.** DevRecall pulls from Git,
Slack, Google Calendar, Jira, Linear, and GitHub/GitLab/Bitbucket, stores it in
a local SQLite database, and generates standups, weekly reports, brag docs,
and perf-review material on demand. All data stays on your machine.

- **Local-first.** No cloud sync. No telemetry. Your data never leaves your
  device unless you explicitly ask it to.
- **Private by default.** OAuth tokens live in `~/.devrecall/tokens/` with
  `0600` permissions. The Cloudflare Worker relay is a pass-through for OAuth
  callbacks only — it never sees your data.
- **Open source.** MIT-licensed. Fork it, audit it, self-compile it.
- **LLM-optional.** Bundled embeddings run offline (ONNX + `all-MiniLM-L6-v2`).
  Use local Ollama for chat, or bring your own OpenAI/Anthropic key.

## Install

Prebuilt binaries (Homebrew, `.deb`) ship with the first tagged release.
Until then, build from source.

### From source

```bash
git clone https://github.com/pavelpilyak/devrecall.git
cd devrecall
make build          # bin/devrecall
```

Requires Go 1.22+ with CGO enabled (for SQLite FTS5).

### Homebrew (macOS, once released)

```bash
brew tap pavelpilyak/devrecall https://github.com/pavelpilyak/devrecall
brew install devrecall
```

### Linux .deb (once released)

Download the latest `.deb` from [Releases](https://github.com/pavelpilyak/devrecall/releases)
and install with `dpkg -i devrecall_*.deb`.

## Quickstart

```bash
# 1. Connect a source (opens browser for OAuth)
devrecall connect slack
devrecall connect github

# 2. Pull activity from the last 7 days
devrecall sync --since 7d

# 3. Generate a standup
devrecall standup

# 4. Chat with your work history
devrecall chat
> what did I ship last sprint?
```

Run `devrecall --help` for the full command list.

## What it collects

| Source     | What                                                  |
| ---------- | ----------------------------------------------------- |
| Git        | Commits, PR descriptions, branch activity             |
| GitHub     | PRs, reviews, issues, comments                        |
| GitLab     | MRs, reviews, issues                                  |
| Bitbucket  | PRs, comments                                         |
| Slack      | Messages in channels you're in                        |
| Calendar   | Google Calendar events                                |
| Jira       | Tickets you touched                                   |
| Linear     | Issues you touched                                    |

## How it works

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

See [`CLAUDE.md`](CLAUDE.md) for the architecture overview.

## Development

```bash
make build              # Build to bin/devrecall
make test               # Run tests with race detector
make lint               # golangci-lint
make relay-deploy       # Deploy Cloudflare Worker (maintainers only)
```

Build tags: `fts5` enables SQLite full-text search; `GO` enables hugot's pure
Go ONNX backend for embeddings.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports and collector contributions
are especially welcome.

## Security

See [SECURITY.md](SECURITY.md) for responsible disclosure.

## License

[MIT](LICENSE) © 2026 Pavel Piliak
