import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock fetch globally before importing api.
const mockFetch = vi.fn();
vi.stubGlobal("fetch", mockFetch);

import { api } from "./api";

function mockJsonResponse(data: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? "OK" : "Error",
    json: () => Promise.resolve(data),
  };
}

beforeEach(() => {
  mockFetch.mockReset();
});

describe("api.status", () => {
  it("fetches /api/status", async () => {
    mockFetch.mockResolvedValue(mockJsonResponse({ status: "ok", sources: [] }));

    const result = await api.status();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/status");
    expect(result.status).toBe("ok");
  });

  it("throws on error response", async () => {
    mockFetch.mockResolvedValue(mockJsonResponse({ error: "db error" }, 500));

    await expect(api.status()).rejects.toThrow("db error");
  });
});

describe("api.standup", () => {
  it("fetches without date param by default", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ date: "2026-04-05", report: "test", activity_count: 1 })
    );

    await api.standup();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/standup");
  });

  it("passes date param when provided", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ date: "2026-03-01", report: "test", activity_count: 0 })
    );

    await api.standup("2026-03-01");

    expect(mockFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:9147/api/standup?date=2026-03-01"
    );
  });
});

describe("api.week", () => {
  it("fetches current week by default", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ week_start: "2026-03-30", week_end: "2026-04-05", report: "", activity_count: 0 })
    );

    await api.week();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/week");
  });

  it("passes weeks_back param", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ week_start: "2026-03-23", week_end: "2026-03-29", report: "", activity_count: 0 })
    );

    await api.week(1);

    expect(mockFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:9147/api/week?weeks_back=1"
    );
  });
});

describe("api.activities", () => {
  it("fetches without filters by default", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ activities: [], count: 0 })
    );

    await api.activities();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/activities");
  });

  it("builds query string from filters", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ activities: [], count: 0 })
    );

    await api.activities({ source: "git", after: "2026-03-01", limit: 10 });

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("source=git");
    expect(url).toContain("after=2026-03-01");
    expect(url).toContain("limit=10");
  });
});

describe("api.search", () => {
  it("passes query and options", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ query: "auth", results: [], count: 0 })
    );

    await api.search("auth", { source: "git", limit: 5 });

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("q=auth");
    expect(url).toContain("source=git");
    expect(url).toContain("limit=5");
  });

  it("requires query parameter", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ error: "missing required parameter: q" }, 400)
    );

    await expect(api.search("")).rejects.toThrow();
  });
});

describe("api.chat", () => {
  it("sends POST with message and history", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ response: "You worked on auth", sources_count: 3 })
    );

    const history = [{ role: "user", content: "hi" }];
    const result = await api.chat("what did I do?", history);

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message: "what did I do?", history }),
    });
    expect(result.response).toBe("You worked on auth");
    expect(result.sources_count).toBe(3);
  });
});

describe("api.activate", () => {
  it("sends POST with license key", async () => {
    const licenseData = {
      plan: "pro",
      features: ["chat", "slack"],
      devices_used: 1,
      devices_allowed: 1,
    };
    mockFetch.mockResolvedValue(
      mockJsonResponse({ message: "pro plan activated", license: licenseData })
    );

    const result = await api.activate("DR-PRO-A1B2-C3D4-E5F6");

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/activate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: "DR-PRO-A1B2-C3D4-E5F6" }),
    });
    expect(result.message).toBe("pro plan activated");
    expect(result.license.plan).toBe("pro");
  });

  it("throws on invalid key", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ error: "invalid license key format" }, 400)
    );

    await expect(api.activate("INVALID")).rejects.toThrow("invalid license key format");
  });
});

describe("api.sync", () => {
  it("sends POST to /api/sync", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ message: "sync acknowledged" })
    );

    const result = await api.sync();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: undefined,
    });
    expect(result.message).toBe("sync acknowledged");
  });
});

describe("api.log", () => {
  it("sends POST with text only", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ id: 42, timestamp: "2026-04-08T10:00:00Z", title: "Talked to mobile team" })
    );

    const result = await api.log({ text: "Talked to mobile team" });

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:9147/api/log", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: "Talked to mobile team" }),
    });
    expect(result.id).toBe(42);
    expect(result.title).toBe("Talked to mobile team");
  });

  it("sends POST with full payload", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ id: 7, timestamp: "2026-04-01T09:30:00Z", title: "Decision call" })
    );

    await api.log({
      text: "Decision call",
      at: "2026-04-01 09:30",
      tags: ["decision"],
      people: ["anna@example.com"],
    });

    const call = mockFetch.mock.calls[0];
    expect(call[0]).toBe("http://127.0.0.1:9147/api/log");
    const body = JSON.parse(call[1].body);
    expect(body).toEqual({
      text: "Decision call",
      at: "2026-04-01 09:30",
      tags: ["decision"],
      people: ["anna@example.com"],
    });
  });

  it("throws on validation error", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ error: "missing required field: text" }, 400)
    );

    await expect(api.log({ text: "" })).rejects.toThrow("missing required field: text");
  });
});
