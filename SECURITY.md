# Security Policy

## Reporting a vulnerability

If you believe you've found a security issue in DevRecall, please report it
privately rather than opening a public issue.

**Email:** security@devrecall.dev

Include:
- A description of the issue and its impact
- Steps to reproduce (a minimal proof-of-concept if possible)
- The affected version or commit SHA
- Your name and a way to reach you (for acknowledgement, if desired)

You'll get an acknowledgement within 72 hours. We aim to ship a fix or mitigation
within 14 days for high-severity issues, longer for lower-severity ones. We'll
keep you informed as the fix progresses and credit you in the release notes
unless you prefer to remain anonymous.

## Scope

In scope:
- The CLI (`cmd/devrecall`) and all packages under `internal/` and `pkg/`
- The desktop app (`desktop/`)
- The Cloudflare Worker relay (`relay/`)
- OAuth token storage, local API (`localhost:9147`), and the embedded SQLite DB

Out of scope:
- Third-party dependencies (report those to their maintainers)
- Social engineering, physical attacks, or issues requiring local access to an
  unlocked machine

## Safe harbor

We won't pursue legal action against researchers who:
- Report issues through the channel above rather than publicly
- Avoid privacy violations, data exfiltration, or service degradation
- Give us reasonable time to fix the issue before public disclosure
