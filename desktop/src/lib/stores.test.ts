import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { get } from "svelte/store";

const mockFetch = vi.fn();
vi.stubGlobal("fetch", mockFetch);

import { connected, apiStatus, checkConnection, today } from "./stores";

function mockJsonResponse(data: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "OK",
    json: () => Promise.resolve(data),
  };
}

beforeEach(() => {
  mockFetch.mockReset();
  connected.set(false);
  apiStatus.set(null);
});

describe("checkConnection", () => {
  it("sets connected=true and apiStatus on success", async () => {
    const statusData = { status: "ok", sources: [{ name: "git", enabled: true, count: 5 }] };
    mockFetch.mockResolvedValue(mockJsonResponse(statusData));

    await checkConnection();

    expect(get(connected)).toBe(true);
    expect(get(apiStatus)?.status).toBe("ok");
  });

  it("sets connected=false on fetch error", async () => {
    mockFetch.mockRejectedValue(new Error("Connection refused"));

    await checkConnection();

    expect(get(connected)).toBe(false);
    expect(get(apiStatus)).toBeNull();
  });

  it("sets connected=false on HTTP error", async () => {
    mockFetch.mockResolvedValue(mockJsonResponse({ error: "server error" }, 500));

    await checkConnection();

    expect(get(connected)).toBe(false);
    expect(get(apiStatus)).toBeNull();
  });
});

describe("today store", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("emits exactly once per actual day change", () => {
    const seen: string[] = [];
    const unsub = today.subscribe((v) => seen.push(v));
    const initial = seen.length;

    vi.useFakeTimers();
    vi.setSystemTime(new Date("2099-01-02T08:00:00Z"));
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("focus"));

    expect(seen.length).toBe(initial + 1);
    expect(get(today)).toBe("2099-01-02");

    unsub();
  });
});
