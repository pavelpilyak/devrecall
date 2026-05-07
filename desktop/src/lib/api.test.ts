import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock @tauri-apps/api/core before importing api.
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn().mockResolvedValue("http://127.0.0.1:3725"),
}));

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

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/status");
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

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/standup");
  });

  it("passes date param when provided", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ date: "2026-03-01", report: "test", activity_count: 0 })
    );

    await api.standup("2026-03-01");

    expect(mockFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3725/api/standup?date=2026-03-01"
    );
  });
});

describe("api.week", () => {
  it("fetches current week by default", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ week_start: "2026-03-30", week_end: "2026-04-05", report: "", activity_count: 0 })
    );

    await api.week();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/week");
  });

  it("passes weeks_back param", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ week_start: "2026-03-23", week_end: "2026-03-29", report: "", activity_count: 0 })
    );

    await api.week(1);

    expect(mockFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3725/api/week?weeks_back=1"
    );
  });
});

describe("api.activities", () => {
  it("fetches without filters by default", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ activities: [], count: 0 })
    );

    await api.activities();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/activities");
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

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message: "what did I do?", history }),
    });
    expect(result.response).toBe("You worked on auth");
    expect(result.sources_count).toBe(3);
  });
});

describe("api.chatStream", () => {
  // Builds a fake fetch response whose body is a ReadableStream that
  // emits the given SSE-formatted text in chunks. Lets us exercise the
  // line-buffer parser without spinning up a real server.
  function mockSSEResponse(chunks: string[]) {
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const c of chunks) controller.enqueue(encoder.encode(c));
        controller.close();
      },
    });
    return {
      ok: true,
      status: 200,
      statusText: "OK",
      body,
      json: () => Promise.resolve({}),
    };
  }

  it("decodes a multi-event SSE stream", async () => {
    const sse =
      "event: thinking\ndata: {\"step\":1}\n\n" +
      "event: tool_call\ndata: {\"step\":1,\"tool_name\":\"current_time\",\"tool_args\":{}}\n\n" +
      "event: tool_result\ndata: {\"step\":1,\"tool_name\":\"current_time\",\"tool_result\":{\"now\":\"noon\"},\"duration_ms\":3}\n\n" +
      "event: token\ndata: {\"token\":\"It is \"}\n\n" +
      "event: token\ndata: {\"token\":\"noon.\"}\n\n" +
      "event: done\ndata: {\"step\":2,\"content\":\"It is noon.\"}\n\n";

    mockFetch.mockResolvedValue(mockSSEResponse([sse]));

    const events: any[] = [];
    await api.chatStream("what time is it?", (ev) => events.push(ev));

    const types = events.map((e) => e.type);
    expect(types).toEqual([
      "thinking",
      "tool_call",
      "tool_result",
      "token",
      "token",
      "done",
    ]);
    expect(events[3].token).toBe("It is ");
    expect(events[5].content).toBe("It is noon.");
  });

  it("handles chunks split mid-line", async () => {
    // Same payload, but the bytes are sliced arbitrarily across reads —
    // the buffered parser must still reassemble each frame correctly.
    const sse =
      "event: token\ndata: {\"token\":\"hi\"}\n\nevent: done\ndata: {\"content\":\"hi\"}\n\n";
    const chunks = [sse.slice(0, 10), sse.slice(10, 25), sse.slice(25)];

    mockFetch.mockResolvedValue(mockSSEResponse(chunks));

    const events: any[] = [];
    await api.chatStream("hi", (ev) => events.push(ev));

    expect(events.map((e) => e.type)).toEqual(["token", "done"]);
    expect(events[0].token).toBe("hi");
  });

  it("rejects when the server responds with an error", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 503,
      statusText: "Service Unavailable",
      body: null,
      json: () => Promise.resolve({ error: "LLM not configured" }),
    });

    await expect(api.chatStream("hi", () => {})).rejects.toThrow(
      "LLM not configured"
    );
  });
});

describe("api.sync", () => {
  it("sends POST to /api/sync", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ message: "sync acknowledged" })
    );

    const result = await api.sync();

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: undefined,
    });
    expect(result.message).toBe("sync acknowledged");
  });
});

describe("api.syncStream", () => {
  function mockSSEResponse(chunks: string[]) {
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const c of chunks) controller.enqueue(encoder.encode(c));
        controller.close();
      },
    });
    return {
      ok: true,
      status: 200,
      statusText: "OK",
      body,
      json: () => Promise.resolve({}),
    };
  }

  it("decodes per-source freshness frames and the terminal done frame", async () => {
    const sse =
      'event: freshness\ndata: {"source":"git","status":"syncing"}\n\n' +
      'event: freshness\ndata: {"source":"git","status":"synced","added":3}\n\n' +
      'event: freshness\ndata: {"source":"slack","status":"syncing"}\n\n' +
      'event: freshness\ndata: {"source":"slack","status":"synced","added":7}\n\n' +
      'event: done\ndata: {"total_added":10}\n\n';

    mockFetch.mockResolvedValue(mockSSEResponse([sse]));

    const events: any[] = [];
    await api.syncStream((ev) => events.push(ev));

    expect(mockFetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3725/api/sync/stream",
      expect.objectContaining({ method: "POST" })
    );
    expect(events.map((e) => e.type)).toEqual([
      "freshness",
      "freshness",
      "freshness",
      "freshness",
      "done",
    ]);
    expect(events[0].source).toBe("git");
    expect(events[1].added).toBe(3);
    expect(events[4].total_added).toBe(10);
  });

  it("surfaces error frames mid-stream without rejecting", async () => {
    const sse =
      'event: freshness\ndata: {"source":"jira","status":"syncing"}\n\n' +
      'event: freshness\ndata: {"source":"jira","status":"error","error":"token expired"}\n\n' +
      'event: done\ndata: {"total_added":0}\n\n';

    mockFetch.mockResolvedValue(mockSSEResponse([sse]));

    const events: any[] = [];
    await api.syncStream((ev) => events.push(ev));

    const errorFrame = events.find(
      (e) => e.type === "freshness" && e.status === "error"
    );
    expect(errorFrame?.error).toBe("token expired");
    expect(events[events.length - 1].type).toBe("done");
  });

  it("rejects on transport error", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 503,
      statusText: "Service Unavailable",
      body: null,
      json: () => Promise.resolve({ error: "no syncers configured" }),
    });

    await expect(api.syncStream(() => {})).rejects.toThrow(
      "no syncers configured"
    );
  });
});

describe("api.log", () => {
  it("sends POST with text only", async () => {
    mockFetch.mockResolvedValue(
      mockJsonResponse({ id: 42, timestamp: "2026-04-08T10:00:00Z", title: "Talked to mobile team" })
    );

    const result = await api.log({ text: "Talked to mobile team" });

    expect(mockFetch).toHaveBeenCalledWith("http://127.0.0.1:3725/api/log", {
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
    expect(call[0]).toBe("http://127.0.0.1:3725/api/log");
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
