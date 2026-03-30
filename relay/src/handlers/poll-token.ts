import { Env, StoredToken } from "../types";

export async function handlePollToken(
  url: URL,
  env: Env
): Promise<Response> {
  const sessionId = url.searchParams.get("session_id");
  if (!sessionId) {
    return Response.json({ error: "missing session_id" }, { status: 400 });
  }

  const key = `session:${sessionId}`;
  const value = await env.OAUTH_SESSIONS.get(key);
  if (!value) {
    return Response.json({ error: "pending" }, { status: 404 });
  }

  // Delete-on-read: token is consumed once.
  await env.OAUTH_SESSIONS.delete(key);

  const token: StoredToken = JSON.parse(value);
  return Response.json(token);
}
