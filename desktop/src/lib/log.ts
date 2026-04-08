/**
 * Helpers for the manual log quick-add form.
 *
 * Kept in a plain TS module so the parsing/validation logic can be unit
 * tested without spinning up Svelte component tests.
 */

import type { LogRequest } from "./api";

/**
 * Parse a comma-separated input into trimmed, deduped, non-empty tokens.
 * Used for both tags and people fields in the quick-add form.
 */
export function parseCSV(input: string): string[] {
  if (!input) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of input.split(",")) {
    const v = raw.trim();
    if (!v) continue;
    if (seen.has(v)) continue;
    seen.add(v);
    out.push(v);
  }
  return out;
}

/**
 * Build a LogRequest payload from raw form fields.
 *
 * Throws if `text` is empty after trimming. Optional fields are dropped
 * from the payload entirely when empty so the API receives a clean object.
 */
export function buildLogRequest(form: {
  text: string;
  at?: string;
  tags?: string;
  people?: string;
}): LogRequest {
  const text = form.text.trim();
  if (!text) {
    throw new Error("Text is required");
  }

  const req: LogRequest = { text };

  const at = form.at?.trim();
  if (at) req.at = at;

  const tags = parseCSV(form.tags ?? "");
  if (tags.length > 0) req.tags = tags;

  const people = parseCSV(form.people ?? "");
  if (people.length > 0) req.people = people;

  return req;
}
