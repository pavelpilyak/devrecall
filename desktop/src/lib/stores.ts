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
