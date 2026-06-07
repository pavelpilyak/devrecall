# DevRecall Cloud Relay

Cloudflare Worker that handles OAuth callbacks for DevRecall. The relay never sees raw user data — it only passes through OAuth tokens during authentication.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/oauth/slack/callback` | Receives Slack OAuth redirect, exchanges code for token |
| GET | `/oauth/poll?session_id=xxx` | CLI polls for token after user authorizes |
| GET | `/health` | Health check |

## Setup (one-time)

```bash
# 1. Install dependencies
cd relay
npm install

# 2. Login to Cloudflare
npx wrangler login

# 3. Create KV namespace (already done, ID is in wrangler.toml)
npx wrangler kv namespace create OAUTH_SESSIONS

# 4. Set secrets
npx wrangler secret put SLACK_CLIENT_ID
npx wrangler secret put SLACK_CLIENT_SECRET

# 5. Deploy
npx wrangler deploy
```

## Development

```bash
npm run dev          # local dev server (wrangler dev)
npm test             # run tests (vitest)
```

## Deployment

From the project root:

```bash
make relay-deploy    # deploy to Cloudflare
make relay-test      # run relay tests
```

Or from the `relay/` directory:

```bash
npx wrangler deploy
```

## DNS

`relay.devrecall.dev` routes to this Worker. Configured via Cloudflare DNS (CNAME → `devrecall-relay.<account>.workers.dev`, proxied).

## Secrets

Managed via `npx wrangler secret put <NAME>`. Never committed to code.

| Secret | Source |
|--------|--------|
| `SLACK_CLIENT_ID` | Slack app → Basic Information → App Credentials |
| `SLACK_CLIENT_SECRET` | Slack app → Basic Information → App Credentials |
