import {
  env,
  createExecutionContext,
  waitOnExecutionContext,
} from "cloudflare:test";
import { describe, it, expect } from "vitest";
import worker from "../src/index";

async function sha256Hex(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function seedBoundSession(
  state: string,
  token: unknown,
  secret: string
) {
  const hash = await sha256Hex(secret);
  await env.OAUTH_SESSIONS.put(
    `session:${state}`,
    JSON.stringify({ token, pickup_hash: hash })
  );
}

describe("Poll token handler", () => {
  it("returns 400 when session_id is missing", async () => {
    const request = new Request("https://relay.devrecall.dev/oauth/poll", {
      headers: { Authorization: "Bearer any" },
    });
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const body = await response.json<{ error: string }>();
    expect(body.error).toBe("missing session_id");
  });

  it("returns 401 when Authorization header is missing", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=nope"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(401);
  });

  it("returns 404 when session is not yet available", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=nonexistent",
      { headers: { Authorization: "Bearer any" } }
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(404);
  });

  it("returns 401 when pickup secret is wrong", async () => {
    const token = { access_token: "xoxp-secret-test" };
    await seedBoundSession("poll-wrong", token, "the-real-secret");

    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=poll-wrong",
      { headers: { Authorization: "Bearer not-the-secret" } }
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(401);

    // Session must NOT have been deleted on a wrong-secret attempt,
    // otherwise an attacker could wipe a pending session by guessing.
    const stillThere = await env.OAUTH_SESSIONS.get("session:poll-wrong");
    expect(stillThere).not.toBeNull();
  });

  it("returns token and deletes it from KV on success", async () => {
    const token = {
      access_token: "xoxp-poll-test",
      user_id: "U789",
      team_id: "T012",
      team_name: "Poll Team",
      scope: "channels:read",
    };
    await seedBoundSession("poll-test", token, "the-pickup-secret");

    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=poll-test",
      { headers: { Authorization: "Bearer the-pickup-secret" } }
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(200);
    const body = await response.json<typeof token>();
    expect(body.access_token).toBe("xoxp-poll-test");
    expect(body.team_id).toBe("T012");

    // Token should be deleted after read.
    const afterRead = await env.OAUTH_SESSIONS.get("session:poll-test");
    expect(afterRead).toBeNull();
  });
});

describe("Health endpoint", () => {
  it("returns ok", async () => {
    const request = new Request("https://relay.devrecall.dev/health");
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(200);
    const body = await response.json<{ status: string }>();
    expect(body.status).toBe("ok");
  });
});

describe("Unknown routes", () => {
  it("returns 404", async () => {
    const request = new Request("https://relay.devrecall.dev/unknown");
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(404);
  });
});
