import { describe, it, expect, vi, beforeEach } from "vitest";
import { handleGoogleRefresh } from "../src/handlers/refresh-google";
import { Env } from "../src/types";

function mockEnv(): Env {
  return {
    OAUTH_SESSIONS: {} as any,
    BACKUP_STORE: {} as any,
    LICENSE_DB: {} as any,
    SLACK_CLIENT_ID: "slack-id",
    SLACK_CLIENT_SECRET: "slack-secret",
    GOOGLE_CLIENT_ID: "google-client-id",
    GOOGLE_CLIENT_SECRET: "google-client-secret",
    GITHUB_CLIENT_ID: "github-id",
    GITHUB_CLIENT_SECRET: "github-secret",
    ATLASSIAN_CLIENT_ID: "atlassian-id",
    ATLASSIAN_CLIENT_SECRET: "atlassian-secret",
    LINEAR_CLIENT_ID: "linear-id",
    LINEAR_CLIENT_SECRET: "linear-secret",
    LICENSE_SIGNING_KEY: "",
    LEMON_SQUEEZY_WEBHOOK_SECRET: "",
  };
}

describe("handleGoogleRefresh", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("rejects non-POST requests", async () => {
    const req = new Request("https://relay.devrecall.dev/oauth/google/refresh", {
      method: "GET",
    });
    const resp = await handleGoogleRefresh(req, mockEnv());
    expect(resp.status).toBe(405);
  });

  it("rejects missing refresh_token", async () => {
    const req = new Request("https://relay.devrecall.dev/oauth/google/refresh", {
      method: "POST",
      body: JSON.stringify({}),
      headers: { "Content-Type": "application/json" },
    });
    const resp = await handleGoogleRefresh(req, mockEnv());
    expect(resp.status).toBe(400);
    const body = await resp.json();
    expect(body.error).toBe("missing_refresh_token");
  });

  it("returns new access token on success", async () => {
    const googleResponse = {
      access_token: "new-access-token",
      expires_in: 3600,
      scope: "calendar.readonly",
    };

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(googleResponse), { status: 200 })
    );

    const req = new Request("https://relay.devrecall.dev/oauth/google/refresh", {
      method: "POST",
      body: JSON.stringify({ refresh_token: "my-refresh-token" }),
      headers: { "Content-Type": "application/json" },
    });

    const resp = await handleGoogleRefresh(req, mockEnv());
    expect(resp.status).toBe(200);

    const body = await resp.json();
    expect(body.access_token).toBe("new-access-token");
    expect(body.expires_in).toBe(3600);
  });

  it("returns error when Google rejects refresh", async () => {
    const googleError = {
      error: "invalid_grant",
      error_description: "Token has been revoked",
    };

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(googleError), { status: 400 })
    );

    const req = new Request("https://relay.devrecall.dev/oauth/google/refresh", {
      method: "POST",
      body: JSON.stringify({ refresh_token: "revoked-token" }),
      headers: { "Content-Type": "application/json" },
    });

    const resp = await handleGoogleRefresh(req, mockEnv());
    expect(resp.status).toBe(400);

    const body = await resp.json();
    expect(body.error).toBe("invalid_grant");
  });
});
