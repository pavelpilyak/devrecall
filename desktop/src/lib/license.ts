import { writable, derived } from "svelte/store";

export type Plan = "free" | "pro" | "team";

export type Feature =
  | "slack"
  | "calendar"
  | "github"
  | "gitlab"
  | "bitbucket"
  | "jira"
  | "linear"
  | "llm"
  | "chat"
  | "brag"
  | "backup"
  | "search";

export interface LicenseInfo {
  plan: Plan;
  features: Feature[];
  devices_used: number;
  devices_allowed: number;
  activated_at?: string;
}

export const FREE_LICENSE: LicenseInfo = {
  plan: "free",
  features: [],
  devices_used: 1,
  devices_allowed: 1,
};

export const licenseInfo = writable<LicenseInfo>(FREE_LICENSE);

export const currentPlan = derived(licenseInfo, ($lic) => $lic.plan);

export const isPro = derived(licenseInfo, ($lic) => $lic.plan === "pro" || $lic.plan === "team");

export function hasFeature(info: LicenseInfo, feature: Feature): boolean {
  if (info.plan === "free") return false;
  return info.features.includes(feature);
}

export function isProFeature(feature: Feature): boolean {
  const proFeatures: Feature[] = [
    "slack", "calendar", "github", "gitlab", "bitbucket",
    "jira", "linear", "llm", "chat", "brag", "backup", "search",
  ];
  return proFeatures.includes(feature);
}

export function featureLabel(feature: Feature): string {
  const labels: Record<Feature, string> = {
    slack: "Slack integration",
    calendar: "Google Calendar",
    github: "GitHub integration",
    gitlab: "GitLab integration",
    bitbucket: "Bitbucket integration",
    jira: "Jira integration",
    linear: "Linear integration",
    llm: "AI-powered standups",
    chat: "AI chat",
    brag: "Brag docs & perf reviews",
    backup: "Cloud backup",
    search: "Full-text search",
  };
  return labels[feature] || feature;
}
