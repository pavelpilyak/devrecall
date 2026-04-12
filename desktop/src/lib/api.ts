/**
 * HTTP client for the DevRecall local API.
 * Port defaults to 9147 but can be overridden via server.port in config.json.
 */

import { invoke } from "@tauri-apps/api/core";

let _baseUrl: string | null = null;

async function baseUrl(): Promise<string> {
  if (_baseUrl) return _baseUrl;
  try {
    _baseUrl = await invoke<string>("api_url");
  } catch {
    _baseUrl = "http://127.0.0.1:9147";
  }
  return _baseUrl;
}

export interface SourceStatus {
  name: string;
  enabled: boolean;
  synced_at?: string;
  count: number;
}

export interface LicenseInfo {
  plan: string;
  features: string[];
  devices_used: number;
  devices_allowed: number;
  activated_at?: string;
}

export interface StatusResponse {
  status: string;
  sources: SourceStatus[];
  license: LicenseInfo;
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

export interface ReviewResponse {
  period_start: string;
  period_end: string;
  report: string;
  activity_count: number;
  file_path?: string;
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

export interface LogResponse {
  id: number;
  timestamp: string;
  title: string;
}

export interface LogRequest {
  text: string;
  at?: string;
  tags?: string[];
  people?: string[];
}

/**
 * One event from POST /api/chat/stream. Only the fields relevant to `type`
 * are populated; switch on `type` before reading the rest.
 */
export type ChatStreamEvent =
  | { type: "thinking"; step: number }
  | { type: "token"; token: string }
  | {
      type: "tool_call";
      step: number;
      tool_name: string;
      tool_args?: unknown;
    }
  | {
      type: "tool_result";
      step: number;
      tool_name: string;
      tool_args?: unknown;
      tool_result?: unknown;
      tool_error?: string;
      duration_ms: number;
    }
  | { type: "done"; step: number; content: string }
  | { type: "error"; error: string };

async function get<T>(path: string): Promise<T> {
  const base = await baseUrl();
  const resp = await fetch(`${base}${path}`);
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || resp.statusText);
  }
  return resp.json();
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const base = await baseUrl();
  const resp = await fetch(`${base}${path}`, {
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

  /**
   * Open a streaming chat connection. Calls `onEvent` for each decoded
   * SSE event from the server. The returned promise resolves when the
   * stream closes (after a `done` or `error` event), and rejects on
   * transport errors. Pass an AbortSignal in `signal` to cancel.
   */
  chatStream: async (
    message: string,
    onEvent: (ev: ChatStreamEvent) => void,
    opts?: {
      history?: { role: string; content: string }[];
      signal?: AbortSignal;
    }
  ): Promise<void> => {
    const base = await baseUrl();
    const resp = await fetch(`${base}/api/chat/stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, history: opts?.history }),
      signal: opts?.signal,
    });
    if (!resp.ok || !resp.body) {
      const err = await resp.json().catch(() => ({ error: resp.statusText }));
      throw new Error(err.error || resp.statusText);
    }

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    let currentEvent = "";
    let dataLine = "";

    // SSE frames are delimited by a blank line; each frame has at most one
    // `event:` line and one `data:` line. We accumulate raw bytes, split on
    // newlines, and dispatch whenever we hit a blank line.
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });

      let idx: number;
      while ((idx = buf.indexOf("\n")) !== -1) {
        const line = buf.slice(0, idx);
        buf = buf.slice(idx + 1);

        if (line === "") {
          if (currentEvent && dataLine) {
            try {
              const parsed = JSON.parse(dataLine);
              onEvent({ type: currentEvent, ...parsed } as ChatStreamEvent);
            } catch {
              /* swallow malformed frame */
            }
          }
          currentEvent = "";
          dataLine = "";
          continue;
        }
        if (line.startsWith("event: ")) {
          currentEvent = line.slice("event: ".length).trim();
        } else if (line.startsWith("data: ")) {
          dataLine = line.slice("data: ".length);
        }
      }
    }
  },

  sync: () => post<{ message: string }>("/api/sync"),

  activate: (key: string) =>
    post<{ message: string; license: LicenseInfo }>("/api/activate", { key }),

  log: (req: LogRequest) => post<LogResponse>("/api/log", req),

  brag: (after: string, before: string) =>
    get<ReviewResponse>(`/api/brag?after=${after}&before=${before}`),

  perfReview: (after: string, before: string) =>
    get<ReviewResponse>(`/api/perf-review?after=${after}&before=${before}`),
};
