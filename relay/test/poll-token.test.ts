import {
  env,
  createExecutionContext,
  waitOnExecutionContext,
} from "cloudflare:test";
import { describe, it, expect } from "vitest";
import worker from "../src/index";

describe("Poll token handler", () => {
  it("returns 400 when session_id is missing", async () => {
    const request = new Request("https://relay.devrecall.dev/oauth/poll");
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const body = await response.json<{ error: string }>();
    expect(body.error).toBe("missing session_id");
  });

  it("returns 404 when token is not yet available", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=nonexistent"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(404);
  });

  it("returns token and deletes it from KV on success", async () => {
    const token = {
      access_token: "xoxp-poll-test",
      user_id: "U789",
      team_id: "T012",
      team_name: "Poll Team",
      scope: "channels:read",
    };
    await env.OAUTH_SESSIONS.put("session:poll-test", JSON.stringify(token));

    const request = new Request(
      "https://relay.devrecall.dev/oauth/poll?session_id=poll-test"
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
