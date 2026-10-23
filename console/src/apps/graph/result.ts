// A function that can fail returns a Result, and its caller shows, skips or returns the error.

export type Result<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly error: Error };

export function succeed<T>(value: T): Result<T> {
  return { ok: true, value };
}

export function fail<T = never>(message: string): Result<T> {
  return { ok: false, error: new Error(message) };
}

// failFrom wraps whatever a boundary threw, keeping an Error as it is.
export function failFrom<T = never>(thrown: unknown): Result<T> {
  return { ok: false, error: thrown instanceof Error ? thrown : new Error(String(thrown)) };
}
