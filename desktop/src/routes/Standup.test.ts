import { describe, it, expect, vi, afterEach } from "vitest";
import { render } from "@testing-library/svelte";
import { tick } from "svelte";
import "@testing-library/jest-dom/vitest";
import Standup from "./Standup.svelte";

function isoYesterdayOf(date: Date): string {
  const d = new Date(date);
  d.setDate(d.getDate() - 1);
  return d.toISOString().slice(0, 10);
}

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

describe("Standup auto-rolls date on day change", () => {
  it("snaps to new yesterday when today rolls over", async () => {
    const { container } = render(Standup);
    const meta = () => container.querySelector(".cta-meta")?.textContent ?? "";

    const initialYesterday = isoYesterdayOf(new Date());
    expect(meta()).toBe(initialYesterday);

    vi.useFakeTimers();
    const tomorrow = new Date();
    tomorrow.setDate(tomorrow.getDate() + 1);
    tomorrow.setHours(8, 0, 0, 0);
    vi.setSystemTime(tomorrow);

    window.dispatchEvent(new Event("focus"));
    await tick();

    expect(meta()).toBe(isoYesterdayOf(tomorrow));
    expect(meta()).not.toBe(initialYesterday);
  });
});
