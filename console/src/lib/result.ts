// Result is how a fallible call reports failure: the caller either reads the value or handles
// the error, and nothing unwinds past it.
export type Result<T> = { ok: true; value: T } | { ok: false; error: Error };

export function succeed<T>(value: T): Result<T> {
  return { ok: true, value };
}

export function fail<T>(error: Error): Result<T> {
  return { ok: false, error };
}

// asError wraps a caught value, which is `unknown`, so a boundary can hand it on as a Result.
export function asError(caught: unknown): Error {
  return caught instanceof Error ? caught : new Error(String(caught));
}
