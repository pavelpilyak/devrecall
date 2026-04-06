/**
 * HTTP client for the DevRecall local API (localhost:9147).
 */

const BASE_URL = "http://127.0.0.1:9147";

export interface SourceStatus {
  name: string;
  enabled: boolean;
  synced_at?: string;
  count: number;
}

export interface StatusResponse {
  status: string;
  sources: SourceStatus[];
}

export interface StandupResponse {
  date: string;
  report: string;
  activity_count: number;
}

export interface WeeklyResponse {
  week_start: string;
  week_end: string;
  report: string;
  activity_count: number;
}

export interface Activity {
  id: number;
  source: string;
  source_id: string;
  type: string;
  title: string;
  content?: string;
  metadata?: string;
  timestamp: string;
}

export interface SearchResult {
  activity: Activity;
  rank: number;
}

export interface ChatResponse {
  response: string;
  sources_count: number;
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(`${BASE_URL}${path}`);
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || resp.statusText);
  }
  return resp.json();
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const resp = await fetch(`${BASE_URL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || resp.statusText);
  }
  return resp.json();
}

export const api = {
  status: () => get<StatusResponse>("/api/status"),

  standup: (date?: string) => {
    const params = date ? `?date=${date}` : "";
    return get<StandupResponse>(`/api/standup${params}`);
  },

  week: (weeksBack?: number) => {
    const params = weeksBack ? `?weeks_back=${weeksBack}` : "";
    return get<WeeklyResponse>(`/api/week${params}`);
  },

  activities: (opts?: {
    source?: string;
    type?: string;
    after?: string;
    before?: string;
    limit?: number;
  }) => {
    const params = new URLSearchParams();
    if (opts?.source) params.set("source", opts.source);
    if (opts?.type) params.set("type", opts.type);
    if (opts?.after) params.set("after", opts.after);
    if (opts?.before) params.set("before", opts.before);
    if (opts?.limit) params.set("limit", String(opts.limit));
    const qs = params.toString();
    return get<{ activities: Activity[]; count: number }>(
      `/api/activities${qs ? `?${qs}` : ""}`
    );
  },

  search: (query: string, opts?: { source?: string; limit?: number }) => {
    const params = new URLSearchParams({ q: query });
    if (opts?.source) params.set("source", opts.source);
    if (opts?.limit) params.set("limit", String(opts.limit));
    return get<{ query: string; results: SearchResult[]; count: number }>(
      `/api/search?${params}`
    );
  },

  chat: (message: string, history?: { role: string; content: string }[]) =>
    post<ChatResponse>("/api/chat", { message, history }),

  sync: () => post<{ message: string }>("/api/sync"),
};
