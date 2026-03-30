import {
  env,
  createExecutionContext,
  waitOnExecutionContext,
} from "cloudflare:test";
import { describe, it, expect, vi, beforeEach } from "vitest";
import worker from "../src/index";

describe("OAuth callback handler", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("returns 400 when code is missing", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/slack/callback?state=abc"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const text = await response.text();
    expect(text).toContain("Missing");
  });

  it("returns 400 when state is missing", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/slack/callback?code=abc"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
  });

  it("returns success HTML and stores token on valid callback", async () => {
    // Mock the Slack OAuth exchange.
    const originalFetch = globalThis.fetch;
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url;
      if (url.includes("slack.com/api/oauth.v2.access")) {
        return new Response(
          JSON.stringify({
            ok: true,
            authed_user: {
              id: "U123",
              access_token: "xoxp-test-token",
              token_type: "user",
              scope: "channels:history",
            },
            team: { id: "T456", name: "Test Team" },
          }),
          { headers: { "Content-Type": "application/json" } }
        );
      }
      return originalFetch(input, init);
    });

    const request = new Request(
      "https://relay.devrecall.dev/oauth/slack/callback?code=valid-code&state=session-123"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(200);
    const text = await response.text();
    expect(text).toContain("Successful");

    // Verify the token was stored in KV.
    const stored = await env.OAUTH_SESSIONS.get("session:session-123");
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored!);
    expect(parsed.access_token).toBe("xoxp-test-token");
    expect(parsed.team_id).toBe("T456");
  });

  it("returns error HTML when Slack returns an error", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => {
      return new Response(
        JSON.stringify({ ok: false, error: "invalid_code" }),
        { headers: { "Content-Type": "application/json" } }
      );
    });

    const request = new Request(
      "https://relay.devrecall.dev/oauth/slack/callback?code=bad-code&state=session-456"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const text = await response.text();
    expect(text).toContain("invalid_code");
  });
});
