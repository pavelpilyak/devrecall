/** Save a value to localStorage as JSON. */
export function save<T>(key: string, value: T): void {
  try {
    localStorage.setItem(`devrecall:${key}`, JSON.stringify(value));
  } catch {
    // Quota exceeded or private browsing — silently ignore.
  }
}

/** Load a value from localStorage. Returns undefined if missing or malformed. */
export function load<T>(key: string): T | undefined {
  try {
    const raw = localStorage.getItem(`devrecall:${key}`);
    if (raw === null) return undefined;
    return JSON.parse(raw) as T;
  } catch {
    return undefined;
  }
}

/** Remove a persisted value. */
export function remove(key: string): void {
  localStorage.removeItem(`devrecall:${key}`);
}
