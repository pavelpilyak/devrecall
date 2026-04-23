import { describe, it, expect } from "vitest";
import { handleVersion } from "../src/handlers/version";
import { Env } from "../src/types";

function envWith(overrides: Partial<Env> = {}): Env {
  return {
    OAUTH_SESSIONS: {} as any,
    SLACK_CLIENT_ID: "",
    SLACK_CLIENT_SECRET: "",
    GOOGLE_CLIENT_ID: "",
    GOOGLE_CLIENT_SECRET: "",
    GITHUB_CLIENT_ID: "",
    GITHUB_CLIENT_SECRET: "",
    ATLASSIAN_CLIENT_ID: "",
    ATLASSIAN_CLIENT_SECRET: "",
    LINEAR_CLIENT_ID: "",
    LINEAR_CLIENT_SECRET: "",
    LATEST_VERSION: "",
    MIN_REQUIRED_VERSION: "",
    UPDATE_MESSAGE: "",
    ...overrides,
  };
}

describe("handleVersion", () => {
  it("returns configured versions", async () => {
    const req = new Request("https://relay.devrecall.dev/v1/version");
    const env = envWith({
      LATEST_VERSION: "v0.5.0",
      MIN_REQUIRED_VERSION: "v0.4.0",
      UPDATE_MESSAGE: "Critical security fix",
    });

    const resp = await handleVersion(req, env);
    expect(resp.status).toBe(200);

    const body = (await resp.json()) as Record<string, string>;
    expect(body.latest_version).toBe("v0.5.0");
    expect(body.min_required_version).toBe("v0.4.0");
    expect(body.message).toBe("Critical security fix");
  });

  it("falls back to v0.0.0 when env vars are unset", async () => {
    const req = new Request("https://relay.devrecall.dev/v1/version");
    const resp = await handleVersion(req, envWith());
    expect(resp.status).toBe(200);

    const body = (await resp.json()) as Record<string, string>;
    expect(body.latest_version).toBe("v0.0.0");
    expect(body.min_required_version).toBe("v0.0.0");
    expect(body.message).toBe("");
  });

  it("sets a cache-control header for CDN caching", async () => {
    const req = new Request("https://relay.devrecall.dev/v1/version");
    const resp = await handleVersion(req, envWith({ LATEST_VERSION: "v0.5.0" }));
    expect(resp.headers.get("cache-control")).toContain("max-age");
  });

  it("omits message when not set", async () => {
    const req = new Request("https://relay.devrecall.dev/v1/version");
    const env = envWith({
      LATEST_VERSION: "v0.5.0",
      MIN_REQUIRED_VERSION: "v0.4.0",
    });

    const resp = await handleVersion(req, env);
    const body = (await resp.json()) as Record<string, string>;
    expect(body.message).toBe("");
  });
});
