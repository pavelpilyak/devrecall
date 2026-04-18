import { Env } from "../types";

interface StoredSession {
  token: unknown;
  pickup_hash: string;
}

async function sha256Hex(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function constantTimeEqualHex(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return diff === 0;
}

// Returns the stored provider token only when the caller presents a Bearer
// `pickup_secret` whose SHA-256 matches the `pickup_hash` registered at
// `/oauth/session/start`. This binds the polling credential to the CLI that
// initiated the flow, so that knowing (or phishing) the `state` value alone
// is not enough to redeem the token.
export async function handlePollToken(
  request: Request,
  url: URL,
  env: Env
): Promise<Response> {
  const sessionId = url.searchParams.get("session_id");
  if (!sessionId) {
    return Response.json({ error: "missing session_id" }, { status: 400 });
  }

  const authHeader = request.headers.get("Authorization") || "";
  if (!authHeader.startsWith("Bearer ")) {
    return Response.json({ error: "missing pickup secret" }, { status: 401 });
  }
  const pickupSecret = authHeader.slice(7).trim();
  if (!pickupSecret) {
    return Response.json({ error: "missing pickup secret" }, { status: 401 });
  }

  const key = `session:${sessionId}`;
  const value = await env.OAUTH_SESSIONS.get(key);
  if (!value) {
    return Response.json({ error: "pending" }, { status: 404 });
  }

  let stored: StoredSession;
  try {
    stored = JSON.parse(value);
  } catch {
    await env.OAUTH_SESSIONS.delete(key);
    return Response.json({ error: "corrupt session" }, { status: 500 });
  }

  const presented = await sha256Hex(pickupSecret);
  if (!constantTimeEqualHex(presented, stored.pickup_hash)) {
    // Do NOT delete on mismatch — that would let an attacker invalidate the
    // legitimate CLI's pickup. Instead, keep the session and let it expire.
    return Response.json({ error: "invalid pickup secret" }, { status: 401 });
  }

  // Delete-on-successful-read: the token is consumed once.
  await env.OAUTH_SESSIONS.delete(key);
  return Response.json(stored.token);
}
