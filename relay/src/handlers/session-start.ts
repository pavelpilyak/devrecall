import { Env } from "../types";

const PENDING_TTL_SECONDS = 600; // 10 min to complete the browser flow

interface StartRequest {
  state: string;
  pickup_hash: string; // hex-encoded SHA-256 of the CLI-generated pickup secret
}

// Pre-registers an OAuth session. The CLI must call this before sending the
// user to the provider so that (a) callbacks for unregistered `state` values
// are rejected, and (b) the polling step can require proof-of-possession of
// the pickup secret whose hash was registered here.
export async function handleOAuthSessionStart(
  request: Request,
  env: Env
): Promise<Response> {
  const body = await request.json<StartRequest>().catch(() => null);
  if (!body?.state || !body?.pickup_hash) {
    return Response.json(
      { error: "missing state or pickup_hash" },
      { status: 400 }
    );
  }
  if (!/^[a-f0-9]{64}$/.test(body.pickup_hash)) {
    return Response.json(
      { error: "pickup_hash must be 64 hex chars (SHA-256)" },
      { status: 400 }
    );
  }
  if (body.state.length < 16 || body.state.length > 256) {
    return Response.json({ error: "invalid state length" }, { status: 400 });
  }

  await env.OAUTH_SESSIONS.put(
    `pending:${body.state}`,
    JSON.stringify({ pickup_hash: body.pickup_hash }),
    { expirationTtl: PENDING_TTL_SECONDS }
  );

  return Response.json({ status: "registered" });
}

const SESSION_TTL_SECONDS = 60;

interface StoredSession {
  token: unknown; // provider-specific shape, opaque to this module
  pickup_hash: string;
}

// Non-consuming existence check — used by OAuth callbacks to bail out
// *before* exchanging the provider code for a token when the `state` was
// never pre-registered. Keeps unregistered-state callbacks from burning
// real OAuth codes at the provider.
export async function isStateRegistered(
  state: string,
  env: Env
): Promise<boolean> {
  const pendingRaw = await env.OAUTH_SESSIONS.get(`pending:${state}`);
  return pendingRaw !== null;
}

// Consumes a pre-registered pending session and binds the provider token
// under `session:<state>` with the pickup_hash carried over from start.
// Returns null if no pending session exists — the caller should reject the
// OAuth callback to foil arbitrary attacker-chosen state values.
export async function bindProviderToken(
  state: string,
  token: unknown,
  env: Env
): Promise<boolean> {
  const pendingKey = `pending:${state}`;
  const pendingRaw = await env.OAUTH_SESSIONS.get(pendingKey);
  if (!pendingRaw) return false;

  let pending: { pickup_hash?: string };
  try {
    pending = JSON.parse(pendingRaw);
  } catch {
    return false;
  }
  if (!pending.pickup_hash) return false;

  const stored: StoredSession = { token, pickup_hash: pending.pickup_hash };
  await env.OAUTH_SESSIONS.put(`session:${state}`, JSON.stringify(stored), {
    expirationTtl: SESSION_TTL_SECONDS,
  });
  await env.OAUTH_SESSIONS.delete(pendingKey);
  return true;
}

