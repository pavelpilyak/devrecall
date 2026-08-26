# Security Policy

## Reporting a vulnerability

Open an issue: **https://github.com/pavelpilyak/devrecall/issues**

Include:
- A description of the issue and its impact
- Steps to reproduce (a minimal proof-of-concept if possible)
- The affected version or commit SHA

If the issue is serious enough that public disclosure before a fix would put
users at risk, say so in the first line and keep the details brief — we'll
follow up on how to share them.

DevRecall is maintained by one person, so response times vary. Issues that let
someone read your local database or exfiltrate OAuth tokens get looked at first.
You'll be credited in the release notes unless you'd rather not be.

## Scope

In scope:
- The CLI (`cmd/devrecall`) and all packages under `internal/` and `pkg/`
- The desktop app (`desktop/`)
- The Cloudflare Worker relay (`relay/`)
- OAuth token storage, local API (`localhost:3725`), and the embedded SQLite DB

Out of scope:
- Third-party dependencies (report those to their maintainers)
- Social engineering, physical attacks, or issues requiring local access to an
  unlocked machine

## Safe harbor

We won't pursue legal action against researchers who:
- Report issues through the channel above rather than weaponizing them
- Avoid privacy violations, data exfiltration, or service degradation
- Give us reasonable time to fix the issue before drawing wider attention to it
