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

function tickToday() {
  const cur = todayISOString();
  if (cur !== _today) {
    _today = cur;
    today.set(cur);
  }
}

if (typeof window !== "undefined") {
  document.addEventListener("visibilitychange", tickToday);
  window.addEventListener("focus", tickToday);
  setInterval(tickToday, 60_000);
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
