import { writable } from "svelte/store";
import { api, type StatusResponse } from "./api";

/** Whether we have a live connection to the DevRecall API. */
export const connected = writable(false);

/** Latest API status response. */
export const apiStatus = writable<StatusResponse | null>(null);

/** Bumped after each successful sync so data-displaying tabs can reload. */
export const lastSyncAt = writable(0);

/** Server startup error (e.g. broken config.json). */
export const serverError = writable("");

/**
 * Live reachability of the configured LLM. Driven by polling GET
 * /api/llm/health, so the sidebar reflects whether the LLM actually responds
 * — not merely whether a provider is configured. "checking" is the transient
 * in-flight state; "unknown" is the pre-first-check default.
 */
export type LLMHealth = {
  // "unsupported" = the running server predates the /api/llm/health route
  // (an older devrecall CLI), so live probing isn't possible.
  state: "unknown" | "checking" | "ok" | "error" | "unsupported";
  provider?: string;
  error?: string;
};
export const llmHealth = writable<LLMHealth>({ state: "unknown" });

/** Probe the LLM and update the llmHealth store. Safe to call frequently. */
export async function checkLLMHealth() {
  // Only surface "checking" on the very first probe; later background polls
  // update the result in place so the badge doesn't flicker every cycle.
  llmHealth.update((h) => (h.state === "unknown" ? { ...h, state: "checking" } : h));
  try {
    const r = await api.llmHealth();
    if (r.unsupported) {
      llmHealth.set({ state: "unsupported" });
      return;
    }
    llmHealth.set(
      r.ok
        ? { state: "ok", provider: r.provider }
        : { state: "error", provider: r.provider, error: r.error }
    );
  } catch {
    // The endpoint always returns 200, so a throw means the API server
    // itself is unreachable — that's the connection banner's job, not an LLM
    // fault. Reset to "unknown" so we don't flash a misleading LLM error.
    llmHealth.set({ state: "unknown" });
  }
}

/**
 * Update availability, driven by GET /api/update (a throttled, cached wrapper
 * over the CLI's passive GitHub-release check). "unsupported" = the running
 * server predates the route (older CLI).
 */
export type UpdateInfo = {
  // "check-failed" = the server reached us but couldn't reach GitHub (typically
  // a 403 rate limit on shared/corporate networks). We can't say whether an
  // update exists, but we still know the manual upgrade command.
  state: "unknown" | "up-to-date" | "available" | "unsupported" | "check-failed";
  current?: string;
  latest?: string;
  notes?: string;
  installMethod?: "cask" | "brew" | "standalone" | "unknown";
  upgradeCommand?: string;
};
export const updateInfo = writable<UpdateInfo>({ state: "unknown" });

/** Check for a newer release and update the updateInfo store. */
export async function checkUpdate() {
  try {
    const r = await api.update();
    if (r.unsupported) {
      updateInfo.set({ state: "unsupported" });
      return;
    }
    // The endpoint always 200s; a non-empty error means the GitHub lookup
    // failed. Don't claim "up to date" — surface the manual command instead.
    if (r.error) {
      updateInfo.set({
        state: "check-failed",
        current: r.current,
        installMethod: r.install_method,
        upgradeCommand: r.upgrade_command,
      });
      return;
    }
    updateInfo.set({
      state: r.update_available ? "available" : "up-to-date",
      current: r.current,
      latest: r.latest,
      notes: r.notes,
      installMethod: r.install_method,
      upgradeCommand: r.upgrade_command,
    });
  } catch {
    // API server unreachable — leave it to the connection banner.
    updateInfo.set({ state: "unknown" });
  }
}

function todayISOString(): string {
  return new Date().toISOString().slice(0, 10);
}

let _today = todayISOString();

/**
 * Local date as YYYY-MM-DD. Updates when the app crosses midnight.
 *
 * Why not just `setTimeout` to next midnight: the laptop sleeping pauses JS
 * timers against monotonic time, not wall clock — a timer aimed at midnight
 * before sleep can fire hours late. We instead poll wall-clock on focus,
 * visibility change, and every 60s, and only emit when the date string
 * actually changes (i.e. once per day).
 */
export const today = writable(_today);

/**
 * Wall-clock `Date.now()`, refreshed on the same focus/visibility/60s cycle
 * as `today`. Use this — not a local `setInterval` — when deriving "X m ago"
 * style timestamps, so the display catches up after the laptop wakes from
 * sleep instead of staying frozen at whatever value setInterval had paused on.
 */
export const nowTick = writable(Date.now());

function tick() {
  const cur = todayISOString();
  if (cur !== _today) {
    _today = cur;
    today.set(cur);
  }
  nowTick.set(Date.now());
}

if (typeof window !== "undefined") {
  document.addEventListener("visibilitychange", tick);
  window.addEventListener("focus", tick);
  setInterval(tick, 60_000);
}

/** Check connection to the API and update stores. */
export async function checkConnection() {
  try {
    const status = await api.status();
    apiStatus.set(status);
    connected.set(true);
  } catch {
    connected.set(false);
    apiStatus.set(null);
  }
}
