import {
  env,
  createExecutionContext,
  waitOnExecutionContext,
} from "cloudflare:test";
import { describe, it, expect, vi, beforeEach } from "vitest";
import worker from "../src/index";

const DUMMY_PICKUP_HASH = "b".repeat(64);

async function seedPending(state: string) {
  await env.OAUTH_SESSIONS.put(
    `pending:${state}`,
    JSON.stringify({ pickup_hash: DUMMY_PICKUP_HASH })
  );
}

describe("Google OAuth callback handler", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("returns 400 when code is missing", async () => {
    const request = new Request(
      "https://relay.devrecall.dev/oauth/google/callback?state=abc"
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
      "https://relay.devrecall.dev/oauth/google/callback?code=abc"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
  });

  it("returns success HTML and stores token on valid callback", async () => {
    const originalFetch = globalThis.fetch;
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url;

      if (url.includes("oauth2.googleapis.com/token")) {
        return new Response(
          JSON.stringify({
            access_token: "ya29.test-access-token",
            refresh_token: "1//test-refresh-token",
            expires_in: 3600,
            token_type: "Bearer",
            scope:
              "https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/userinfo.email",
          }),
          { headers: { "Content-Type": "application/json" } }
        );
      }

      if (url.includes("googleapis.com/oauth2/v2/userinfo")) {
        return new Response(
          JSON.stringify({
            id: "112233445566",
            email: "test@example.com",
            name: "Test User",
          }),
          { headers: { "Content-Type": "application/json" } }
        );
      }

      return originalFetch(input, init);
    });

    await seedPending("session-789abcdef0123456");
    const request = new Request(
      "https://relay.devrecall.dev/oauth/google/callback?code=valid-code&state=session-789abcdef0123456"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(200);
    const text = await response.text();
    expect(text).toContain("Successful");

    const stored = await env.OAUTH_SESSIONS.get("session:session-789abcdef0123456");
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored!);
    expect(parsed.token.access_token).toBe("ya29.test-access-token");
    expect(parsed.token.refresh_token).toBe("1//test-refresh-token");
    expect(parsed.token.email).toBe("test@example.com");
    expect(parsed.token.expires_in).toBe(3600);
    expect(parsed.pickup_hash).toBe(DUMMY_PICKUP_HASH);
  });

  it("returns error HTML when Google returns an error", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => {
      return new Response(
        JSON.stringify({
          error: "invalid_grant",
          error_description: "Code was already redeemed.",
        }),
        { headers: { "Content-Type": "application/json" } }
      );
    });

    await seedPending("session-000");
    const request = new Request(
      "https://relay.devrecall.dev/oauth/google/callback?code=bad-code&state=session-000"
    );
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const text = await response.text();
    expect(text).toContain("Code was already redeemed");
  });
});
