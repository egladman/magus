// A step that can fail returns its outcome as a value; the caller shows, skips or returns the error.
export type Result<T> = { ok: true; value: T } | { ok: false; error: Error };

// asError wraps a caught value, which is unknown, for a Result's error slot.
export function asError(e: unknown): Error {
  return e instanceof Error ? e : new Error(String(e));
}
