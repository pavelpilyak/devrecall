export const SOURCE_HUES: Record<string, string> = {
  git: "#f0883e",
  github: "#c7cdd6",
  gitlab: "#fc6d26",
  slack: "#c986e6",
  calendar: "#6aa9ff",
  jira: "#4a9eff",
  linear: "#9a8cff",
};

export type SyncStatus = "ok" | "syncing" | "warn" | "error";
