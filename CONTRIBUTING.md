# Contributing to DevRecall

Thanks for your interest. DevRecall is a privacy-first local dev activity
aggregator, and contributions are welcome — especially new collectors,
bug fixes, and documentation.

## Ground rules

- **Privacy-first.** Anything that would send raw user data off-device needs
  explicit discussion in an issue before you send a PR.
- **Local-first by default.** New features should work without a network
  connection where possible.
- **Tests are not optional.** New collectors, storage changes, and RAG changes
  need tests. See `CLAUDE.md` for the testing philosophy.

## Getting started

```bash
git clone https://github.com/pavelpilyak/devrecall.git
cd devrecall
make build    # Builds bin/devrecall with fts5 + GO tags
make test     # Runs the full suite with the race detector
make lint     # golangci-lint
```

The relay (Cloudflare Worker) has its own workflow:

```bash
cd relay
source ~/.nvm/nvm.sh && nvm use
npm install
npm test
```

## Before opening a PR

- [ ] `make test` passes
- [ ] `make lint` is clean
- [ ] If you added a collector, it implements the `collector.Collector`
      interface and has a test with a fake transport
- [ ] If you touched storage, the change is backwards-compatible or includes
      a migration
- [ ] Commit messages are descriptive (imperative mood, optional body)

## Bug reports

Open an issue with:
- Your OS and DevRecall version (`devrecall version`)
- Steps to reproduce
- What you expected vs. what happened
- Any relevant log output (`~/.devrecall/logs/`)

## Security issues

Don't open a public issue. See [SECURITY.md](SECURITY.md).

## Scope of contributions

We're most interested in:
- New collectors (Confluence, Notion, additional git hosts)
- Embedding / RAG improvements
- Bug fixes and test coverage
- Docs and examples

Less likely to be merged:
- Cloud-sync features (we're deliberately local-first)
- Integrations with proprietary analytics or telemetry platforms
- Large architectural rewrites without prior discussion in an issue
